// v0.6 UI integration: actual sandboxed Electron + temporary profile + local
// HTTP fixtures. Every quote and industry observation below is SYNTHETIC.
// Default mode uses no production profile, provider account, external source, or live order.
// Build first: npm run build. Run: node scripts/data-ui-smoke.mjs [packaged-binary]
// Optional public-network probe: node scripts/data-ui-smoke.mjs --live-public
// That mode uses a separate temporary profile, no fixtures, and seven account-free
// public sources. A source outage is reported as partial, never replaced by mock data.
import { spawn, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdtemp, mkdir, writeFile, stat, rm } from 'node:fs/promises';
import http from 'node:http';
import net from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { seedOfflineChartHistory } from './chart-test-fixtures.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const livePublic = process.argv.includes('--live-public');
const packagedBinary = process.argv.slice(2).find(argument => !argument.startsWith('--'));
const profile = await mkdtemp(path.join(tmpdir(), 'cornwatch-data-ui-'));
const userData = path.join(profile, 'cornwatch');
const artifacts = path.join(root, 'artifacts', 'v6-data-ui');
const fixtureToken = randomBytes(24).toString('hex');
const clean = value => String(value).split(fixtureToken).join('[isolated test credential]');
const checks = [];
const fixtureCounts = { quotes: 0, industry: 0, rejected: 0 };
let marketFailure = false;
let quoteVersion = 0;
const baseTime = Date.now() - 60_000;
const tradeDate = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(baseTime));
const contractYear = String(new Date(baseTime).getUTCFullYear() + 1).slice(-2);
const symbol = `c${contractYear}01`;
const syntheticEntity = '隔离验收·合成玉米收购监测';
const syntheticTruckEntity = '隔离验收·合成企业到车';
const initialPrice = 2367;
if (!livePublic) await seedOfflineChartHistory(root, userData, ['c2701', symbol]);

function fixtureQuotes() {
  return [
    [symbol, 'c', '隔离验收合成玉米', initialPrice + quoteVersion * 2, 180000, 260000],
    [`c${contractYear}05`, 'c', '隔离验收合成玉米远月', 2391, 90000, 150000],
    [`cs${contractYear}01`, 'cs', '隔离验收合成淀粉', 2786, 60000, 100000],
    [`lh${contractYear}01`, 'lh', '隔离验收合成生猪', 16430, 40000, 90000],
    [`m${contractYear}01`, 'm', '隔离验收合成豆粕', 3128, 140000, 200000],
  ].map(([contract, product, name, last, volume, oi]) => ({
    symbol: contract, product, name, exchange: 'DCE', trade_date: tradeDate,
    last, open: last - 5, high: last + 10, low: last - 12,
    previous_settlement: last - 7, volume, open_interest: oi,
    observed_at: new Date(baseTime + quoteVersion * 1000).toISOString(),
  }));
}
function fixtureObservations(origin) {
  const source = '隔离UI验收合成数据（不是官方或企业真实报价）';
  const base = { region: '隔离测试区域', source, source_url: `${origin}/industry`, is_demo: false };
  return [
    { ...base, kind: 'purchase_price', entity: syntheticEntity, value: 2238, unit: '元/吨', observed_at: new Date(baseTime - 7 * 86400000).toISOString(), note: '合成周度监测口径，水分14%，三等，非实际市场价格；仅写入临时测试库。' },
    { ...base, kind: 'purchase_price', entity: syntheticEntity, value: 2256, unit: '元/吨', observed_at: new Date(baseTime - 3600000).toISOString(), note: '合成周度监测口径，水分14%，三等，非实际市场价格；仅写入临时测试库。' },
    { ...base, kind: 'truck_arrivals', entity: syntheticTruckEntity, value: 37, unit: '辆', observed_at: new Date(baseTime).toISOString(), note: '隔离合成验收：单一测试厂，当日07:00至08:00到车，非全国统计；退出后清理。' },
    { ...base, kind: 'related_price', entity: '隔离验收·合成生猪监测', value: 16.7, unit: '元/公斤', observed_at: new Date(baseTime).toISOString(), note: '仅验证关联品种、单位、来源和时间字段，不是真实监测数据。' },
  ];
}

const fixtureServer = http.createServer((request, response) => {
  const send = (status, body) => { response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); response.end(JSON.stringify(body)); };
  if (request.headers.authorization !== `Bearer ${fixtureToken}`) { fixtureCounts.rejected++; send(401, { error: 'isolated fixture requires its test bearer credential' }); return; }
  if (request.url === '/quotes') {
    fixtureCounts.quotes++;
    if (marketFailure) { send(503, { error: `synthetic unavailable response; do not render secret ${fixtureToken}` }); return; }
    send(200, { generated_at: new Date().toISOString(), quotes: fixtureQuotes() });
  } else if (request.url === '/industry') {
    fixtureCounts.industry++;
    send(200, { generated_at: new Date().toISOString(), observations: fixtureObservations(fixtureOrigin) });
  } else send(404, { error: 'unknown isolated fixture route' });
});
if (!livePublic) await new Promise(resolve => fixtureServer.listen(0, '127.0.0.1', resolve));
const fixtureOrigin = livePublic ? '' : `http://127.0.0.1:${fixtureServer.address().port}`;
const listener = net.createServer();
await new Promise(resolve => listener.listen(0, '127.0.0.1', resolve));
const port = listener.address().port;
await new Promise(resolve => listener.close(resolve));

const env = { ...process.env, XDG_CONFIG_HOME: profile };
for (const key of Object.keys(env)) if (key.startsWith('LLM_') || key.startsWith('CORNWATCH_') || key === 'ELECTRON_RUN_AS_NODE') delete env[key];
env.CORNWATCH_AUTO_SYNC = 'false';
const electronBinary = process.platform === 'darwin' ? 'node_modules/electron/dist/Electron.app/Contents/MacOS/Electron' : process.platform === 'win32' ? 'node_modules/electron/dist/electron.exe' : 'node_modules/electron/dist/electron';
const child = spawn(packagedBinary ? path.resolve(packagedBinary) : path.join(root, electronBinary), [...(packagedBinary ? [] : ['.']), `--remote-debugging-port=${port}`, `--user-data-dir=${userData}`], { cwd: root, env, detached: process.platform !== 'win32', stdio: ['ignore', 'pipe', 'pipe'] });
let logs = '';
child.stdout.on('data', chunk => { logs = (logs + chunk).slice(-12000); });
child.stderr.on('data', chunk => { logs = (logs + chunk).slice(-12000); });
let spawnError;
child.once('error', error => { spawnError = error; });
let socket;
let sequence = 0;
const pending = new Map();
const rendererErrors = [];

function command(method, params = {}) {
  if (socket?.readyState !== WebSocket.OPEN) return Promise.reject(new Error('CDP is disconnected'));
  return new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 20000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
}
async function evaluate(expression) {
  const result = await command('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(clean(JSON.stringify(result.exceptionDetails)));
  return result.result?.value;
}
async function until(expression, timeout = 20000) {
  const start = Date.now();
  while (Date.now() - start < timeout) { if (await evaluate(`Boolean(${expression})`)) return; await delay(150); }
  throw new Error(clean(`UI condition timed out: ${expression}`));
}
function assert(value, message) { if (!value) throw new Error(message); }
async function navigate(label) {
  const ok = await evaluate(`(() => { const button=[...document.querySelectorAll('.sidebar nav button')].find(item=>item.textContent.trim()===${JSON.stringify(label)}); if(!button||button.disabled)return false;button.click();return true;})()`);
  assert(ok, `Missing navigation: ${label}`);
}
async function clickIn(selector, label) {
  const ok = await evaluate(`(() => { const scope=document.querySelector(${JSON.stringify(selector)});const button=scope&&[...scope.querySelectorAll('button')].find(item=>item.textContent.trim()===${JSON.stringify(label)});if(!button||button.disabled)return false;button.click();return true;})()`);
  assert(ok, `Missing or disabled button: ${label} in ${selector}`);
}
async function setInput(selector, value) {
  const ok = await evaluate(`(() => {const input=document.querySelector(${JSON.stringify(selector)});if(!input||input.disabled)return false;Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(value)});input.dispatchEvent(new Event('input',{bubbles:true}));return true;})()`);
  assert(ok, `Input is unavailable: ${selector}`);
}
async function api(pathname, options) { return await evaluate(`window.cornwatch.request(${JSON.stringify(pathname)}${options ? `,${JSON.stringify(options)}` : ''})`); }
async function screenshot(filename, selector) {
  if (selector) await evaluate(`(() => {const element=document.querySelector(${JSON.stringify(selector)});if(element)window.scrollTo({top:Math.max(0,window.scrollY+element.getBoundingClientRect().top-90),behavior:'instant'});})()`);
  await delay(250);
  const result = await command('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(path.join(artifacts, filename), Buffer.from(result.data, 'base64'));
}
async function viewport(width) {
  await command('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
  await delay(250);
  assert(!await evaluate('document.documentElement.scrollWidth > innerWidth + 2'), `Page overflows ${width}px viewport`);
}
async function source(id) { return (await api('/api/data-sources')).sources.find(item => item.id === id); }
function card(sourceName) { return `.ds-card[aria-label=${JSON.stringify(`${sourceName}数据源`)}]`; }
async function openConfig(selector) {
  await evaluate(`(() => {const card=document.querySelector(${JSON.stringify(selector)});if(card&&!card.querySelector('.ds-config-form'))card.querySelector('.ds-card-header button').click();})()`);
  await until(`document.querySelector(${JSON.stringify(`${selector} .ds-config-form`)}) !== null`);
}
async function refreshStatus() {
  await until(`document.querySelector('.ds-console-heading .secondary-button') && !document.querySelector('.ds-console-heading .secondary-button').disabled`);
  await clickIn('.data-sources', '刷新状态');
  await until(`!document.querySelector('.ds-console-heading .secondary-button')?.disabled`);
}
async function syncCard(selector) {
  await clickIn(selector, '立即同步');
  await until(`document.querySelector(${JSON.stringify(`${selector} .ds-action-notice`)})?.textContent.includes('同步完成') && ![...document.querySelector(${JSON.stringify(selector)}).querySelectorAll('button')].find(button=>button.textContent.trim()==='立即同步')?.disabled`);
}

function checkCurrentReport(report, live) {
  assert(report && /^c\d{4}$/.test(report.symbol), 'Current analysis did not select an actual corn contract');
  const quote = live.quotes.find(item => item.symbol === report.symbol);
  assert(quote && JSON.stringify(report.snapshot) === JSON.stringify(quote), 'Analysis changed or fabricated the source quote');
  const technical = report.technical, points = technical?.points || [], last = points.at(-1), previous = points.at(-2);
  assert(technical.is_demo === false && points.length >= 60 && technical.bar_count === points.length, 'Current analysis contains demo, insufficient, or mismatched daily bars');
  assert(technical.symbol === report.symbol && last.close === technical.latest.close && Date.parse(last.timestamp) === Date.parse(technical.as_of), 'Technical values do not align with the final actual-contract daily bar');
  assert(new URL(report.daily_url).hostname === 'stock2.finance.sina.com.cn' && report.daily_source.includes('新浪'), 'Daily evidence is not the expected public source');
  assert(/^[a-f0-9]{64}$/.test(report.daily_sha256) && /^[a-f0-9]{64}$/.test(report.input_sha256), 'Missing auditable evidence fingerprints');
  const generatedAt = Date.parse(report.generated_at), availableAt = Date.parse(report.daily_available_at);
  assert(availableAt <= generatedAt && Date.parse(last.timestamp) <= generatedAt, 'Current analysis used unavailable daily evidence');
  const shanghaiNow = new Date(generatedAt + 8 * 3600000), today = shanghaiNow.toISOString().slice(0, 10);
  assert(last.trade_date < today || (last.trade_date === today && shanghaiNow.getUTCHours() * 60 + shanghaiNow.getUTCMinutes() >= 15 * 60 + 15), 'An incomplete current daily bar entered the analysis');
  if (today === '2026-10-28') assert(report.symbol === 'c2701' && last.trade_date === '2026-10-28', 'Expected current C2701 and the completed 2026-10-28 daily session');
  for (let index = 0; index < points.length; index++) {
    const p = points[index];
    assert([p.open, p.high, p.low, p.close].every(value => Number.isFinite(value) && value > 0) && p.high >= Math.max(p.open, p.close) && p.low <= Math.min(p.open, p.close), 'Malformed daily OHLC');
    assert(!index || p.trade_date > points[index - 1].trade_date, 'Daily evidence is duplicated or out of order');
  }
  const near = (actual, expected, name) => assert(Number.isFinite(actual) && Math.abs(actual - expected) < 1e-7, `Current analysis ${name} disagrees with independently recomputed daily inputs`);
  const mean = values => values.reduce((total, value) => total + value, 0) / values.length;
  for (const size of [5, 20, 60]) near(last[`ma${size}`], mean(points.slice(-size).map(p => p.close)), `MA${size}`);
  const ranges = points.map((p, index) => index ? Math.max(p.high - p.low, Math.abs(p.high - points[index - 1].close), Math.abs(p.low - points[index - 1].close)) : p.high - p.low);
  let atr = mean(ranges.slice(0, 14));
  for (const value of ranges.slice(14)) atr = (atr * 13 + value) / 14;
  const differences = points.slice(1).map((p, index) => p.close - points[index].close);
  let gain = mean(differences.slice(0, 14).map(value => Math.max(value, 0))), loss = mean(differences.slice(0, 14).map(value => Math.max(-value, 0)));
  for (const value of differences.slice(14)) { gain = (gain * 13 + Math.max(value, 0)) / 14; loss = (loss * 13 + Math.max(-value, 0)) / 14; }
  near(technical.latest.atr14, atr, 'ATR14');
  near(technical.latest.rsi14, gain + loss ? 100 * gain / (gain + loss) : 50, 'RSI14');
  near(technical.latest.change_pct, (last.close / previous.close - 1) * 100, 'previous-close change');
  near(report.snapshot.change_pct, (quote.last / quote.previous_settlement - 1) * 100, 'previous-settlement quote change');
  if (technical.latest.oi_change != null) near(technical.latest.oi_change, last.open_interest - previous.open_interest, 'completed-day open interest change');
  for (const spread of report.spreads) {
    const left = live.quotes.find(item => item.symbol === spread.left), right = live.quotes.find(item => item.symbol === spread.right);
    assert(left && right && left.source === right.source && left.trade_date === right.trade_date && Math.abs(Date.parse(left.observed_at) - Date.parse(right.observed_at)) <= 300000, 'Spread used incompatible quotes');
    near(spread.value, left.last - right.last, 'same-time spread');
  }
  assert(report.evidence.every(item => !item.is_demo && Date.parse(item.available_at) <= generatedAt && Date.parse(item.observed_at) <= generatedAt), 'Unavailable or demo industry evidence entered the report');
  return { symbol: report.symbol, generated_at: report.generated_at, snapshot_observed_at: report.snapshot.observed_at, daily_as_of: technical.as_of, daily_available_at: report.daily_available_at, daily_bars: points.length, close: last.close, ma5: last.ma5, ma20: last.ma20, ma60: last.ma60, atr14: atr, rsi14: technical.latest.rsi14, input_sha256: report.input_sha256, daily_sha256: report.daily_sha256, independently_recomputed: ['MA5', 'MA20', 'MA60', 'ATR14', 'RSI14', 'daily close change', 'quote settlement change', 'same-time spreads'] };
}

async function captureCurrentAnalysis(live) {
  await until(`document.querySelector('.current-analysis') !== null`);
  assert((await api('/api/research/current')).report === null, 'Fresh temporary profile unexpectedly has an analysis');
  console.log('LIVE: generating actual-contract current analysis through the Electron UI…');
  await clickIn('.current-analysis', '生成当前分析');
  await until(`document.querySelector('.ca-headline') || document.querySelector('.current-analysis .market-error')`, 45000);
  const envelope = await api('/api/research/current');
  if (!envelope.report) {
    const message = await evaluate(`document.querySelector('.current-analysis .market-error')?.textContent || 'No completed report'`);
    await screenshot('live-analysis-unavailable-1400.png', '.current-analysis');
    return { ok: false, error: message };
  }
  const summary = checkCurrentReport(envelope.report, live);
  await writeFile(path.join(artifacts, 'live-current-analysis.json'), JSON.stringify(envelope.report, null, 2));
  for (const width of [1400, 1080]) {
    await viewport(width);
    await screenshot(`live-analysis-${width}.png`, '.current-analysis');
    await evaluate(`(() => {document.querySelector('.ca-chart').open=true;document.querySelector('.ca-evidence').open=true;})()`);
    await screenshot(`live-analysis-chart-${width}.png`, '.ca-chart');
    await screenshot(`live-analysis-evidence-${width}.png`, '.ca-evidence');
    await evaluate(`(() => {document.querySelector('.ca-chart').open=false;document.querySelector('.ca-evidence').open=false;})()`);
  }
  assert(await evaluate(`document.querySelector('.current-analysis')?.textContent.includes('较昨收') && document.querySelector('.current-analysis')?.textContent.includes('较昨结')`), 'UI hid the different daily and snapshot change bases');
  await clickIn('.current-analysis', '带入复盘');
  const expectedTitle = `${envelope.report.symbol.toUpperCase()} 盘面证据复盘`;
  await until(`document.querySelector('.journal-title-input')?.value===${JSON.stringify(expectedTitle)} && document.querySelector('.journal-editor')?.getClientRects().length>0`);
  const journalDraft = await evaluate(`(() => {const fields=[...document.querySelectorAll('.journal-section-field textarea')];return {title:document.querySelector('.journal-title-input')?.value,plan:fields[0]?.value,thesis:fields[1]?.value,save_state:document.querySelector('.journal-save-state')?.textContent,persisted:window.cornwatch.readJournalDrafts?.()};})()`);
  assert(journalDraft.thesis.includes(envelope.report.input_sha256) && journalDraft.thesis.includes(envelope.report.headline), 'Journal seed lost the source fingerprint or current thesis');
  assert(envelope.report.scenarios.every(item => journalDraft.plan.includes(item.condition)), 'Journal seed lost observation scenarios');
  await until(`document.querySelector('.journal-save-state')?.textContent.includes('本地草稿已保留')`);
  const persistedDraft = await evaluate('window.cornwatch.readJournalDrafts?.()');
  assert(persistedDraft?.includes(envelope.report.input_sha256), 'Analysis journal draft was not durably retained in the isolated profile');
  assert((await api('/api/journals')).length === 0, 'Bringing into review unexpectedly saved or published a journal');
  for (const width of [1400, 1080]) { await viewport(width); await screenshot(`live-analysis-journal-${width}.png`, '.journal-editor'); }
  await navigate('市场总览');
  await navigate('复盘日记');
  assert(await evaluate(`document.querySelector('.journal-title-input')?.value===${JSON.stringify(expectedTitle)}`), 'Navigating away lost the seeded review draft');
  await navigate('市场总览');
  return { ok: true, ...summary, journal_seed_verified: true, journal_draft_retained: true, journal_saved: false };
}

async function captureLivePublic(catalog) {
  const outcomes = [];
  const publicIds = ['sina', 'boyar_prices', 'boyar_arrivals', 'import_auction', 'liaoning', 'moa', 'weather'];
  await navigate('数据与设置');
  await until(`document.querySelectorAll('.ds-card').length===9`);
  for (const id of publicIds) {
    const definition = catalog.sources.find(item => item.id === id);
    const selector = card(definition.name);
    assert(definition.account_required === false, 'Live public probe must not request an account-based source');
    console.log(`LIVE: manually synchronizing ${id} (${definition.name})…`);
    await clickIn(selector, '立即同步');
    await until(`document.querySelector(${JSON.stringify(`${selector} .ds-action-notice`)}) && ![...document.querySelector(${JSON.stringify(selector)}).querySelectorAll('button')].find(button=>button.textContent.trim()==='立即同步')?.disabled`, 120000);
    const state = await source(id);
    const ok = !!state.last_success && !state.last_error;
    outcomes.push({ id, name: state.name, ok, status: state.status, freshness: state.freshness, freshness_note: state.freshness_note, record_count: state.total_records, added: state.added, duplicates: state.duplicates, latest_data_at: state.latest_data_at, last_attempt: state.last_attempt, last_success: state.last_success, no_publication: state.freshness_note?.includes('本次公开栏目未发现') || false, error: state.last_error || '', coverage: state.coverage });
    console.log(`LIVE ${ok ? 'OK' : 'UNAVAILABLE'}: ${id}; records=${state.total_records}; source_time=${state.latest_data_at || 'unknown'}${state.last_error ? `; ${clean(state.last_error)}` : ''}`);
  }
  const live = await api('/api/market/live');
  const observations = (await api('/api/observations')).filter(item => !item.is_demo);
  const events = (await api('/api/events')).filter(item => !item.is_demo);
  assert(fixtureCounts.quotes === 0 && fixtureCounts.industry === 0 && fixtureCounts.rejected === 0, 'Live mode accidentally used a synthetic fixture');
  assert(!observations.some(item => item.source.includes('隔离UI验收') || item.entity.includes('隔离验收')), 'Live mode contains a synthetic observation');
  const bridgeState = await source('market_bridge');
  assert(!bridgeState.config.endpoint && !bridgeState.config.token_configured, 'Live mode must not configure a bridge or credentials');
  const weatherRegions = [...new Set(observations.filter(item => item.kind === 'weather').map(item => item.region))];
  const publicEvidence = {};
  for (const [id, kind, sourceText] of [['boyar_prices', 'purchase_price', '博亚和讯'], ['boyar_arrivals', 'truck_arrivals', '博亚和讯'], ['import_auction', 'import_auction_rate', '中储粮']]) {
    const rows = observations.filter(item => item.kind === kind && item.source.includes(sourceText));
    const category = id === 'import_auction' ? 'import_auction' : kind;
    const records = events.filter(item => item.category === category && item.source.includes(sourceText));
    const state = outcomes.find(item => item.id === id);
    if (state.ok && !state.no_publication) assert(rows.length > 0 && records.length > 0, `${id} synchronized without any semantic observations and events`);
    assert(rows.every(item => item.id && item.entity && item.note && item.source_url?.startsWith('https://') && Number.isFinite(item.value) && Date.parse(item.available_at) <= Date.now() && Date.parse(item.observed_at) <= Date.now()), `${id} lost source/observation/availability semantics`);
    if (kind === 'truck_arrivals') assert(rows.every(item => item.unit === '辆' && Number.isInteger(item.value) && item.value >= 0), 'Morning arrivals lost their vehicle-count unit');
    if (kind === 'import_auction_rate') assert(rows.every(item => item.unit === '%' && item.value >= 0 && item.value <= 100 && item.note.includes('进口')), 'Import auction rates were confused with tonnage or newly signed import contracts');
    if (kind === 'purchase_price') assert(rows.every(item => item.unit && item.value > 0), 'Enterprise prices lack explicit raw-unit semantics');
    publicEvidence[id] = { observations: rows.length, events: records.length, units: [...new Set(rows.map(item => item.unit))], observed_at: [...new Set(rows.map(item => item.observed_at))], source_urls: [...new Set([...rows, ...records].map(item => item.source_url))], availability_from: rows.map(item => item.available_at).sort()[0], availability_to: rows.map(item => item.available_at).sort().at(-1) };
  }
  await writeFile(path.join(artifacts, 'live-evidence.json'), JSON.stringify({ mode: 'live-public', synthetic_fixtures: false, captured_at: new Date().toISOString(), sources: outcomes, market: live, observations, events }, null, 2));
  await navigate('市场总览');
  await until(`document.querySelector('.industry-board') !== null`);
  if (outcomes.find(item => item.id === 'sina')?.ok) {
    await until(`document.querySelector('.market-monitor')?.textContent.includes('新浪') && ![...document.querySelectorAll('.quote-card .badge.demo')].some(element=>element.getClientRects().length>0)`);
    assert(live.source_id === 'sina' && live.quotes.length > 0, 'Successful public sync did not become the visible quote source');
    assert(!await evaluate(`[...document.querySelectorAll('.overview-grid, .chart-panel')].some(element=>element.getClientRects().length>0)`), 'Public quotes are still mixed with the old demo trend');
  }
  if (outcomes.find(item => item.id === 'weather')?.ok) {
    await until(`document.querySelectorAll('.industry-weather-grid > article').length===5`);
    assert(weatherRegions.length === 5, 'Weather collection should preserve all five configured regions');
    assert(await evaluate(`document.querySelectorAll('.weather-temperature').length===15`), 'Three-day min/max temperatures are missing from the five-region weather panel');
  }
  if (outcomes.find(item => item.id === 'boyar_prices')?.ok) {
    await until(`document.querySelector('.industry-price-table') !== null`);
    assert(await evaluate(`document.querySelector('.industry-price-table')?.textContent.includes('博亚')`), 'Enterprise quote details did not identify the public source');
  }
  if (outcomes.find(item => item.id === 'boyar_arrivals')?.ok) await until(`document.querySelector('.industry-arrivals')?.textContent.includes('博亚')`);
  if (outcomes.find(item => item.id === 'import_auction')?.ok) {
    await until(`document.querySelector('.industry-imports') !== null`);
    const importText = await evaluate(`document.querySelector('.industry-imports')?.textContent || ''`);
    assert(importText.includes('中储粮') && importText.includes('成交率') && importText.includes('%'), 'Import board omitted actual auction-rate semantics');
  }
  for (const width of [1400, 1080]) {
    await viewport(width);
    // Skip a whole-page overview when the public quote source is unavailable;
    // the initial application demo must not be presented as live evidence.
    if (live.quotes.length) await screenshot(`live-overview-${width}.png`, '.page-heading');
    await screenshot(`live-industry-${width}.png`, '.industry-board');
    await screenshot(`live-weather-${width}.png`, '.industry-weather');
    await screenshot(`live-imports-${width}.png`, '.industry-imports');
    if (outcomes.find(item => item.id === 'boyar_prices')?.ok) {
      await evaluate(`document.querySelector('.industry-price-table').open=true`);
      await screenshot(`live-enterprise-prices-${width}.png`, '.industry-price-table');
      await evaluate(`document.querySelector('.industry-price-table').open=false`);
    }
  }
  const analysis = live.quotes.length ? await captureCurrentAnalysis(live) : { ok: false, error: 'Public quotes unavailable; analysis skipped without substitutes' };
  await navigate('数据与设置');
  await refreshStatus();
  for (const width of [1400, 1080]) {
    await viewport(width);
    await screenshot(`live-sources-${width}.png`, '.data-sources');
    await screenshot(`live-official-${width}.png`, card(catalog.sources.find(item => item.id === 'liaoning').name));
    await screenshot(`live-weather-status-${width}.png`, card(catalog.sources.find(item => item.id === 'weather').name));
    await screenshot(`live-enterprise-status-${width}.png`, card(catalog.sources.find(item => item.id === 'boyar_prices').name));
    await screenshot(`live-import-status-${width}.png`, card(catalog.sources.find(item => item.id === 'import_auction').name));
  }
  assert(rendererErrors.length === 0, `Renderer exceptions: ${rendererErrors.join('; ')}`);
  const passed = outcomes.every(item => item.ok) && analysis.ok;
  await writeFile(path.join(artifacts, 'live-report.json'), JSON.stringify({ passed, mode: 'live-public', fixtures: 'none; actual account-free public responses only; disposable database', sources: outcomes, public_evidence: publicEvidence, current_analysis: analysis, real_quote_symbols: live.quotes.map(item => item.symbol), real_industry_sources: [...new Set(observations.filter(item => item.kind !== 'weather').map(item => item.source))], weather_regions: weatherRegions, viewport_widths: [1080, 1400], created_at: new Date().toISOString() }, null, 2));
  await rm(path.join(artifacts, 'live-unexpected-failure.png'), { force: true });
  console.log(`LIVE ${passed ? 'PASS' : 'PARTIAL'}: ${outcomes.filter(item => item.ok).length}/${publicIds.length} actual public sources; current analysis ${analysis.ok ? 'PASS' : 'UNAVAILABLE'}. No mock or provider credentials; evidence: artifacts/v6-data-ui/live-*.png and live-report.json`);
  if (!passed) process.exitCode = 2;
}

try {
  await mkdir(artifacts, { recursive: true });
  let target;
  for (let index = 0; index < 150; index++) {
    if (spawnError) throw spawnError;
    if (child.exitCode !== null) throw new Error(clean(`Electron exited ${child.exitCode}: ${logs}`));
    try { target = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find(item => item.type === 'page' && item.url.startsWith('http://127.0.0.1:')); } catch {}
    if (target) break;
    await delay(200);
  }
  assert(target, clean(`Electron did not expose a page. ${logs}`));
  socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
  socket.addEventListener('message', event => {
    const response = JSON.parse(event.data);
    if (response.id) { const task = pending.get(response.id); if (task) { clearTimeout(task.timer); pending.delete(response.id); response.error ? task.reject(new Error(clean(JSON.stringify(response.error)))) : task.resolve(response.result); } }
    if (response.method === 'Runtime.exceptionThrown') rendererErrors.push(clean(response.params.exceptionDetails.text));
  });
  socket.addEventListener('close', () => { for (const task of pending.values()) { clearTimeout(task.timer); task.reject(new Error('CDP disconnected')); } pending.clear(); });
  await command('Runtime.enable');
  await until(`document.querySelector('.sidebar nav') && window.cornwatch`);
  assert(!await evaluate(`typeof window.require!=='undefined'||typeof process!=='undefined'`), 'Renderer Node isolation is missing');
  const settings = await api('/api/settings');
  assert(settings.live_trading === false, 'UI test must not run with live trading enabled');
  const isolatedDatabase = await stat(path.join(userData, 'cornwatch.db')).catch(() => null);
  assert(isolatedDatabase?.isFile(), 'Refusing to mutate data without a database in the disposable profile');
  const catalog = await api('/api/data-sources');
  assert(catalog.auto_sync_paused === true, 'Automatic collectors must be paused in isolated UI tests');
  assert(catalog.sources.length === 9, 'Expected all nine source definitions');
  checks.push('temporary profile verified; automatic external collectors paused');

  if (livePublic) {
    await captureLivePublic(catalog);
  } else {
  const market = catalog.sources.find(item => item.id === 'market_bridge');
  const industry = catalog.sources.find(item => item.id === 'industry_bridge');
  const marketCard = card(market.name), industryCard = card(industry.name);
  await navigate('数据与设置');
  await until(`document.querySelectorAll('.ds-card').length===9`);
  await openConfig(marketCard);
  await setInput(`${marketCard} input[type="url"]`, `${fixtureOrigin}/quotes`);
  await setInput(`${marketCard} input[type="password"]`, fixtureToken);
  assert(!await evaluate(`document.querySelector(${JSON.stringify(`${marketCard} .ds-toggle-label input`)})?.checked`), 'Bridge fixture should keep automatic collection disabled');
  await navigate('市场总览');
  await navigate('数据与设置');
  await until(`document.querySelector(${JSON.stringify(`${marketCard} input[type="url"]`)})?.value===${JSON.stringify(`${fixtureOrigin}/quotes`)}`);
  assert(await evaluate(`document.querySelector(${JSON.stringify(`${marketCard} input[type="password"]`)})?.value===${JSON.stringify(fixtureToken)}`), 'Navigating away lost an unsaved in-memory credential');
  assert(!await evaluate(`JSON.stringify(Object.entries(localStorage)).includes(${JSON.stringify(fixtureToken)})`), 'Credential leaked into localStorage');
  await clickIn(marketCard, '保存配置');
  await until(`document.querySelector(${JSON.stringify(`${marketCard} .ds-action-notice`)})?.textContent.includes('配置已保存')`);
  const savedMarket = await source('market_bridge');
  assert(savedMarket.config.enabled === false && savedMarket.config.token_configured, 'Saved bridge did not preserve disabled mode and token marker');
  assert(!JSON.stringify(savedMarket).includes(fixtureToken) && !Object.hasOwn(savedMarket.config, 'token'), 'GET config exposed a credential');
  assert(await evaluate(`document.querySelector(${JSON.stringify(`${marketCard} input[type="password"]`)})?.value===''`), 'Credential was refilled after saving');
  checks.push('config draft survives navigation; token saved without echo or localStorage persistence');

  await clickIn(marketCard, '测试连接');
  await until(`document.querySelector(${JSON.stringify(`${marketCard} .ds-action-notice`)})?.textContent.includes('连接测试通过')`);
  const afterTest = await source('market_bridge');
  assert(!afterTest.last_success && !afterTest.last_attempt && afterTest.total_records === 0, 'Connection test incorrectly changed persisted sync state');
  assert((await api('/api/market/live')).quotes.length === 0, 'Connection test inserted fixture quotes');
  await syncCard(marketCard);
  const first = await api('/api/market/live');
  assert(first.source_id === 'market_bridge' && first.quotes.length === 5 && first.quotes.find(item => item.symbol === symbol)?.last === initialPrice, 'Disabled bridge manual sync did not populate the live snapshot');
  assert(first.price_history[symbol].length === 1, 'Fixture history should start at one received sample');
  checks.push('disabled source can test and sync manually; tests do not write data');

  // A second real UI sync creates exactly one new sample, not a demo history.
  await delay(5100); // Respect the shared upstream burst cache before changing a fixture.
  quoteVersion = 1;
  await syncCard(marketCard);
  await navigate('市场总览');
  await until(`document.querySelector('.market-monitor')?.textContent.includes('实时价格与历史行情') && document.querySelector('.market-monitor')?.textContent.includes(${JSON.stringify((initialPrice + 2).toLocaleString('zh-CN'))})`);
  const live = await api('/api/market/live');
  assert(live.price_history[symbol].length === 2, 'Live chart history mixed in synthetic demo bars or duplicate samples');
  assert(await evaluate(`document.querySelector('.market-monitor')?.textContent.includes(${JSON.stringify(market.name)})`), 'Overview did not name the actual bridge source');
  assert(!await evaluate(`[...document.querySelectorAll('.overview-grid, .chart-panel')].some(element=>element.getClientRects().length>0)`), 'Legacy demo trend remains visible alongside live quotes');
  assert(!await evaluate(`[...document.querySelectorAll('.quote-card .badge.demo')].some(element=>element.getClientRects().length>0)`), 'Live quote cards retained demo badges');
  checks.push('overview uses the actual bridge source and exactly received price samples, without the old demo chart');

  await navigate('数据与设置');
  await openConfig(industryCard);
  await setInput(`${industryCard} input[type="url"]`, `${fixtureOrigin}/industry`);
  await setInput(`${industryCard} input[type="password"]`, fixtureToken);
  await clickIn(industryCard, '保存配置');
  await until(`document.querySelector(${JSON.stringify(`${industryCard} .ds-action-notice`)})?.textContent.includes('配置已保存')`);
  await syncCard(industryCard);
  const observations = (await api('/api/observations')).filter(item => item.source.includes('隔离UI验收合成数据'));
  assert(observations.length === 4 && observations.every(item => !item.is_demo && item.available_at && item.note), 'Industry fixture did not preserve units, provenance and available time');
  await navigate('市场总览');
  await until(`document.querySelector('.industry-board')?.textContent.includes(${JSON.stringify(syntheticEntity)}) && document.querySelector('.industry-arrivals')?.textContent.includes(${JSON.stringify(syntheticTruckEntity)})`);
  assert(await evaluate(`document.querySelector('.industry-series-select')?.textContent.includes('2,256') && document.querySelector('.industry-arrivals')?.textContent.includes('37')`), 'Price/arrivals panels did not show the imported fixture values');
  checks.push('industry bridge populates purchase-price and truck-arrival panels with explicit synthetic provenance');

  await viewport(1400);
  await screenshot('overview-1400-synthetic.png', '.page-heading');
  await screenshot('industry-1400-synthetic.png', '.industry-board');
  await viewport(1080);
  await screenshot('overview-1080-synthetic.png', '.page-heading');
  await screenshot('industry-1080-synthetic.png', '.industry-board');
  await navigate('数据与设置');
  await refreshStatus();
  await screenshot('sources-1080-synthetic.png', '.data-sources');
  await screenshot('bridge-1080-synthetic.png', marketCard);
  await screenshot('config-1080-synthetic.png', `${marketCard} .ds-config-form`);
  await viewport(1400);
  await screenshot('sources-1400-synthetic.png', '.data-sources');
  await screenshot('bridge-1400-synthetic.png', marketCard);
  await screenshot('config-1400-synthetic.png', `${marketCard} .ds-config-form`);
  checks.push('1080px and 1400px overview, industry and source screens have no horizontal document overflow');

  // Provider failure must leave both prices and their original timestamp intact.
  const beforeFailure = await api('/api/market/live');
  await delay(5100); // Failure must exercise a new fetch, not the valid cache.
  marketFailure = true;
  await clickIn(marketCard, '立即同步');
  await until(`document.querySelector(${JSON.stringify(`${marketCard} .ds-action-notice.error`)})?.textContent.includes('503')`);
  await until(`document.querySelector(${JSON.stringify(`${marketCard} .ds-last-error`)})?.textContent.includes('503')`);
  const failedSource = await source('market_bridge'), afterFailure = await api('/api/market/live');
  assert(failedSource.last_error.includes('503') && failedSource.consecutive_failures === 1, 'Failed collection was not tracked independently');
  assert(afterFailure.last_success === beforeFailure.last_success && afterFailure.latest_data_at === beforeFailure.latest_data_at && JSON.stringify(afterFailure.quotes) === JSON.stringify(beforeFailure.quotes), 'Failure overwrote the successful snapshot or its source time');
  assert(!await evaluate(`document.body.innerText.includes(${JSON.stringify(fixtureToken)})`), 'Credential leaked into UI messages');
  assert(!JSON.stringify(failedSource).includes(fixtureToken), 'Credential leaked into persisted error status');
  await screenshot('failure-source-1400-synthetic.png', marketCard);
  await navigate('市场总览');
  await until(`document.querySelector('.market-error')?.textContent.includes('503')`);
  assert(await evaluate(`document.querySelector('.market-current')?.textContent.includes('2,369')`), 'Failure hid the last successful quote');
  await screenshot('failure-overview-1400-synthetic.png', '.market-monitor');
  checks.push('503 failure retains prior quotes, source timestamp and successful-sync time without exposing credentials');

  // Deterministic optimistic-lock regression: keep a draft, mutate via the API,
  // then try the stale UI revision. Do not overwrite the saved external change.
  marketFailure = false;
  await navigate('数据与设置');
  await openConfig(marketCard);
  await setInput(`${marketCard} .ds-interval-label input`, '15');
  const conflictBase = await source('market_bridge');
  await api('/api/data-sources/market_bridge/config', { method: 'POST', body: { revision: conflictBase.config.revision, enabled: false, interval_seconds: 20, endpoint: conflictBase.config.endpoint } });
  // The SSE refresh may detect the new revision before the stale save reaches
  // the server. Both paths must show the same retained-draft conflict state.
  await evaluate(`(() => {const button=[...document.querySelector(${JSON.stringify(marketCard)}).querySelectorAll('button')].find(item=>item.textContent.trim()==='保存配置');if(button&&!button.disabled)button.click();})()`);
  await until(`document.querySelector(${JSON.stringify(`${marketCard} .ds-conflict`)}) !== null`);
  assert(await evaluate(`document.querySelector(${JSON.stringify(`${marketCard} .ds-interval-label input`)})?.value==='15'`), 'Revision conflict discarded the local interval draft');
  assert((await source('market_bridge')).config.interval_seconds === 20, 'Stale UI save overwrote a newer configuration');
  await clickIn(marketCard, '撤销本页修改，使用服务器配置');
  await until(`!document.querySelector(${JSON.stringify(`${marketCard} .ds-conflict`)})`);
  assert((await source('market_bridge')).config.token_configured, 'Blank token in a config update deleted the saved credential');
  checks.push('configuration revision conflicts preserve the local draft and the saved credential');
  assert(fixtureCounts.rejected === 0, 'Bridge requests failed to send the stored credential');
  assert(rendererErrors.length === 0, `Renderer exceptions: ${rendererErrors.join('; ')}`);

  await writeFile(path.join(artifacts, 'report.json'), JSON.stringify({ passed: true, fixtures: 'synthetic, loopback-only, disposable database', checks, fixture_requests: fixtureCounts, viewport_widths: [1080, 1400], created_at: new Date().toISOString() }, null, 2));
  await rm(path.join(artifacts, 'failure-unexpected-synthetic.png'), { force: true });
  console.log(`PASS: ${checks.length} v0.6 Electron integration checks. Synthetic fixtures only; screenshots and report: artifacts/v6-data-ui/`);
  }
} catch (error) {
  try { await screenshot(livePublic ? 'live-unexpected-failure.png' : 'failure-unexpected-synthetic.png'); } catch {}
  throw new Error(clean(error instanceof Error ? error.stack : error));
} finally {
  if (child.exitCode === null && socket?.readyState === WebSocket.OPEN) {
    // An unsuccessful assertion may leave an unsaved draft. Graceful shutdown
    // is attempted first; the disposable process group is terminated below.
    try { await command('Browser.close'); } catch {}
    await delay(500);
  }
  socket?.close();
  for (const task of pending.values()) clearTimeout(task.timer);
  try {
    if (process.platform === 'win32') { if (child.exitCode === null && child.pid) spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', timeout: 10000 }); }
    else if (child.pid) process.kill(-child.pid, 'SIGKILL');
  } catch {}
  if (fixtureServer.listening) {
    fixtureServer.closeAllConnections();
    await new Promise(resolve => fixtureServer.close(resolve));
  }
  await delay(300);
  await rm(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
}
