import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { Activity, AlertCircle, ArrowDownToLine, CalendarClock, Check, ChevronDown, CloudRain, Database, ExternalLink, FileCheck2, Info, KeyRound, Link2, LoaderCircle, PauseCircle, PlugZap, RefreshCw, Save, Settings2, ShieldCheck, Sprout, Truck, Wheat, X } from 'lucide-react';
import { api } from './api';
import { Busy, Empty, number } from './components';
import './DataSources.css';
import type { PollingStatus } from './market-api';

type SourceId = 'boyar_prices' | 'boyar_arrivals' | 'import_auction' | 'sina' | 'liaoning' | 'moa' | 'weather' | 'market_bridge' | 'industry_bridge';
type SourceStatus = 'disabled' | 'unconfigured' | 'idle' | 'running' | 'ok' | 'error';
type Freshness = 'never' | 'fresh' | 'stale' | 'unknown' | 'market_closed';
interface SourceConfig {
  revision: number;
  enabled: boolean;
  interval_seconds: number;
  endpoint: string;
  token_configured: boolean;
}
interface DataSource {
  polling?: PollingStatus;
  id: SourceId;
  name: string;
  kind: string;
  description: string;
  official_url: string;
  account_required: boolean;
  cost_note: string;
  license_note: string;
  request_policy: string;
  configurable_endpoint: boolean;
  min_interval_seconds: number;
  max_interval_seconds: number;
  config: SourceConfig;
  status: SourceStatus;
  freshness: Freshness;
  freshness_note: string;
  last_attempt?: string;
  last_success?: string;
  latest_data_at?: string;
  next_run?: string;
  last_error: string;
  consecutive_failures: number;
  added: number;
  duplicates: number;
  total_records: number;
  coverage: string[];
}
interface SourceResponse { sources: DataSource[]; active_market_source: string; auto_sync_paused: boolean }
interface ActionResult { ok: boolean; message: string; record_count: number; latest_data_at?: string; coverage: string[]; added?: number; duplicates?: number }
type IntervalUnit = 1 | 60 | 3600;
interface ConfigDraft { enabled: boolean; interval: string; unit: IntervalUnit; endpoint: string; token: string; clearToken: boolean }
interface ResultNotice { kind: 'save' | 'test' | 'sync' | 'info' | 'error'; message: string; result?: ActionResult; at: string }

const sourceOrder: SourceId[] = ['sina', 'boyar_prices', 'boyar_arrivals', 'import_auction', 'liaoning', 'moa', 'weather', 'market_bridge', 'industry_bridge'];
const sourceMeta = {
  boyar_prices: {icon:Wheat,label:'延迟公开报价',group:'企业收购价',tone:'amber',summary:'企业报价表与已生效通知',note:'按原始日期查看最新公开资料。未注明价格单位的表格仅展示原值，不猜测元/吨；水分、含票条件按原文保留。未来生效通知只作为事件，不提前作为现行报价。'},
  boyar_arrivals: {icon:Truck,label:'晨间公开样本',group:'深加工到车',tone:'amber',summary:'山东晨间到车 · 不代表全天或全国',note:'原文未区分累计到厂、门前待卸及统计企业样本时明确保留不确定性。车辆数不直接换算吨数，最新记录不等于当前实时排队。'},
  import_auction: {icon:Database,label:'拍卖结果',group:'进口玉米参考',tone:'green',summary:'每日检查最近公开拍卖场次',note:'中储粮公开的进口玉米境内竞价销售结果，按实际场次统计。成交率不等于吨数，缺少原始吨价不反推；无当天公告不代表零成交，也不能视为全国每日新签进口量。'},
  sina: { icon: Activity, label: '公开参考快照', group: '期货行情', tone: 'amber', summary: '玉米与关联期货 · 公开行情参考', note: '公开快照的时效、完整性与可用性取决于上游。它不是交易所授权实时行情，也不能替代用于下单的授权行情通道。' },
  liaoning: { icon: Wheat, label: '官方周报', group: '玉米收购价', tone: 'green', summary: '区域收购价格 · 保留发布口径', note: '周报用于观察区域价格变化；报告发布日期与价格统计期不同，不能将周报报价解释为企业当前挂牌收购价。' },
  moa: { icon: Sprout, label: '官方周报', group: '关联产业', tone: 'green', summary: '畜产品与饲料 · 周度价格观察', note: '畜产品、饲料周度调查与期货合约价格的频率、单位、地区和样本不同，比较前需核对同口径数据。' },
  weather: { icon: CloudRain, label: '天气预报', group: '产区天气', tone: 'blue', summary: '重点产区点位 · 降水与气温', note: '记录天气预报快照。预报目标日期不等于采集时间；少量城市点位不能代表全省，预报也不等同于已发生的灾害。' },
  market_bridge: { icon: PlugZap, label: '授权桥接', group: '期货行情', tone: 'blue', summary: '连接本机授权行情适配器', note: '先在桥接脚本中接入已获授权的 SDK，再提供符合 CornWatch 协议的 HTTP JSON。配置地址本身不代表已获得行情授权。' },
  industry_bridge: { icon: Truck, label: '授权桥接', group: '收购价与到车量', tone: 'blue', summary: '企业报价与到车量 · 同口径跟踪', note: '需要你有权使用和保存的数据，以及按本项目协议编写的 JSON 适配器。此入口不等同于直接支持某家供应商的标准 API。' },
} as const;
const statusLabels: Record<SourceStatus, string> = { disabled: '自动采集关闭', unconfigured: '等待配置', idle: '等待采集', running: '任务进行中', ok: '上次同步成功', error: '上次同步失败' };
const freshnessLabels: Record<Freshness, string> = { never: '尚无数据', fresh: '数据在时效范围内', stale: '数据已陈旧', unknown: '数据时效未知', market_closed: '休市 · 保留最近数据' };

function formatTime(value?: string): string {
  if (!value || !Number.isFinite(Date.parse(value))) return '尚无记录';
  return new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
}
function intervalLabel(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '未设置';
  if (seconds % 3600 === 0) return `${seconds / 3600} 小时`;
  if (seconds % 60 === 0) return `${seconds / 60} 分钟`;
  return `${seconds} 秒`;
}
function redact(text: string, token = ''): string {
  let value = text;
  if (token) {
    for (const secret of [token, encodeURIComponent(token)]) value = value.split(secret).join('[凭据已隐藏]');
  }
  return value.replace(/(bearer\s+)[^\s,;"']+/gi, '$1[凭据已隐藏]')
    .replace(/([?&](?:token|api_key|apikey|key|secret|password|access_token)=)[^&#\s]+/gi, '$1[凭据已隐藏]')
    .replace(/(https?:\/\/)[^/@\s]+:[^/@\s]+@/gi, '$1[凭据已隐藏]@')
    .slice(0, 6000);
}
function errorText(error: unknown, token = ''): string { return redact(error instanceof Error ? error.message : '请求失败，请稍后重试。', token); }
function endpointLabel(value: string): string {
  if (!value) return '尚未设置';
  return redact(value);
}
function fromConfig(config: SourceConfig): ConfigDraft {
  const unit: IntervalUnit = config.interval_seconds % 3600 === 0 ? 3600 : config.interval_seconds % 60 === 0 ? 60 : 1;
  return { enabled: config.enabled, interval: String(config.interval_seconds / unit), unit, endpoint: config.endpoint || '', token: '', clearToken: false };
}
function seconds(draft: ConfigDraft): number { return Number(draft.interval) * draft.unit; }
function isDirty(draft: ConfigDraft, config: SourceConfig): boolean {
  return draft.enabled !== config.enabled || seconds(draft) !== config.interval_seconds || draft.endpoint.trim() !== (config.endpoint || '') || !!draft.token || draft.clearToken;
}
function safeOfficialUrl(value: string): string | null {
  try { const url = new URL(value); return url.protocol === 'https:' && !url.username && !url.password ? url.href : null; }
  catch { return null; }
}

function SourceGuide({ id, requestPolicy }: { id: SourceId; requestPolicy?: string }) {
  const bridge = id === 'market_bridge' || id === 'industry_bridge';
  return <details className="ds-guide">
    <summary><Info size={13} />{bridge ? '如何接入授权数据' : '查看数据口径与更新说明'}<ChevronDown size={13} /></summary>
    <div>
      <p>{sourceMeta[id].note}</p>
      {id === 'weather' && <p>Open-Meteo 免费接口适用于个人非商业研究；商业使用应另行购买对应服务与授权。本入口的固定免费端点不等于已接入商业套餐。</p>}
      {bridge && <ol>
        <li>{id === 'market_bridge' ? '在本机脚本中配置授权 SDK（例如 TqSdk）的账号与订阅权限；账号登录在脚本侧完成。' : '先确认收购价、到车量数据的使用与存储权限，并保留企业、地区、统计时段及来源。'}</li>
        <li>由适配脚本按 CornWatch 桥接协议返回 HTTP JSON。若在本机运行，建议只监听 127.0.0.1。</li>
        <li>在下方填入端点及可选 Bearer 令牌，保存后测试连接；测试通过后点击立即同步。</li>
      </ol>}
      {bridge && <p className="ds-guide-foot">上游 SDK 密码留在桥接脚本的配置中；这里的令牌只用于访问你的桥接服务。不要把供应商网页地址直接作为 JSON 接口。</p>}
      {id === 'market_bridge' && <p className="ds-guide-foot">仓库适配脚本：<code>adapters/tqsdk_bridge.py</code>，默认端点 <code>http://127.0.0.1:8787/quotes</code>。授权账号通过脚本侧环境变量配置。</p>}
      {bridge && <p className="ds-guide-foot">请求格式、启动与授权说明见仓库文档 <code>docs/data-bridge.md</code>。</p>}
      {requestPolicy && <p className="ds-guide-foot">请求频率与授权：{requestPolicy}</p>}
      <p className="ds-guide-foot">同步间隔是本程序的请求安排，最低间隔不是供应商许可额度。发布方可能按周更新，重复同步不会产生更高频的真实数据；页面刷新只读取本地缓存。</p>
    </div>
  </details>;
}

function ActionNotice({ notice, onClose }: { notice: ResultNotice; onClose: () => void }) {
  const success = notice.kind !== 'error';
  return <div className={`ds-action-notice ${success ? 'success' : 'error'}`} role={success ? 'status' : 'alert'}>
    {success ? <Check size={15} /> : <AlertCircle size={15} />}
    <div>
      <strong>{notice.kind === 'test' ? '连接测试通过 · 未入库' : notice.kind === 'sync' ? '同步完成' : notice.kind === 'save' ? '配置已保存' : notice.kind === 'info' ? '配置草稿' : '操作未完成'}</strong>
      <p>{notice.message}</p>
      {notice.result && <div className="ds-action-facts">
        <span>读取 {number(notice.result.record_count)} 条</span>
        {notice.kind === 'sync' && <><span>新增 / 更新 {number(notice.result.added)} 条</span><span>重复跳过 {number(notice.result.duplicates)} 条</span></>}
        {notice.result.latest_data_at && <span>返回数据时刻 {formatTime(notice.result.latest_data_at)}</span>}
      </div>}
      <small>本次操作：{formatTime(notice.at)}</small>
    </div>
    <button className="icon-button" type="button" aria-label="关闭操作提示" onClick={onClose}><X size={13} /></button>
  </div>;
}

interface SourceCardProps {
  source: DataSource;
  current: boolean;
  paused: boolean;
  onUpdate: (source: DataSource) => void;
  onRefresh: () => Promise<void>;
  onChanged: () => void;
  onDirty: (id: SourceId, dirty: boolean) => void;
}
function SourceCard({ source, current, paused, onUpdate, onRefresh, onChanged, onDirty }: SourceCardProps) {
  const [baseConfig, setBaseConfig] = useState(source.config);
  const [draft, setDraft] = useState<ConfigDraft>(() => fromConfig(source.config));
  const [expanded, setExpanded] = useState(false);
  const [operation, setOperation] = useState<'save' | 'test' | 'sync' | ''>('');
  const [notice, setNotice] = useState<ResultNotice | null>(null);
  const [conflict, setConflict] = useState(false);
  const operationRef = useRef(false);
  const dirty = isDirty(draft, baseConfig);
  const busy = !!operation || source.status === 'running';
  const meta = sourceMeta[source.id];
  const Icon = meta.icon;
  const officialUrl = safeOfficialUrl(source.official_url);
  const hasEndpoint = !source.configurable_endpoint || !!source.config.endpoint;
  const ready = hasEndpoint && source.status !== 'unconfigured';
  const changedElsewhere = dirty && source.config.revision !== baseConfig.revision;
  const syncStatus = source.status === 'running' ? 'running' : source.last_error ? 'error' : source.last_success ? 'ok' : source.status;

  useEffect(() => { onDirty(source.id, dirty); }, [source.id, dirty, onDirty]);
  useEffect(() => {
    if (!dirty) { setBaseConfig(source.config); setDraft(fromConfig(source.config)); setConflict(false); }
  }, [source.config.revision, source.config.enabled, source.config.interval_seconds, source.config.endpoint, source.config.token_configured, dirty]);

  function updateDraft(patch: Partial<ConfigDraft>) {
    if (operationRef.current) return;
    setDraft(previous => ({ ...previous, ...patch }));
  }
  function restoreConfig() {
    if (operationRef.current) return;
    setBaseConfig(source.config); setDraft(fromConfig(source.config)); setConflict(false);
    setNotice({ kind: 'info', message: '已读取当前保存的配置，本地修改已撤销。', at: new Date().toISOString() });
  }
  function retainDraft() {
    if (operationRef.current) return;
    setBaseConfig(source.config); setConflict(false);
    setNotice({ kind: 'info', message: `本页编辑仍保留，已基于 v${source.config.revision} 继续编辑；点击保存后才会应用这些设置。`, at: new Date().toISOString() });
  }
  function validateDraft(): string | null {
    const interval = seconds(draft);
    if (!draft.interval.trim() || !Number.isFinite(interval) || !Number.isSafeInteger(Math.round(interval * 1000)) || Math.abs(interval * 1000 - Math.round(interval * 1000)) > 1e-6 || interval < source.min_interval_seconds || interval > source.max_interval_seconds) return `同步间隔最细支持毫秒，范围 ${intervalLabel(source.min_interval_seconds)}至 ${intervalLabel(source.max_interval_seconds)}。`;
    if (source.configurable_endpoint) {
      if (draft.enabled && !draft.endpoint.trim()) return '请先填写桥接端点，或关闭自动采集后保存。';
      if (draft.endpoint.trim()) {
        let endpoint: URL;
        try { endpoint = new URL(draft.endpoint.trim()); } catch { return '请输入完整的 HTTP / HTTPS 桥接端点。'; }
        if (!['http:', 'https:'].includes(endpoint.protocol)) return '桥接地址应使用 HTTP / HTTPS。';
        if (endpoint.username || endpoint.password || [...endpoint.searchParams.keys()].some(key => /^(token|api_key|apikey|key|secret|password|access_token)$/i.test(key))) return '请将凭据填入独立令牌栏，地址中不要包含账号、密码或密钥。';
        if (endpoint.protocol === 'http:' && !['127.0.0.1', 'localhost', '[::1]'].includes(endpoint.hostname)) return '远程桥接须使用 HTTPS；HTTP 仅用于本机回环地址。';
        if (endpoint.search) return '桥接端点不接受查询参数，请在适配脚本中提供固定的 JSON 路径。';
        if (endpoint.hash) return '桥接地址不能包含 # 片段。';
      }
      if (/[\r\n\x00]/.test(draft.token)) return '令牌不能包含换行或空字符。';
    }
    return null;
  }
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (operationRef.current) return;
    const validation = validateDraft();
    if (validation) { setNotice({ kind: 'error', message: validation, at: new Date().toISOString() }); return; }
    operationRef.current = true; setOperation('save'); setNotice(null);
    const submitted = { ...draft };
    try {
      const body = { revision: baseConfig.revision, enabled: submitted.enabled, interval_seconds: seconds(submitted), endpoint: submitted.endpoint.trim(), ...(source.configurable_endpoint && submitted.token ? { token: submitted.token } : {}), ...(source.configurable_endpoint && submitted.clearToken ? { clear_token: true } : {}) };
      const result = await api<DataSource>(`/api/data-sources/${source.id}/config`, { method: 'POST', body });
      setBaseConfig(result.config); setDraft(fromConfig(result.config)); setConflict(false); onUpdate(result);
      setNotice({ kind: 'save', message: `v${result.config.revision} · ${result.config.enabled ? `已启用自动采集，间隔 ${intervalLabel(result.config.interval_seconds)}` : '已关闭自动采集，仍可手动测试与同步'}。保存不代表连接测试或同步已经成功。`, at: new Date().toISOString() });
      onChanged();
      await onRefresh();
    } catch (error) {
      const message = errorText(error, submitted.token);
      setNotice({ kind: 'error', message: `${message} 当前编辑保留在本页，尚未保存。`, at: new Date().toISOString() });
      if (/409|版本|冲突|revision|conflict/i.test(message)) { setConflict(true); await onRefresh(); }
    } finally { operationRef.current = false; setOperation(''); }
  }
  async function run(action: 'test' | 'sync') {
    if (operationRef.current || source.status === 'running' || dirty || !ready) return;
    operationRef.current = true; setOperation(action); setNotice(null);
    try {
      const result = await api<ActionResult>(`/api/data-sources/${source.id}/${action}`, { method: 'POST', body: {} });
      if (!result.ok) throw new Error(result.message || '来源没有返回可用数据。');
      setNotice({ kind: action, message: redact(result.message || (action === 'test' ? '已校验连接与返回格式，测试数据未写入研究库。' : '本次返回的数据已校验并入库。')), result, at: new Date().toISOString() });
      if (action === 'sync') onChanged();
    } catch (error) { setNotice({ kind: 'error', message: errorText(error), at: new Date().toISOString() }); }
    finally { operationRef.current = false; setOperation(''); await onRefresh(); }
  }
  function changeUnit(unit: IntervalUnit) { updateDraft({ unit, interval: String(seconds(draft) / unit) }); }

  return <article className={`ds-card panel ${syncStatus === 'error' ? 'has-error' : ''}`} aria-label={`${source.name}数据源`}>
    <header className="ds-card-header">
      <div className={`ds-source-icon ${meta.tone}`}><Icon size={22} /></div>
      <div className="ds-source-title">
        <div className="ds-source-kind"><span>{meta.group}</span><span className={`ds-origin ${meta.tone}`}>{meta.label}</span>{current && <span className="ds-current">当前市场使用</span>}</div>
        <h3>{source.name}</h3>
        <p>{meta.summary}</p>
      </div>
      <button className={`icon-button ${expanded ? 'active' : ''}`} type="button" aria-expanded={expanded} aria-label={`配置${source.name}`} title="编辑采集配置" onClick={() => setExpanded(!expanded)}><Settings2 size={18} /></button>
    </header>

    <div className="ds-card-body">
      <div className="ds-status-row">
        <span className={`ds-status ${syncStatus}`}>{source.status === 'running' || operation === 'sync' || operation === 'test' ? <LoaderCircle size={12} className="spin" /> : <i />}{operation === 'test' ? '正在测试 · 不入库' : operation === 'sync' ? '正在同步' : statusLabels[syncStatus] || syncStatus}</span>
        <span className={`ds-freshness ${source.freshness}`}>{freshnessLabels[source.freshness] || '数据时效未知'}</span>
        {dirty && <span className="ds-unsaved">配置待保存</span>}
      </div>
      <p className="ds-description">{source.description}</p>
      {source.freshness_note && <p className={`ds-freshness-note ${source.freshness === 'stale' ? 'warning' : ''}`}>{redact(source.freshness_note)}</p>}
      <div className="ds-coverage"><span>实际覆盖</span>{source.coverage?.length ? source.coverage.map(item => <b key={item}>{item}</b>) : <small>同步或测试后核验返回内容</small>}</div>

      <dl className="ds-times">
        <div><dt>上次尝试</dt><dd>{formatTime(source.last_attempt)}</dd></div>
        <div><dt>上次成功同步</dt><dd>{formatTime(source.last_success)}</dd></div>
        <div><dt>{source.id === 'weather' ? '预报采集时刻 · 非目标日期' : '来源数据时刻'}</dt><dd>{source.latest_data_at ? formatTime(source.latest_data_at) : '来源尚未提供'}</dd></div>
        <div><dt>下次自动同步</dt><dd>{!source.config.enabled ? '已停用' : paused ? '全局自动同步已暂停' : source.status === 'unconfigured' ? '等待配置' : source.next_run ? formatTime(source.next_run) : '等待调度'}</dd></div>
      </dl>
      {source.kind === 'market' && source.polling && <div className="ds-polling"><span>目标 <b>{number(source.polling.target_requests_per_minute,0)} 次/分</b> · {number(source.polling.target_interval_ms,0)} ms</span><span>过去60秒实际 <b>{source.polling.requests_last_minute} 次</b></span><span>最近耗时 {number(source.polling.last_latency_ms,0)} ms</span><small>{source.polling.persistence_note || '失败退避或慢响应会降低实际请求数；不重复并发追赶。'}</small></div>}
      <div className="ds-record-counts"><span>上次有效记录 <b>{number(source.total_records)}</b></span><span>新增 / 更新 <b>{number(source.added)}</b></span><span>重复跳过 <b>{number(source.duplicates)}</b></span>{source.consecutive_failures > 0 && <span className="ds-fail-count">连续失败 <b>{source.consecutive_failures}</b></span>}</div>

      {source.last_error && <details className="ds-last-error"><summary><AlertCircle size={13} /><span>{redact(source.last_error)}</span><ChevronDown size={13} /></summary><p>{redact(source.last_error)}</p><small>失败不会清除先前成功的数据；请同时查看数据时刻和新鲜度。</small></details>}

      <div className="ds-access-note"><KeyRound size={13} /><div><strong>{source.account_required ? '需要自备账号或数据授权' : '当前接入不要求账号'}</strong><p>{source.cost_note || '费用与使用条件请向来源方核实。'}</p>{source.license_note && <small>{source.license_note}</small>}</div>{officialUrl && <a href={officialUrl} target="_blank" rel="noreferrer noopener" aria-label={`${source.name}来源说明`} title="查看来源说明"><ExternalLink size={14} /></a>}</div>
      <SourceGuide id={source.id} requestPolicy={source.request_policy} />

      {expanded && <form className="ds-config-form" onSubmit={event => void save(event)}>
        <div className="ds-config-heading"><h4>采集配置</h4><span>保存版本 v{baseConfig.revision}</span></div>
        {(conflict || changedElsewhere) && <div className="ds-conflict" role="alert"><strong>配置已被其他操作更新</strong><p>本页草稿基于 v{baseConfig.revision}，服务器为 v{source.config.revision}。当前编辑仍保留，请比较后选择如何继续。</p><p>服务器当前：自动采集{source.config.enabled ? '开启' : '关闭'}，每 {intervalLabel(source.config.interval_seconds)}{source.configurable_endpoint ? `；端点 ${endpointLabel(source.config.endpoint)}` : ''}。</p><div><button className="text-button" type="button" disabled={busy} onClick={retainDraft}>保留本页编辑，基于最新版本继续</button><button className="text-button" type="button" disabled={busy} onClick={restoreConfig}>撤销本页修改，使用服务器配置</button></div></div>}
        <fieldset disabled={!!operation}>
          <label className="ds-toggle-label"><input type="checkbox" checked={draft.enabled} onChange={event => updateDraft({ enabled: event.target.checked })} /><span><strong>启用自动采集</strong><small>保存后按设定频率采集；关闭后仍可手动测试与同步{paused ? '。当前全局自动同步暂停' : ''}。</small></span></label>
          <label className="ds-interval-label">采集间隔<div><input type="number" required step="any" min={source.min_interval_seconds / draft.unit} max={source.max_interval_seconds / draft.unit} value={draft.interval} onChange={event => updateDraft({ interval: event.target.value })} aria-label={`${source.name}采集间隔`} /><select value={draft.unit} onChange={event => changeUnit(Number(event.target.value) as IntervalUnit)} aria-label={`${source.name}采集间隔单位`}><option value={1}>秒</option><option value={60}>分钟</option><option value={3600}>小时</option></select></div><small>范围 {intervalLabel(source.min_interval_seconds)}至 {intervalLabel(source.max_interval_seconds)}；请求频率不等于发布频率。</small></label>
          {source.kind === 'market' && source.min_interval_seconds <= .5 && <div className="ds-frequency-preset"><button type="button" className="secondary-button" onClick={() => updateDraft({ interval: '0.5', unit: 1 })}>设为 120 次 / 分钟（500 ms）</button><small>保存配置后生效。采集目标不是上游更新承诺；请求被拒时会自动退避。</small></div>}
          {source.configurable_endpoint && <>
            <label>桥接端点<input type="url" maxLength={2000} autoComplete="off" spellCheck={false} value={draft.endpoint} required={draft.enabled} onChange={event => updateDraft({ endpoint: event.target.value })} placeholder={source.id === 'market_bridge' ? 'http://127.0.0.1:8787/quotes' : 'http://127.0.0.1:8766/industry'} /><small>示例地址需替换为正在运行的桥接服务；凭据请放入下方令牌栏。</small></label>
            <label>访问令牌 <span className="ds-token-state">{source.config.token_configured ? '已设置 · 不回显' : '尚未设置'}</span><input type="password" maxLength={4096} autoComplete="new-password" spellCheck={false} disabled={draft.clearToken} value={draft.token} onChange={event => updateDraft({ token: event.target.value })} placeholder={source.config.token_configured ? '同服务留空保留；新令牌可替换' : '可选，用于访问你自己的桥接服务'} /><small>仅随保存请求提交，不写入浏览器本地存储。更换协议、主机或端口时需重新填写，避免向新服务发送旧令牌。</small></label>
            {source.config.token_configured && <label className="ds-clear-token"><input type="checkbox" checked={draft.clearToken} onChange={event => updateDraft({ clearToken: event.target.checked, token: '' })} /><span>保存时删除现有令牌</span></label>}
          </>}
        </fieldset>
        <div className="ds-save-actions"><span>{dirty ? '有未保存修改 · 测试与同步使用已保存配置' : '配置与服务器保存版本一致'}</span><button className="text-button" type="button" disabled={!!operation || !dirty} onClick={restoreConfig}>撤销修改</button><button className="primary-button" type="submit" disabled={!!operation || !dirty || conflict || changedElsewhere}><Busy busy={operation === 'save'}><Save size={13} />保存配置</Busy></button></div>
      </form>}

      {notice && <ActionNotice notice={notice} onClose={() => setNotice(null)} />}
    </div>

    <footer className="ds-card-footer">
      <div><span><CalendarClock size={12} />{source.config.enabled ? `每 ${intervalLabel(source.config.interval_seconds)}` : '自动采集关闭'}</span>{source.configurable_endpoint && <small title={endpointLabel(source.config.endpoint)}>{endpointLabel(source.config.endpoint)}</small>}</div>
      <div className="ds-card-actions">
        {!ready && <button className="text-button" type="button" onClick={() => setExpanded(true)}>配置接入</button>}
        <button className="secondary-button" type="button" disabled={busy || dirty || !ready} title={dirty ? '请先保存配置，再测试连接' : !ready ? '请先配置桥接端点并保存' : '测试已保存的连接与返回格式，不入库'} onClick={() => void run('test')}><Busy busy={operation === 'test'}><FileCheck2 size={13} />测试连接</Busy></button>
        <button className="secondary-button ds-sync-button" type="button" disabled={busy || dirty || !ready} title={dirty ? '请先保存配置，再同步' : !ready ? '请先配置桥接端点并保存' : '按已保存配置读取并入库'} onClick={() => void run('sync')}><Busy busy={operation === 'sync'}><ArrowDownToLine size={13} />立即同步</Busy></button>
      </div>
    </footer>
  </article>;
}

export default function DataSources({ onChanged, active = true }: { onChanged?: () => void; active?: boolean }) {
  const [data, setData] = useState<SourceResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [checkedAt, setCheckedAt] = useState('');
  const [dirtySources, setDirtySources] = useState<Set<SourceId>>(new Set());
  const sequence = useRef(0);
  const mounted = useRef(true);
  const onChangedRef = useRef(onChanged);
  useEffect(() => { onChangedRef.current = onChanged; }, [onChanged]);

  const refresh = useCallback(async (visible = false) => {
    const request = ++sequence.current;
    if (visible) setLoading(true);
    try {
      const response = await api<SourceResponse>('/api/data-sources');
      if (request !== sequence.current || !mounted.current) return;
      if (!Array.isArray(response.sources)) throw new Error('数据源列表格式无效，请检查后端版本。');
      setData(response); setError(''); setCheckedAt(new Date().toISOString());
    } catch (failure) { if (request === sequence.current && mounted.current) setError(errorText(failure)); }
    finally { if (request === sequence.current && mounted.current) setLoading(false); }
  }, []);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; ++sequence.current; };
  }, []);
  useEffect(() => {
    if (!active) return;
    void refresh(true);
    const interval = setInterval(() => void refresh(), 15000);
    let timer: ReturnType<typeof setTimeout> | undefined;
    const onUpdate = (update: {kind:string}) => { if (timer) return; timer = setTimeout(() => { timer = undefined; void refresh(); }, update.kind === 'market' ? 1000 : 250); };
    const unsubscribe = window.cornwatch?.onUpdate(onUpdate);
    return () => { clearInterval(interval); if (timer) clearTimeout(timer); unsubscribe?.(); };
  }, [refresh, active]);
  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => { if (dirtySources.size) { event.preventDefault(); event.returnValue = ''; } };
    window.addEventListener('beforeunload', beforeUnload);
    return () => window.removeEventListener('beforeunload', beforeUnload);
  }, [dirtySources]);

  const updateSource = useCallback((source: DataSource) => {
    ++sequence.current;
    setData(previous => previous ? { ...previous, sources: previous.sources.map(item => item.id === source.id ? source : item) } : previous);
    setLoading(false);
  }, []);
  const reportDirty = useCallback((id: SourceId, dirty: boolean) => {
    setDirtySources(previous => { if (previous.has(id) === dirty) return previous; const next = new Set(previous); if (dirty) next.add(id); else next.delete(id); return next; });
  }, []);
  const changed = useCallback(() => onChangedRef.current?.(), []);
  const refreshSources = useCallback(() => refresh(), [refresh]);
  const sources = (data?.sources || []).filter(source => sourceOrder.includes(source.id)).sort((a, b) => sourceOrder.indexOf(a.id) - sourceOrder.indexOf(b.id));
  const enabled = sources.filter(source => source.config.enabled).length;
  const attention = sources.filter(source => source.config.enabled && (source.status === 'error' || source.freshness === 'stale')).length;
  const awaiting = sources.filter(source => source.status === 'unconfigured' || source.configurable_endpoint && !source.config.endpoint).length;
  const activeSource = sources.find(source => source.id === data?.active_market_source);

  return <section className="data-sources" aria-label="真实数据源管理">
    <div className="ds-console-heading"><div><span className="section-kicker">DATA CONNECTIONS</span><h2>真实数据源与更新状态</h2><p>保存接入配置，核验连接，再将可追溯的市场信息同步入库。</p></div><button className="secondary-button" disabled={loading} onClick={() => void refresh(true)}><RefreshCw size={14} className={loading ? 'spin' : ''} />刷新状态</button></div>
    <div className="ds-overview">
      <div><Database size={18} /><span>已启用<strong>{enabled}<small> / {sources.length}</small></strong></span></div>
      <div className={attention ? 'attention' : ''}><AlertCircle size={18} /><span>需要处理<strong>{attention}<small> 个来源</small></strong></span></div>
      <div><Link2 size={18} /><span>待配置桥接<strong>{awaiting}<small> 个来源</small></strong></span></div>
      <div className="ds-active-source"><Activity size={18} /><span>市场当前来源<strong>{activeSource?.name || '尚无可用来源'}</strong><small>{activeSource?.id === 'sina' ? '公开参考快照' : activeSource?.id === 'market_bridge' ? '授权 JSON 桥接' : '同步后核验覆盖与数据时间'}</small></span></div>
    </div>
    <div className="ds-console-note"><ShieldCheck size={15} /><span>报价快照与回测日线分开保存。采集成功只表示请求与入库完成，数据是否及时请看来源数据时刻与时效标记。</span></div>
    {data?.auto_sync_paused && <div className="ds-paused" role="status"><PauseCircle size={16} /><span>后台自动同步已全局暂停；已启用的数据源仍可手动测试和同步。恢复自动任务需调整服务端启动配置。</span></div>}
    {!!dirtySources.size && <div className="ds-draft-banner" role="status">{dirtySources.size} 个来源有未保存的配置。本页保留编辑内容，保存前不会用于测试或同步；凭据不写入浏览器草稿。</div>}
    {error && <div className="ds-load-error" role="alert"><AlertCircle size={16} /><span>状态读取失败：{error}{data ? '。继续显示上次成功读取的状态。' : ''}</span><button className="text-button" onClick={() => void refresh(true)}>重试</button></div>}
    {!data && loading ? <div className="ds-loading panel"><LoaderCircle size={24} className="spin" /><p>正在读取数据源配置与状态…</p></div> : <>
      <div className="ds-grid">{sources.map(source => <SourceCard key={source.id} source={source} current={data?.active_market_source === source.id} paused={!!data?.auto_sync_paused} onUpdate={updateSource} onRefresh={refreshSources} onChanged={changed} onDirty={reportDirty} />)}</div>
      {!sources.length && !error && <div className="panel"><Empty title="后端尚未返回数据源" detail="请检查服务版本与连接状态。这里不会用演示记录代替真实数据。" /></div>}
    </>}
    <footer className="ds-console-footer"><span>状态检查：{checkedAt ? formatTime(checkedAt) : '尚未完成'} · 时间均为上海时区</span><span>界面每 15 秒刷新状态，来源采集频率以各卡片配置为准。</span></footer>
  </section>;
}
