const { app, BrowserWindow, ipcMain, dialog, session } = require('electron');
const { spawn } = require('node:child_process');
const { randomBytes } = require('node:crypto');
const { createInterface } = require('node:readline');
const path = require('node:path');
const fs = require('node:fs');

const token = randomBytes(32).toString('hex');
const dev = process.env.CORNWATCH_DEV === '1' && !app.isPackaged;
let backend;
let backendOrigin;
let window;
let quitting = false;
let streamController;
let reconnectTimer;
const allowedGet = /^\/api\/(health|report-settings|daily-reports|daily-reports\/[a-zA-Z0-9_-]+|data-sources|market\/(live|history)|dashboard|bars|events|observations|sources|backtests|settings|research\/(market|current)|journals|journals\/[a-zA-Z0-9_-]+|journal-assets\/[a-zA-Z0-9_-]+|journal-reviews|journal-reviews\/[a-zA-Z0-9_-]+\/snapshot|journal-lessons)(\?[^#]*)?$/;
const allowedPost = /^\/api\/(report-settings(?:\/test)?|daily-reports\/generate|daily-reports\/[a-zA-Z0-9_-]+\/journal|data-sources\/(sina|liaoning|moa|weather|boyar_prices|boyar_arrivals|import_auction|market_bridge|industry_bridge)\/(config|test|sync)|events|events\/[a-zA-Z0-9_-]+\/analyze|observations|import\/bars|backtests|collect\/(weather|feeds)|research\/(risk|validate|current)|journals|journals\/[a-zA-Z0-9_-]+(?:\/archive)?|journal-assets|journal-reviews|journal-lessons\/[a-zA-Z0-9_-]+\/revoke|journal-reviews\/[a-zA-Z0-9_-]+\/lessons\/[a-zA-Z0-9_-]+\/accept)$/;

function startBackend() {
  return new Promise((resolve, reject) => {
    const root = path.join(__dirname, '../..');
    const bin = path.join(app.isPackaged ? process.resourcesPath : root, 'bin', process.platform === 'win32' ? 'cornwatch.exe' : 'cornwatch');
    if (!fs.existsSync(bin)) { reject(new Error('未找到后端程序，请先运行 npm run build。')); return; }
    const ui = app.isPackaged ? path.join(process.resourcesPath, 'ui') : path.join(root, 'desktop/dist');
    backend = spawn(bin, ['--addr', '127.0.0.1:0', '--data', path.join(app.getPath('userData'), 'cornwatch.db'), '--ui', ui], {
      cwd: app.isPackaged ? app.getPath('userData') : root,
      env: { ...process.env, CORNWATCH_TOKEN: token },
      stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true,
    });
    const timeout = setTimeout(() => reject(new Error('后端启动超过 20 秒，请检查日志。')), 20000);
    const lines = createInterface({ input: backend.stdout });
    lines.on('line', line => {
      try {
        const message = JSON.parse(line);
        if (message.event === 'listening') {
          const url = new URL(message.address);
          if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port) throw new Error('后端返回了非法监听地址');
          clearTimeout(timeout); backendOrigin = url.origin; resolve(backendOrigin);
        }
      } catch { /* Non-protocol backend log output is not exposed to the renderer. */ }
    });
    backend.stderr.on('data', chunk => process.stderr.write(chunk));
    backend.once('error', error => { clearTimeout(timeout); reject(error); });
    backend.once('exit', code => {
      clearTimeout(timeout);
      if (!backendOrigin) reject(new Error(`后端启动失败（退出码 ${code}）`));
      else if (!quitting) { dialog.showErrorBox('CornWatch 后端已停止', '请重新启动应用。已保存的数据不会丢失。'); app.quit(); }
    });
  });
}

function trustedFrame(event) {
  try { return new URL(event.senderFrame.url).origin === (dev ? 'http://127.0.0.1:5173' : backendOrigin) && event.sender === window?.webContents; }
  catch { return false; }
}

async function subscribeToUpdates() {
  if (quitting || !backendOrigin || !window) return;
  streamController = new AbortController();
  try {
    const response = await fetch(`${backendOrigin}/api/stream`, { headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' }, signal: streamController.signal, redirect: 'error' });
    if (!response.ok || !response.body) throw new Error('事件流连接失败');
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    while (!quitting) {
      const chunk = await reader.read();
      if (chunk.done) break;
      buffer += decoder.decode(chunk.value, { stream: true }).replace(/\r\n/g, '\n');
      if (buffer.length > 65536) throw new Error('事件流数据过长');
      let boundary;
      while ((boundary = buffer.indexOf('\n\n')) >= 0) {
        const block = buffer.slice(0, boundary); buffer = buffer.slice(boundary + 2);
        if (!/^event:\s*update$/m.test(block)) continue;
        const data = block.split('\n').filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n');
        try {
          const update = JSON.parse(data);
          if (typeof update.kind === 'string' && typeof update.at === 'string' && window && !window.isDestroyed()) window.webContents.send('cornwatch:update', { kind: update.kind, at: update.at });
        } catch { /* Ignore invalid payloads; the polling fallback remains active. */ }
      }
    }
  } catch { /* Reconnect below; UI also refreshes every 15 seconds. */ }
  if (!quitting) reconnectTimer = setTimeout(subscribeToUpdates, 2000);
}

app.whenReady().then(async () => {
  try {
    await startBackend();
    session.defaultSession.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
    // Localhost uses an ephemeral port. Store drafts outside origin-scoped
    // localStorage so an application restart cannot hide unsaved work.
    const draftPath = path.join(app.getPath('userData'), 'journal-drafts.json');
    ipcMain.on('cornwatch:read-journal-drafts', event => {
      let result = null;
      try {
        if (trustedFrame(event) && fs.statSync(draftPath).size <= 2 * 1024 * 1024) result = fs.readFileSync(draftPath, 'utf8');
      } catch { /* First launch has no draft file. */ }
      event.returnValue = result;
    });
    ipcMain.on('cornwatch:write-journal-drafts', (event, value) => {
      let result = false;
      try {
        if (trustedFrame(event) && typeof value === 'string' && Buffer.byteLength(value) <= 2 * 1024 * 1024) {
          const data = JSON.parse(value);
          if (data && typeof data === 'object' && !Array.isArray(data)) {
            fs.writeFileSync(`${draftPath}.tmp`, value, { mode: 0o600 });
            fs.renameSync(`${draftPath}.tmp`, draftPath);
            result = true;
          }
        }
      } catch { /* Renderer retains the draft and displays a save warning. */ }
      event.returnValue = result;
    });
    ipcMain.handle('cornwatch:capture-chart', async (event, rect) => {
      if (!trustedFrame(event) || !rect || typeof rect !== 'object') throw new Error('不受信任的截图请求');
      const [width, height] = window.getContentSize();
      if (!['x', 'y', 'width', 'height'].every(k => Number.isInteger(rect[k])) || rect.x < 0 || rect.y < 0 || rect.width < 1 || rect.height < 1 || rect.x + rect.width > width || rect.y + rect.height > height || rect.width * rect.height > 24000000) throw new Error('截图范围超出应用窗口');
      let timeout;
      const captured = await Promise.race([
        window.webContents.capturePage({ x: rect.x, y: rect.y, width: rect.width, height: rect.height }, { stayAwake: true }),
        new Promise((_resolve, reject) => { timeout = setTimeout(() => reject(new Error('截图超时，请保持应用可见后重试')), 10000); }),
      ]).finally(() => clearTimeout(timeout));
      const png = captured.toPNG();
      if (png.length > 4 * 1024 * 1024) throw new Error('截图超过 4 MiB，请缩小窗口后重试');
      return `data:image/png;base64,${png.toString('base64')}`;
    });
    ipcMain.handle('cornwatch:request', async (event, endpoint, options = {}) => {
      if (!trustedFrame(event)) throw new Error('不受信任的页面');
      if (typeof endpoint !== 'string' || typeof options !== 'object' || options === null) throw new Error('非法请求');
      const method = options.method ?? 'GET';
      if (!(method === 'GET' && allowedGet.test(endpoint)) && !(method === 'POST' && allowedPost.test(endpoint))) throw new Error('此接口不允许访问');
      const target = new URL(endpoint, backendOrigin);
      if (target.origin !== backendOrigin || endpoint.includes('..') || endpoint.includes('\\')) throw new Error('非法请求路径');
      const body = method === 'POST' && options.body !== undefined ? JSON.stringify(options.body) : undefined;
      if (body && Buffer.byteLength(body) > 12 * 1024 * 1024) throw new Error('请求超过 12 MiB 限制');
      const response = await fetch(target, { method, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body, signal: AbortSignal.timeout(120000), redirect: 'error' });
      const data = await response.json();
      if (!response.ok) throw new Error(`[HTTP ${response.status}] ${data.error || '请求失败'}`);
      return data;
    });
    window = new BrowserWindow({
      width: 1480, height: 980, minWidth: 1080, minHeight: 760,
      title: 'CornWatch · 玉米市场研究工作台', backgroundColor: '#0a1118', autoHideMenuBar: true,
      webPreferences: { preload: path.join(__dirname, 'preload.cjs'), contextIsolation: true, nodeIntegration: false, sandbox: true, webSecurity: true },
    });
    window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
    window.webContents.on('will-navigate', event => event.preventDefault());
    window.webContents.on('will-attach-webview', event => event.preventDefault());
    window.on('closed', () => { window = undefined; });
    await window.loadURL(dev ? 'http://127.0.0.1:5173' : backendOrigin);
    void subscribeToUpdates();
  } catch (error) { dialog.showErrorBox('CornWatch 启动失败', error.message); app.quit(); }
});
app.on('window-all-closed', () => app.quit());
app.on('will-quit', () => { quitting = true; clearTimeout(reconnectTimer); streamController?.abort(); backend?.kill(); });
