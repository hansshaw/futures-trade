// Runs the real Electron app with a disposable user profile and native sandbox.
// Uses Chromium's local debugging protocol; no extra automation dependencies.
import { spawn, spawnSync } from 'node:child_process';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { seedOfflineChartHistory } from './chart-test-fixtures.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const profile = await mkdtemp(path.join(tmpdir(), 'cornwatch-electron-'));
const userData = path.join(profile, 'cornwatch');
await seedOfflineChartHistory(root, userData, ['c2701']);
const listener = net.createServer();
await new Promise(resolve => listener.listen(0, '127.0.0.1', resolve));
const port = listener.address().port;
await new Promise(resolve => listener.close(resolve));
const env = { ...process.env, XDG_CONFIG_HOME: profile };
for (const key of Object.keys(env)) if (key.startsWith('LLM_') || key.startsWith('CORNWATCH_') || key === 'ELECTRON_RUN_AS_NODE') delete env[key];
env.CORNWATCH_AUTO_SYNC = 'false';
const binary = process.platform === 'darwin' ? 'node_modules/electron/dist/Electron.app/Contents/MacOS/Electron' : process.platform === 'win32' ? 'node_modules/electron/dist/electron.exe' : 'node_modules/electron/dist/electron';
const packagedBinary = process.argv[2];
const launchArgs = [...(packagedBinary ? [] : ['.']), `--remote-debugging-port=${port}`, `--user-data-dir=${userData}`];
let child = spawn(packagedBinary ? path.resolve(packagedBinary) : path.join(root, binary), launchArgs, { cwd: root, env, detached: process.platform !== 'win32', stdio: ['ignore', 'pipe', 'pipe'] });
let logs = '';
child.stdout.on('data', chunk => { logs += chunk; });
child.stderr.on('data', chunk => { logs += chunk; });
let socket;
let seq = 0;
const pending = new Map();
const errors = [];
function command(method, params = {}) {
  if(socket?.readyState!==WebSocket.OPEN)return Promise.reject(new Error('CDP is disconnected'));
  return new Promise((resolve, reject) => {
    const id = ++seq;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout ${method}`)); }, 15000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
}
async function evaluate(expression) {
  const response = await command('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (response.exceptionDetails) throw new Error(JSON.stringify(response.exceptionDetails));
  return response.result?.value;
}
async function until(expression, milliseconds = 15000) {
  for (let elapsed = 0; elapsed < milliseconds; elapsed += 200) {
    if (await evaluate(expression)) return;
    await delay(200);
  }
  throw new Error(`UI condition timed out: ${expression}`);
}
async function click(text, exact = true) {
  const result = await evaluate(`(() => { const e=[...document.querySelectorAll('button')].find(e=>${exact ? 'e.textContent.trim()===' : 'e.textContent.includes('}${JSON.stringify(text)}${exact ? '' : ')'}); if(!e) return false; e.click(); return true; })()`);
  if (!result) throw new Error(`Missing UI button ${text}`);
}
async function screenshot(name) {
  const result = await command('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(path.join(root, 'artifacts', name), Buffer.from(result.data, 'base64'));
}

async function attach() {
  let target;
  for (let i = 0; i < 150; i++) {
    if (child.exitCode !== null) throw new Error(`Electron exited ${child.exitCode}: ${logs}`);
    try { target = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find(t => t.type === 'page' && t.url.startsWith('http://127.0.0.1:')); } catch {}
    if (target) break;
    await delay(200);
  }
  if (!target) throw new Error(`Electron did not expose a page: ${logs}`);
  socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
  const connectedSocket=socket;
  socket.addEventListener('close',()=>{if(socket!==connectedSocket)return;for(const task of pending.values()){clearTimeout(task.timer);task.reject(new Error('CDP disconnected'));}pending.clear();});
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.id) {
      const task = pending.get(message.id);
      if (task) { clearTimeout(task.timer); pending.delete(message.id); message.error ? task.reject(message.error) : task.resolve(message.result); }
    }
    if (message.method === 'Runtime.exceptionThrown') errors.push(message.params.exceptionDetails.text);
  });
  await command('Runtime.enable');
}

try {
  await attach();
  await until(`document.body.innerText.includes('C2701') && document.querySelectorAll('.quote-card').length >= 4`);
  if (await evaluate(`typeof window.require !== 'undefined' || typeof process !== 'undefined'`)) throw new Error('Renderer Node isolation is missing');
  await mkdir(path.join(root, 'artifacts'), { recursive: true });
  await screenshot('electron-overview.png');
  await click('盘面研究');
  await until(`document.querySelector('.research-candles') && document.body.innerText.includes('前 20 日均量')`);
  await screenshot('electron-research.png');
  if (!await evaluate(`document.querySelector('.research-risk form').checkValidity()`)) throw new Error('Risk example form is not valid');
  await click('计算风险允许手数');
  await until(`document.querySelector('.research-risk-result')?.textContent.includes('约束内上限')`);
  await click('截图存入复盘');
  await until(`document.querySelector('.journal-title-input')?.value.includes('盘面复盘') && document.querySelectorAll('.journal-assets img').length === 1`);
  await evaluate(`(() => { const fields=document.querySelectorAll('.journal-section-field textarea'); const set=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set; set.call(fields[3],'测试复盘：未按事先计划核验量仓，今天保持观望。');fields[3].dispatchEvent(new Event('input',{bubbles:true})); })()`);
  await evaluate(`(() => { const e=document.querySelectorAll('.journal-section-field textarea')[4]; Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(e,'每次入场前记录假设失效条件，先核对风险预算。');e.dispatchEvent(new Event('input',{bubbles:true})); })()`);
  await click('保存复盘');
  await until(`document.querySelector('.journal-save-state')?.textContent.includes('已保存 · v1')`);
  const firstNote = await evaluate(`window.cornwatch.request('/api/journals').then(x=>x[0])`);
  if (firstNote.attachment_ids.length !== 1 || firstNote.pnl !== null) throw new Error('Journal screenshot seed or nullable PNL lost');
  // Upload a real screenshot through the native file input.
  const imgDoc = await command('DOM.getDocument');
  const imgInput = await command('DOM.querySelector',{nodeId:imgDoc.root.nodeId,selector:'.journal-dropzone input[type="file"]'});
  await command('DOM.setFileInputFiles',{nodeId:imgInput.nodeId,files:[path.join(root,'artifacts/electron-overview.png')]});
  await until(`document.querySelectorAll('.journal-assets img').length === 2`);
  await evaluate(`(async()=>{const img=document.querySelector('.journal-assets img');const bytes=Uint8Array.from(atob(img.src.split(',')[1]),c=>c.charCodeAt(0));const data=new DataTransfer();data.items.add(new File([bytes],'pasted-chart.png',{type:'image/png'}));document.querySelector('.journal-editor').dispatchEvent(new ClipboardEvent('paste',{clipboardData:data,bubbles:true,cancelable:true}));})()`);
  await until(`document.querySelectorAll('.journal-assets img').length === 3`);
  await evaluate(`document.dispatchEvent(new KeyboardEvent('keydown',{key:'s',ctrlKey:true,bubbles:true}))`);
  await until(`document.querySelector('.journal-save-state')?.textContent.includes('已保存 · v2')`);
  await evaluate(`document.querySelector('.journal-image-button').click()`);
  await until(`document.querySelector('[role="dialog"] img') !== null`);
  await screenshot('electron-journal-image.png');
  await evaluate(`document.querySelector('[aria-label="关闭弹窗"]').click()`);
  await evaluate(`document.querySelector('.journal-editor').scrollIntoView({block:'start'})`);
  await screenshot('electron-journal.png');
  await click('复盘智能体');
  await click('使用本地规则整理');
  await until(`document.querySelector('.journal-review-card')?.textContent.includes('规则整理')`);
  await click('采纳经验');
  await until(`document.body.innerText.includes('经验已采纳')`);
  await evaluate(`document.querySelector('.journal-agent-layout').scrollIntoView({block:'start'})`);
  await screenshot('electron-journal-agent.png');
  await click('经验库',false);
  await click('撤回采纳');
  await until(`document.querySelectorAll('.journal-library-item').length === 0 && document.body.innerText.includes('已撤回采纳')`);
  if ((await evaluate(`window.cornwatch.request('/api/journal-lessons')`)).length !== 0) throw new Error('Lesson revoke failed');
  await click('复盘智能体');
  await click('采纳经验');
  await until(`document.body.innerText.includes('经验已采纳')`);
  const reviewSnapshot = await evaluate(`window.cornwatch.request('/api/journal-reviews').then(x=>window.cornwatch.request('/api/journal-reviews/'+x[0].id+'/snapshot'))`);
  if (reviewSnapshot.journals[0].revision !== 2) throw new Error('Review version snapshot mismatch');
  // A concurrent edit must be rejected without losing the local draft.
  await evaluate(`[...document.querySelectorAll('.journal-tabs button')].find(e=>e.textContent.startsWith('复盘日记')).click()`);
  await evaluate(`window.cornwatch.request('/api/journals').then(x=>window.cornwatch.request('/api/journals/'+x[0].id,{method:'POST',body:{...x[0],review:'并发更新：远端已保存的原文'}}))`);
  await evaluate(`(()=>{const e=document.querySelectorAll('.journal-section-field textarea')[3];Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(e,'冲突时必须保留的本地复盘');e.dispatchEvent(new Event('input',{bubbles:true}));})()`);
  await click('保存复盘');
  await until(`document.querySelector('.journal-conflict') !== null`);
  if (!await evaluate(`window.cornwatch.readJournalDrafts()?.includes('冲突时必须保留的本地复盘')`)) throw new Error('Conflict lost draft');
  const remote = await evaluate(`window.cornwatch.request('/api/journals').then(x=>x[0])`);
  if (remote.review !== '并发更新：远端已保存的原文' || remote.revision !== 3) throw new Error('Conflict overwrote saved source');
  // Deliberately leave a draft, navigate away, and verify the process-level draft file.
  await click('新建复盘');
  await evaluate(`(() => {const e=document.querySelector('.journal-title-input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,'跨启动保留的未保存复盘');e.dispatchEvent(new Event('input',{bubbles:true}));})()`);
  if (!await evaluate(`window.cornwatch.readJournalDrafts()?.includes('跨启动保留的未保存复盘')`)) throw new Error('Desktop draft not persisted');
  await click('回测实验室');
  await click('运行回测', false);
  await until(`document.body.innerText.includes('100,490.40') || document.body.innerText.includes('回测已完成')`);
  await click('运行时间切分检验');
  await until(`document.querySelectorAll('.validation-card').length === 2`);
  await screenshot('electron-backtest.png');
  await click('事件雷达', false);
  await evaluate(`document.querySelector('.event-card').click()`);
  await click('规则分析');
  await until(`document.body.innerText.includes('rules-v1')`);
  await screenshot('electron-event.png');
  await evaluate(`document.querySelector('[aria-label="关闭弹窗"]').click()`);
  await click('录入事件');
  await evaluate(`(() => { const values={title:'桌面集成测试事件',body:'临时测试：报价2200元/吨。仅测试数据，退出后删除。',region:'山东',source:'Electron smoke test'}; for(const [name,value] of Object.entries(values)){const input=document.querySelector('[name="'+name+'"]');input.value=value;input.dispatchEvent(new Event('input',{bubbles:true}));} })()`);
  await click('保存事件');
  await until(`document.querySelector('[role="dialog"]') === null && document.body.innerText.includes('桌面集成测试事件')`);
  await click('数据与设置');
  await until(`document.body.innerText.includes('LLM_BASE_URL') && document.body.innerText.includes('Open-Meteo')`);
  await screenshot('electron-settings.png');
  // Verify the actual file picker and controlled source input against the API.
  const csvPath = path.join(profile, 'synthetic-ui-test.csv');
  await writeFile(csvPath, 'symbol,timestamp,trade_date,open,high,low,close,settlement,volume,open_interest,limit_up,limit_down\nc2701,2026-09-01T15:00:00+08:00,2026-09-01,2200,2220,2190,2210,2205,10000,20000,2400,2000\n');
  await click('导入行情 CSV');
  const document = await command('DOM.getDocument');
  const input = await command('DOM.querySelector', { nodeId: document.root.nodeId, selector: '.modal input[type="file"]' });
  await command('DOM.setFileInputFiles', { nodeId: input.nodeId, files: [csvPath] });
  await until(`document.querySelector('.code-input')?.value.includes('c2701')`);
  await evaluate(`(() => { const input = document.querySelector('.modal input:not([type="file"])'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'synthetic isolated UI test'); input.dispatchEvent(new Event('input',{bubbles:true})); })()`);
  await click('校验并导入');
  await until(`document.body.innerText.includes('新增 1 条')`);
  await evaluate(`document.querySelector('[aria-label="关闭弹窗"]').click()`);
  await click('事件雷达', false);
  // No direct React refresh: a backend mutation must arrive through SSE.
  await evaluate(`window.cornwatch.request('/api/events',{method:'POST',body:{title:'SSE桌面即时更新测试',body:'临时测试记录，退出后删除。',source:'isolated SSE test',published_at:'2026-09-01T08:00:00+08:00',category:'news',region:'测试',severity:'high'}})`);
  await until(`document.querySelector('.event-list')?.textContent.includes('SSE桌面即时更新测试')`, 8000);
  const draftFiles = await readFile(path.join(userData,'journal-drafts.json'),'utf8');
  if (!draftFiles.includes('跨启动保留的未保存复盘')) throw new Error('Draft file missing after navigation');
  // Restart the actual desktop process with the same disposable profile. The
  // backend will choose a new port; drafts must survive this origin change.
  const exited = new Promise(resolve => child.once('exit',resolve));
  await Promise.race([exited,command('Browser.close').catch(()=>{})]);
  await Promise.race([exited,delay(5000)]);
  if(child.exitCode===null) throw new Error('Desktop did not exit cleanly');
  socket.close();
  await delay(250);
  child = spawn(packagedBinary ? path.resolve(packagedBinary) : path.join(root,binary), launchArgs,{cwd:root,env,detached:process.platform !== 'win32',stdio:['ignore','pipe','pipe']});
  child.stdout.on('data', chunk => { logs += chunk; });
  child.stderr.on('data', chunk => { logs += chunk; });
  await attach();
  await until(`document.querySelector('.sidebar nav') !== null`);
  await click('复盘日记');
  await until(`document.querySelector('.journal-title-input')?.value === '跨启动保留的未保存复盘'`);
  const persistedNotes = await evaluate(`window.cornwatch.request('/api/journals')`);
  if (!persistedNotes.some(note=>note.id===firstNote.id && note.attachment_ids.length===3 && note.revision===3)) throw new Error('Saved journal did not survive desktop restart');
  await screenshot('electron-journal-restart.png');
  await command('Emulation.setDeviceMetricsOverride',{width:1080,height:800,deviceScaleFactor:1,mobile:false});
  await delay(300);
  if (await evaluate(`document.documentElement.scrollWidth > innerWidth + 2`)) throw new Error('Journal overflows the minimum desktop width');
  await screenshot('electron-journal-compact.png');
  if (errors.length) throw new Error(`Renderer exceptions: ${errors.join(', ')}`);
  console.log('PASS: real sandboxed Electron, private backend IPC, market indicators/risk, chart capture, journal save/upload/paste/preview/CtrlS/conflict, rules agent/acceptance/revoke/snapshot, desktop restart/draft recovery, OOS, dashboard, backtest, events, CSV, SSE, settings. Screenshots: artifacts/electron-*.png');
} catch (error) {
  try { await screenshot('electron-failure.png'); } catch {}
  throw error;
} finally {
  if (child.exitCode===null && socket?.readyState===WebSocket.OPEN) {
    try { await command('Browser.close'); } catch {}
    await delay(500);
  }
  socket?.close();
  for (const task of pending.values()) clearTimeout(task.timer);
  try {
    if (process.platform === 'win32') {
      if (child.exitCode === null && child.pid) spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', timeout: 10000 });
    } else process.kill(-child.pid, 'SIGKILL');
  } catch {}
  await delay(400);
  await rm(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
}
