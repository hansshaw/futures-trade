// v0.5: sandboxed packaged Electron, disposable profile, real public Sina quotes
// and actual-contract daily data, plus a LOOPBACK MOCK MODEL and clearly marked
// synthetic industry inputs. This verifies UI/protocol behavior, NOT AI quality,
// provider-account compatibility, paid billing, or unattended real scheduling.
// Build first. Run: node scripts/reports-ui-smoke.mjs [packaged-binary]
// Use --visual-only to replay the prior exported report without network calls.
// All databases/configuration are temporary and removed even after failures.
import { spawn, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdtemp, mkdir, writeFile, readFile, readdir, stat, rm } from 'node:fs/promises';
import http from 'node:http';
import net from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const root = fileURLToPath(new URL('../', import.meta.url));
const binary = process.argv.slice(2).find(value => !value.startsWith('--'));
const visualOnly = process.argv.includes('--visual-only');
const profile = await mkdtemp(path.join(tmpdir(), 'cornwatch-reports-ui-'));
const userData = path.join(profile, 'cornwatch');
const downloads = path.join(profile, 'downloads');
const artifacts = path.join(root, 'artifacts', 'v5-ui');
const testKey = randomBytes(24).toString('hex');
const clean = value => String(value).split(testKey).join('[isolated test credential]');
const checks = [];
const modelRequests = [];
const reports = [];
const rendererErrors = [];
const fixtureSource = '隔离 UI 验收合成产业样例，非真实市场数据';
const modelName = 'loopback-mock-not-real-ai';
let live, publicSync, fixtures;
const now = new Date();
const researchDate = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(now);

function assert(condition, message) { if (!condition) throw new Error(message); }
async function mockModel(request, response) {
  const send = (status, body) => { response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); response.end(JSON.stringify(body)); };
  try {
    if (request.method !== 'POST' || request.url !== '/v1/chat/completions') { send(404, { error: 'isolated mock route only' }); return; }
    let raw = '';
    for await (const chunk of request) { raw += chunk; if (raw.length > 600000) throw new Error('mock request too large'); }
    const payload = JSON.parse(raw);
    const input = JSON.parse(payload.messages.find(message => message.role === 'user').content);
    assert(Array.isArray(input.evidence) && input.evidence.length > 0, 'Model received no auditable evidence');
    assert(!raw.includes(testKey), 'Secret leaked into model messages');
    const selected = [...new Map(input.evidence.map(item => [item.category, item])).values()].slice(0, 10);
    const isTest = input.symbol === '连接测试';
    const output = {
      summary: isTest ? '隔离连接测试：模拟服务按严格引用协议返回，不能据此判断市场。' : `隔离验收模拟回复：${input.symbol} ${input.date} ${input.kind === 'pre' ? '盘前' : '盘后'}。真实公开行情与日线，合成产业样例；不代表真实 AI 分析质量。`,
      sections: selected.map(item => ({
        title: `隔离模拟整理 · ${item.title}`,
        body: `事实：输入依据的原文和可得时间保留如下。${String(item.text).slice(0, 240)}\n推断/待验证：这是本地确定性测试回复，没有评估方向或预测能力；产业样例不是现实供需结论。`,
        evidence_ids: [item.id], quotes: [{ evidence_id: item.id, text: [...item.text].slice(0, 100).join('') }],
      })),
      scenarios: isTest ? [] : [{ name: '隔离验收观察条件', condition: '核对市场来源、观测日期与可得时间后，再独立研究。', invalidation: '来源失效、字段口径不一致或输入缺失时，不采纳推断。', evidence_ids: [selected[0].id] }],
      risks: ['本回复来自本机模拟服务，仅用于界面与引用校验；没有验证真实模型质量或付费账户。', '产业样例是明确标注的合成验收数据，不可作为实际交易依据。'],
    };
    modelRequests.push({ at: new Date().toISOString(), kind: input.kind, date: input.date, symbol: input.symbol, is_connection_test: isTest, model: payload.model, credential_sent: request.headers.authorization === `Bearer ${testKey}`, credential_absent: !request.headers.authorization, evidence_count: input.evidence.length, evidence_ids: input.evidence.map(item => item.id) });
    send(200, { choices: [{ message: { content: JSON.stringify(output) }, finish_reason: 'stop' }] });
  } catch (error) { send(400, { error: clean(error.message) }); }
}
const modelA = http.createServer((request, response) => void mockModel(request, response));
const modelB = http.createServer((request, response) => void mockModel(request, response));
await Promise.all([new Promise(resolve => modelA.listen(0, '127.0.0.1', resolve)), new Promise(resolve => modelB.listen(0, '127.0.0.1', resolve))]);
const endpointA = `http://127.0.0.1:${modelA.address().port}/v1`;
const endpointB = `http://127.0.0.1:${modelB.address().port}/v1`;
const listener = net.createServer();
await new Promise(resolve => listener.listen(0, '127.0.0.1', resolve));
const cdpPort = listener.address().port;
await new Promise(resolve => listener.close(resolve));
const env = { ...process.env, XDG_CONFIG_HOME: profile };
for (const key of Object.keys(env)) if (key.startsWith('LLM_') || key.startsWith('CORNWATCH_') || key === 'ELECTRON_RUN_AS_NODE') delete env[key];
env.CORNWATCH_AUTO_SYNC = 'false';
env.CORNWATCH_REPORT_AUTO = 'false';
const electron = process.platform === 'darwin' ? 'node_modules/electron/dist/Electron.app/Contents/MacOS/Electron' : process.platform === 'win32' ? 'node_modules/electron/dist/electron.exe' : 'node_modules/electron/dist/electron';
// Visual-only replay seeds previously exported immutable data BEFORE Electron
// opens this disposable bbolt file. It performs no public or model request.
if (visualOnly) {
  try {
    const previous = JSON.parse(await readFile(path.join(artifacts, 'report.json'), 'utf8'));
    const marketInput = JSON.parse(await readFile(path.join(artifacts, 'live-market-input.json'), 'utf8'));
    const saved = await Promise.all(['pre-rules', 'pre-llm', 'post-llm'].map(async name => JSON.parse(await readFile(path.join(artifacts, `report-${name}-mock-model.json`), 'utf8')).report));
    const meta = { report_settings_v1: { revision: 5, base_url: endpointA, model: modelName + '-newer', api_key: testKey, enabled: true, pre_time: '08:30', post_time: '15:30', auto_save: true, skip_dates: ['2027-01-01'] }, datahub_config_sina: marketInput.public_sync.source.config, datahub_record_sina: { ...marketInput.public_sync.source, quotes: marketInput.live.quotes, main: marketInput.live.main, price_history: marketInput.live.price_history } };
    for (const report of saved) meta['daily_report_doc_' + report.id] = report;
    for (const job of previous.jobs) meta['daily_report_job_' + job.id] = job;
    await mkdir(userData, { recursive: true });
    const seedPath = path.join(profile, 'visual-seed.json'), goPath = path.join(profile, 'seed.go');
    await writeFile(seedPath, JSON.stringify(meta), { mode: 0o600 });
    await writeFile(goPath, `package main
import("encoding/json";"os";bolt "go.etcd.io/bbolt")
func main(){raw,e:=os.ReadFile(os.Args[2]);if e!=nil{panic(e)};var meta map[string]json.RawMessage;if e=json.Unmarshal(raw,&meta);e!=nil{panic(e)};db,e:=bolt.Open(os.Args[1],0600,nil);if e!=nil{panic(e)};defer db.Close();e=db.Update(func(tx *bolt.Tx)error{b,e:=tx.CreateBucketIfNotExists([]byte("meta"));if e!=nil{return e};for k,v:=range meta{if e=b.Put([]byte(k),v);e!=nil{return e}};return nil});if e!=nil{panic(e)}}
`);
    const seeded = spawnSync('go', ['run', goPath, path.join(userData, 'cornwatch.db'), seedPath], { cwd: path.join(root, 'backend'), encoding: 'utf8', timeout: 60000 });
    if (seeded.status !== 0) throw new Error(clean(`Visual fixture seed failed: ${seeded.stderr}`));
  } catch (error) {
    for (const server of [modelA, modelB]) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
    await rm(profile, { recursive: true, force: true }); throw error;
  }
}
const child = spawn(binary ? path.resolve(binary) : path.join(root, electron), [...(binary ? [] : ['.']), `--remote-debugging-port=${cdpPort}`, `--user-data-dir=${userData}`], { cwd: root, env, detached: process.platform !== 'win32', stdio: ['ignore', 'pipe', 'pipe'] });
let logs = '', spawnError, socket, sequence = 0;
const pending = new Map();
child.stdout.on('data', value => { logs = (logs + value).slice(-12000); });
child.stderr.on('data', value => { logs = (logs + value).slice(-12000); });
child.once('error', error => { spawnError = error; });

function command(method, params = {}) {
  if (socket?.readyState !== WebSocket.OPEN) return Promise.reject(new Error('CDP disconnected'));
  return new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 45000);
    pending.set(id, { resolve, reject, timer }); socket.send(JSON.stringify({ id, method, params }));
  });
}
async function evaluate(expression) {
  const result = await command('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(clean(JSON.stringify(result.exceptionDetails)));
  return result.result?.value;
}
async function until(expression, timeout = 20000) {
  const start = Date.now();
  while (Date.now() - start < timeout) { if (await evaluate(`Promise.resolve((${expression})).then(Boolean)`)) return; await delay(150); }
  throw new Error(clean(`UI condition timed out: ${expression}`));
}
async function api(pathname, options) { return evaluate(`window.cornwatch.request(${JSON.stringify(pathname)}${options ? `,${JSON.stringify(options)}` : ''})`); }
async function navigate(label) {
  const clicked = await evaluate(`(() => {const button=[...document.querySelectorAll('.sidebar nav button')].find(item=>item.textContent.trim()===${JSON.stringify(label)});if(!button||button.disabled)return false;button.click();return true;})()`);
  assert(clicked, `Navigation unavailable: ${label}`);
}
async function clickIn(selector, label) {
  const clicked = await evaluate(`(() => {const parent=document.querySelector(${JSON.stringify(selector)});const button=parent&&[...parent.querySelectorAll('button')].find(item=>item.textContent.trim()===${JSON.stringify(label)});if(!button||button.disabled)return false;button.click();return true;})()`);
  assert(clicked, `Button missing or disabled: ${selector} / ${label}`);
}
async function setInput(selector, value) {
  const changed = await evaluate(`(() => {const input=document.querySelector(${JSON.stringify(selector)});if(!input||input.disabled)return false;const prototype=input.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:input.tagName==='SELECT'?HTMLSelectElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(prototype,'value').set.call(input,${JSON.stringify(value)});input.dispatchEvent(new Event(input.tagName==='SELECT'?'change':'input',{bubbles:true}));return true;})()`);
  assert(changed, `Input unavailable: ${selector}`);
}
async function viewport(width) {
  await command('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false }); await delay(200);
  assert(!await evaluate('document.documentElement.scrollWidth > innerWidth + 2'), `Horizontal document overflow at ${width}px`);
}
async function screenshot(file, selector) {
  if (selector) await evaluate(`(() => {const item=document.querySelector(${JSON.stringify(selector)});if(item)window.scrollTo({top:Math.max(0,window.scrollY+item.getBoundingClientRect().top-90),behavior:'instant'});})()`);
  await delay(250);
  const result = await command('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(path.join(artifacts, file), Buffer.from(result.data, 'base64'));
}
async function savedSettings() { return api('/api/report-settings'); }
async function saveSettings() {
  const revision = (await savedSettings()).revision;
  await clickIn('.dr-settings', '保存报告设置');
  await until(`window.cornwatch.request('/api/report-settings').then(item=>item.revision>${revision})`);
  await until(`!document.querySelector('.dr-settings-state') && document.querySelector('.dr-settings input[type="password"]')?.value===''`);
  return savedSettings();
}
function settingsInput(settings, patch = {}) {
  return { revision: settings.revision, base_url: settings.base_url, model: settings.model, enabled: settings.enabled, pre_time: settings.pre_time, post_time: settings.post_time, auto_save: settings.auto_save, skip_dates: settings.skip_dates || [], ...patch };
}
async function generate(kind, mode) {
  const before = new Set((await api('/api/daily-reports')).jobs.map(job => job.id));
  if (mode === 'rules') await setInput('select[aria-label="规则预览时段"]', kind);
  await clickIn('.daily-reports', mode === 'rules' ? '规则预览' : `生成${kind === 'pre' ? '盘前' : '盘后'} AI 分析`);
  let job;
  const started = Date.now();
  while (Date.now() - started < 175000) {
    const list = await api('/api/daily-reports');
    job = list.jobs.find(item => !before.has(item.id));
    if (job && ['succeeded', 'failed', 'interrupted'].includes(job.status) && !list.busy) break;
    await delay(350);
  }
  assert(job?.status === 'succeeded', clean(`Report ${kind}/${mode} did not succeed: ${JSON.stringify(job)}`));
  const report = await api(`/api/daily-reports/${job.report_id}`);
  await until(`document.querySelector('.dr-provenance')?.textContent.includes(${JSON.stringify(report.id)})`);
  assert(report.kind === kind && report.mode === mode && report.output.mode === mode, 'Report modes or periods were mislabeled');
  assert(report.bundle.current.daily_source.includes('新浪') && !report.bundle.current.technical.is_demo, 'Report did not use real actual-contract daily observations');
  assert(report.bundle.input_sha256 === report.output.input_sha256, 'Report output lost input fingerprint');
  assert(report.bundle.evidence.some(item => item.category === 'market' && item.source.includes('新浪')), 'Report lacks its actual public market source');
  for (const item of report.bundle.evidence) {
    assert(Date.parse(item.available_at) <= Date.parse(report.bundle.as_of), `Future availability entered report: ${item.id}`);
    if (!item.is_forecast) assert(Date.parse(item.observed_at) <= Date.parse(report.bundle.as_of), `Future realized data entered report: ${item.id}`);
  }
  for (const section of report.output.sections) for (const quote of section.quotes) {
    const item = report.bundle.evidence.find(evidence => evidence.id === quote.evidence_id);
    assert(item && [item.title, item.text, item.note || ''].some(text => text.includes(quote.text)), 'Model quote does not exactly match its evidence');
  }
  reports.push(report);
  await writeFile(path.join(artifacts, `report-${kind}-${mode}-mock-model.json`), JSON.stringify({ notice: '真实公开行情/日线 + 明确合成产业样例 + 本地模拟模型；仅验证流程，不代表真实模型分析质量。', report }, null, 2));
  return report;
}
async function verifyFixtures(report) {
  const evidence = report.bundle.evidence;
  for (const category of ['purchase_price', 'truck_arrivals', 'weather_forecast', 'import', 'related_price']) assert(evidence.some(item => item.category === category && item.source === fixtureSource), `Missing marked fixture category: ${category}`);
  const weather = evidence.find(item => item.category === 'weather_forecast');
  assert(weather.is_forecast && weather.original_ids.length === 2 && weather.text.includes('预报'), 'Weather lost forecast label or underlying metric IDs');
  const unknown = evidence.find(item => item.unit === '未注明');
  assert(unknown?.note.includes('禁止猜测'), 'Unknown price unit lost its no-conversion guard');
  assert(evidence.filter(item => item.category === 'truck_arrivals').length === 2, 'Arrival comparison lost the prior observation');
  assert(evidence.find(item => item.category === 'import')?.note.includes('进口存量拍卖'), 'Imports were not distinguished from new purchases or arrivals');
}

try {
  await Promise.all([mkdir(artifacts, { recursive: true }), mkdir(downloads, { recursive: true })]);
  let target;
  for (let i = 0; i < 150; i++) {
    if (spawnError) throw spawnError;
    if (child.exitCode !== null) throw new Error(clean(`Electron exited ${child.exitCode}: ${logs}`));
    try { target = (await (await fetch(`http://127.0.0.1:${cdpPort}/json/list`)).json()).find(item => item.type === 'page' && item.url.startsWith('http://127.0.0.1:')); } catch {}
    if (target) break; await delay(200);
  }
  assert(target, clean(`No Electron page: ${logs}`));
  socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.id) { const task = pending.get(message.id); if (task) { clearTimeout(task.timer); pending.delete(message.id); message.error ? task.reject(new Error(clean(JSON.stringify(message.error)))) : task.resolve(message.result); } }
    if (message.method === 'Runtime.exceptionThrown') rendererErrors.push(clean(message.params.exceptionDetails.text));
  });
  socket.addEventListener('close', () => { for (const task of pending.values()) { clearTimeout(task.timer); task.reject(new Error('CDP disconnected')); } pending.clear(); });
  await command('Runtime.enable');
  await command('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: downloads });
  await until(`document.querySelector('.sidebar nav') && window.cornwatch`);
  assert(!await evaluate(`typeof window.require!=='undefined'||typeof process!=='undefined'`), 'Renderer Node isolation missing');
  assert((await stat(path.join(userData, 'cornwatch.db'))).isFile(), 'Refusing writes outside disposable database');
  assert((await api('/api/settings')).live_trading === false, 'Refusing live trading state');
  if (visualOnly) {
    await navigate('每日分析');
    await until(`document.querySelector('.dr-report .dr-summary')?.textContent.includes('隔离验收模拟回复')`);
    await clickIn('.daily-reports', '报告设置');
    await until(`document.querySelector('.dr-settings input[type="password"]')`);
    assert(await evaluate(`getComputedStyle(document.querySelector('.dr-checkbox')).flexDirection==='row'`), 'Settings checkbox is stacked by global label CSS');
    assert(await evaluate(`Math.abs(document.querySelectorAll('.dr-inline-fields input')[0].getBoundingClientRect().top-document.querySelectorAll('.dr-inline-fields input')[1].getBoundingClientRect().top)<2`), 'Schedule time controls are vertically misaligned');
    assert(await evaluate(`parseFloat(getComputedStyle(document.querySelector('.dr-settings .secondary-button')).fontSize)<=13`), 'Report button inherited body font size');
    for (const width of [1400, 1080]) { await viewport(width); await screenshot(`reports-settings-${width}-mock-model.png`, '.dr-settings'); }
    await evaluate(`document.querySelector('button[aria-label="收起报告设置"]').click()`);
    await evaluate(`document.querySelector('.dr-report .dr-citation').click()`);
    await until(`document.querySelector('.dr-evidence')?.open && document.querySelector('.dr-evidence-item.focused')`);
    assert(await evaluate(`document.querySelector('.dr-evidence-item.focused')?.textContent.includes('行情快照')`), 'Evidence category was not localized');
    for (const width of [1400, 1080]) {
      await viewport(width);
      await screenshot(`evidence-${width}-mock-model.png`, '.dr-evidence-item.focused');
      await screenshot(`report-${width}-mock-model.png`, '.dr-report-heading');
      await screenshot(`daily-workspace-${width}-mock-model.png`, '.daily-reports');
    }
    assert(modelRequests.length===0 && rendererErrors.length===0, 'Visual replay called model or raised renderer errors');
    await writeFile(path.join(artifacts, 'visual-replay.json'), JSON.stringify({passed:true,created_at:new Date().toISOString(),binary:binary||'development Electron',mode:'只读视觉回放：复用先前真实行情/日线+模拟模型报告，未重新采集或调用模型',viewports:[1080,1400],checks:['checkbox row layout','aligned schedule inputs','scoped button typography','localized evidence categories','no document horizontal overflow','no renderer exceptions'],source_times_preserved:true,temporary_profile_removed_on_exit:true},null,2));
    console.log('PASS: offline visual replay at 1080/1400; no public/model request. artifacts/v5-ui/');
  } else {
  const catalog = await api('/api/data-sources');
  assert(catalog.auto_sync_paused && (await api('/api/daily-reports')).scheduler_paused, 'Automatic collection and reporting must both be paused');
  // Prevent per-report due-refresh from touching other public sources. Only one
  // explicit Sina quote sync is performed; each report fetches real daily data.
  for (const source of catalog.sources) {
    await api(`/api/data-sources/${source.id}/config`, { method: 'POST', body: { revision: source.config.revision, enabled: false, interval_seconds: source.config.interval_seconds, endpoint: source.config.endpoint } });
  }
  checks.push('temporary database and sandbox verified; all background collection and scheduling disabled');
  await navigate('每日分析');
  await until(`document.querySelector('.dr-status-grid')?.textContent.includes('服务已暂停调度')`);
  assert(await evaluate(`[...document.querySelectorAll('.daily-reports button')].filter(button=>button.textContent.includes('生成盘')).every(button=>button.disabled)`), 'Unconfigured paid model actions should be disabled');
  await clickIn('.daily-reports', '报告设置');
  await until(`document.querySelector('.dr-settings input[type="url"]')`);
  const urlInput = '.dr-settings input[type="url"]';
  const keyInput = '.dr-settings input[type="password"]';
  const modelInput = '.dr-settings input[placeholder="服务提供方的模型 ID"]';
  await setInput(urlInput, endpointA); await setInput(modelInput, modelName); await setInput(keyInput, testKey);
  await setInput('.dr-settings input[type="date"]', '2027-01-01'); await clickIn('.dr-settings', '添加');
  await navigate('市场总览'); await navigate('每日分析');
  assert(await evaluate(`document.querySelector(${JSON.stringify(keyInput)})?.value===${JSON.stringify(testKey)} && document.querySelector(${JSON.stringify(urlInput)})?.value===${JSON.stringify(endpointA)}`), 'Settings draft lost across navigation');
  assert(!await evaluate(`JSON.stringify(Object.entries(localStorage)).includes(${JSON.stringify(testKey)})`), 'Unsaved key leaked to localStorage');
  let settings = await saveSettings();
  assert(settings.key_configured && settings.configured && settings.skip_dates.includes('2027-01-01'), 'Settings did not persist');
  assert(!JSON.stringify(settings).includes(testKey) && !Object.hasOwn(settings, 'api_key'), 'GET settings exposed secret');
  checks.push('write-only credential, navigation draft, exclusion dates and successful settings save');
  await clickIn('.dr-settings', '测试已保存的模型连接');
  await until(`document.querySelector('.dr-notice[role="status"]')?.textContent.includes('模型连接与结构化引用校验通过')`);
  assert(modelRequests.at(-1).is_connection_test && modelRequests.at(-1).credential_sent, 'Model protocol test did not send stored credential');
  assert((await api('/api/daily-reports')).reports.length === 0 && (await api('/api/journals')).length === 0, 'Connection test created market report or journal');
  checks.push('explicit model test validates strict JSON/exact quotation protocol without creating reports');

  await setInput(urlInput, endpointB); settings = await saveSettings();
  assert(!settings.key_configured && settings.configured, 'Changing model origin reused old key or disabled keyless local model');
  await clickIn('.dr-settings', '测试已保存的模型连接');
  await until(`document.querySelector('.dr-notice[role="status"]')?.textContent.includes('模型连接与结构化引用校验通过')`);
  assert(modelRequests.at(-1).credential_absent, 'Old key was sent to the second origin');
  await setInput(urlInput, endpointA); await setInput(keyInput, testKey); settings = await saveSettings();
  checks.push('model origin change clears prior credential; local keyless service remains usable');

  await setInput(modelInput, 'unsaved-conflict-draft');
  settings = await api('/api/report-settings', { method: 'POST', body: settingsInput(settings, { model: modelName + '-newer' }) });
  await evaluate(`(() => {const b=[...document.querySelectorAll('.dr-settings button')].find(item=>item.textContent.trim()==='保存报告设置');if(b&&!b.disabled)b.click();})()`);
  await until(`document.querySelector('.dr-settings [role="alert"]')?.textContent.includes('本页草稿仍保留')`);
  assert(await evaluate(`document.querySelector(${JSON.stringify(modelInput)})?.value==='unsaved-conflict-draft'`), '409 discarded settings draft');
  assert((await savedSettings()).model === modelName + '-newer', 'Stale UI overwrote external settings update');
  await clickIn('.dr-settings', '放弃本页草稿，读取最新配置');
  await until(`!document.querySelector('.dr-settings-state')`);
  assert((await savedSettings()).key_configured, 'Same-origin blank-key settings update lost saved key');
  checks.push('optimistic-lock conflict preserves local edits and newer saved settings; same-origin key retention');
  for (const width of [1400, 1080]) { await viewport(width); await screenshot(`reports-settings-${width}-mock-model.png`, '.dr-settings'); }
  await evaluate(`document.querySelector('button[aria-label="收起报告设置"]').click()`);

  await navigate('数据与设置');
  await until(`document.querySelectorAll('.ds-card').length===9`);
  const sina = catalog.sources.find(source => source.id === 'sina');
  const selector = `.ds-card[aria-label=${JSON.stringify(`${sina.name}数据源`)}]`;
  const syncStart = new Date().toISOString();
  await clickIn(selector, '立即同步');
  await until(`document.querySelector(${JSON.stringify(selector + ' .ds-action-notice')})?.textContent.includes('同步完成')`, 45000);
  const synced = (await api('/api/data-sources')).sources.find(source => source.id === 'sina');
  live = await api('/api/market/live');
  assert(live.source_id === 'sina' && live.quotes.length > 0, 'Actual public quote synchronization failed; no synthetic fallback permitted');
  publicSync = { started_at: syncStart, finished_at: new Date().toISOString(), source: synced };
  await writeFile(path.join(artifacts, 'live-market-input.json'), JSON.stringify({ public_sync: publicSync, live }, null, 2));
  checks.push('one actual Sina public quote sync; report daily fetches remain real actual-contract data');
  const base = { region: '隔离验收区域', source: fixtureSource, source_url: 'https://example.invalid/isolated-ui-fixture', observed_at: new Date(Date.now() - 60000).toISOString(), is_demo: false, note: '仅用于临时数据库的流程验收；明确合成，不是实际市场数据。' };
  const today = `${researchDate}T00:00:00+08:00`;
  fixtures = [];
  for (const item of [
    { kind: 'purchase_price', entity: '合成未注明单位收购原值', value: 1.205, unit: '未注明' },
    { kind: 'truck_arrivals', entity: '合成晨间待卸车辆', value: 42, unit: '辆' },
    { kind: 'truck_arrivals', entity: '合成晨间待卸车辆', value: 55, unit: '辆', observed_at: new Date(Date.now() - 86460000).toISOString() },
    { kind: 'weather', entity: '合成日降水预报', value: 18, unit: 'mm', observed_at: today },
    { kind: 'weather', entity: '合成最高温度预报', value: 12, unit: '°C', observed_at: today },
    { kind: 'import_auction_rate', entity: '合成进口存量拍卖成交率', value: 31, unit: '%' },
    { kind: 'related_price', entity: '合成生猪关联价格', value: 16.2, unit: '元/公斤' },
  ]) fixtures.push(await api('/api/observations', { method: 'POST', body: { ...base, ...item } }));
  await writeFile(path.join(artifacts, 'synthetic-industry-input.json'), JSON.stringify({ notice: fixtureSource, observations: fixtures }, null, 2));

  await navigate('复盘日记'); await until(`document.querySelector('.journal-title-input')`);
  await clickIn('.journal-intro', '新建复盘');
  const originalDraft = '隔离验收：日报跳转前的个人未保存草稿';
  await setInput('.journal-title-input', originalDraft);
  await setInput('.journal-section-field textarea', '个人原始计划应在打开报告日记后完整保留。');
  await navigate('每日分析');
  const modelCountBeforeRules = modelRequests.length;
  const rules = await generate('pre', 'rules'); await verifyFixtures(rules);
  assert(modelRequests.length === modelCountBeforeRules && !rules.journal_id, 'Rules preview called a model or auto-saved a journal');
  assert(await evaluate(`document.querySelector('.dr-report-heading')?.textContent.includes('未调用大模型')`), 'Rules preview lacks clear non-AI label');
  await clickIn('.dr-report', '保存到复盘');
  await until(`document.querySelector('.dr-report-actions')?.textContent.includes('查看已保存复盘')`);
  const savedRules = await api(`/api/daily-reports/${rules.id}`);
  assert(savedRules.journal_id, 'Manual rules report save did not link a journal');
  checks.push('rules preview uses complete auditable bundle without model call or automatic journal; manual save works');

  const pre = await generate('pre', 'llm'); await verifyFixtures(pre);
  assert(pre.journal_id, 'Pre-market AI report did not auto-save journal');
  const preJournal = await api(`/api/journals/${pre.journal_id}`);
  await clickIn('.dr-report', '查看已保存复盘');
  await until(`document.querySelector('.journal-title-input')?.value===${JSON.stringify(preJournal.title)}`);
  assert(await evaluate(`document.querySelector('.journal-editor-footer code')?.title===${JSON.stringify(pre.journal_id)}`), 'Report opened a different journal');
  assert(await evaluate(`document.querySelector('.journal-local-drafts')?.textContent.includes(${JSON.stringify(originalDraft)})`), 'Opening saved report discarded unrelated original draft');
  const editedTitle = '个人已修订：' + preJournal.title;
  await setInput('.journal-title-input', editedTitle); await clickIn('.journal-editor-actions', '保存复盘');
  await until(`document.querySelector('.journal-save-state')?.textContent.includes('已保存') && !document.querySelector('.journal-save-state')?.textContent.includes('草稿')`);
  const repeat = await api(`/api/daily-reports/${pre.id}/journal`, { method: 'POST', body: {} });
  assert(repeat.already_saved && repeat.journal_id === pre.journal_id && (await api(`/api/journals/${pre.journal_id}`)).title === editedTitle, 'Repeat save duplicated or overwrote user-edited journal');
  await clickIn('.journal-local-drafts', originalDraft + '尚未入库 · c2701');
  await until(`document.querySelector('.journal-title-input')?.value===${JSON.stringify(originalDraft)}`);
  assert(await evaluate(`document.querySelector('.journal-section-field textarea')?.value==='个人原始计划应在打开报告日记后完整保留。'`), 'Original draft text was changed');
  for (const width of [1400, 1080]) { await viewport(width); await screenshot(`retained-journal-draft-${width}.png`, '.journal-editor'); }
  checks.push('AI pre report auto-journal opens exact entry; original draft retained; re-save never overwrites user revisions');

  await navigate('每日分析');
  const post = await generate('post', 'llm'); await verifyFixtures(post);
  assert(post.journal_id, 'Post-market AI report did not auto-save journal');
  const citation = await evaluate(`(() => {const b=document.querySelector('.dr-report .dr-citation');if(!b)return false;b.click();return true;})()`);
  assert(citation, 'Report has no citation control');
  await until(`document.querySelector('.dr-evidence')?.open && document.querySelector('.dr-evidence-item.focused')`);
  assert(await evaluate(`document.querySelector('.dr-evidence-item.focused')?.textContent.includes('本系统首次可用')`), 'Evidence target lost first-available time');
  for (const width of [1400, 1080]) {
    await viewport(width);
    await screenshot(`evidence-${width}-mock-model.png`, '.dr-evidence-item.focused');
    await screenshot(`report-${width}-mock-model.png`, '.dr-report-heading');
    await screenshot(`daily-workspace-${width}-mock-model.png`, '.daily-reports');
  }
  await clickIn('.dr-report', '导出 JSON 与依据');
  let exported;
  for (let i = 0; i < 60; i++) { exported = (await readdir(downloads)).find(file => file.endsWith('.json')); if (exported) break; await delay(150); }
  assert(exported, 'JSON download missing');
  const exportData = JSON.parse(await readFile(path.join(downloads, exported), 'utf8'));
  assert(exportData.id === post.id && exportData.bundle.input_sha256 === post.bundle.input_sha256 && exportData.output.sections.length === post.output.sections.length, 'UI JSON export lost report or evidence');
  await writeFile(path.join(artifacts, 'ui-export-mock-model.json'), JSON.stringify(exportData, null, 2));
  checks.push('AI post report, exact original citations, evidence timing, full JSON export and 1080/1400 no-overflow screenshots');
  assert(!await evaluate(`document.body.innerText.includes(${JSON.stringify(testKey)}) || JSON.stringify(Object.entries(localStorage)).includes(${JSON.stringify(testKey)})`), 'Key leaked to UI text or browser storage');
  assert(rendererErrors.length === 0, `Renderer exceptions: ${rendererErrors.join('; ')}`);
  const finalList = await api('/api/daily-reports');
  assert(finalList.reports.length === 3 && finalList.jobs.every(job => job.trigger === 'manual') && (await api('/api/journals')).length === 3, 'Report/journal counts or disabled scheduler incorrect');
  await writeFile(path.join(artifacts, 'report.json'), JSON.stringify({ passed: true, created_at: new Date().toISOString(), binary: binary || 'development Electron', methodology: '真实新浪公开行情与实际合约日线 + 临时合成产业样例 + 本机模拟模型。未验证真实模型质量、付费账户或真实无人值守调度。', checks, public_sync: publicSync, model_requests: modelRequests, report_ids: reports.map(report => report.id), jobs: finalList.jobs, renderer_errors: rendererErrors, screenshots: (await readdir(artifacts)).filter(file => file.endsWith('.png')), automatic_collection: false, automatic_reporting: false, temporary_profile_removed_on_exit: true }, null, 2));
  await rm(path.join(artifacts, 'unexpected-failure.png'), { force: true });
  console.log(`PASS: ${checks.length} report UI checks. Real public market input + local mock model; artifacts/v5-ui/`);
  }
} catch (error) {
  try { await screenshot('unexpected-failure.png'); } catch {}
  await writeFile(path.join(artifacts, 'failure.json'), JSON.stringify({ passed: false, error: clean(error.stack || error), checks, model_requests: modelRequests, public_sync: publicSync, renderer_errors: rendererErrors, at: new Date().toISOString() }, null, 2)).catch(() => {});
  throw new Error(clean(error.stack || error));
} finally {
  if (child.exitCode === null && socket?.readyState === WebSocket.OPEN) { try { await command('Browser.close'); } catch {} await delay(300); }
  socket?.close(); for (const task of pending.values()) clearTimeout(task.timer);
  try { if (process.platform === 'win32') { if (child.exitCode === null && child.pid) spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', timeout: 10000 }); } else if (child.pid) process.kill(-child.pid, 'SIGKILL'); } catch {}
  for (const server of [modelA, modelB]) if (server.listening) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
  await delay(250); await rm(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
}
