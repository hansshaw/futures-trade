import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { AlertCircle, ArrowRight, BookOpen, CalendarDays, Check, Clock3, Download, ExternalLink, FileText, History, KeyRound, LoaderCircle, NotebookPen, Plus, RefreshCw, Settings2, ShieldCheck, Sparkles, Sunrise, Sunset, X } from 'lucide-react';
import { Busy, time } from './components';
import { reportAPI, type DailyReport, type ReportEvidence, type ReportJob, type ReportKind, type ReportList, type ReportMode, type ReportSettings, type ReportSettingsInput } from './report-api';
import './DailyReports.css';

type Notice = { text: string; error?: boolean };
type SettingsDraft = ReportSettingsInput & { api_key: string; clear_api_key: boolean; original: string };
const kindName: Record<ReportKind, string> = { pre: '盘前', post: '盘后' };
const evidenceNames: Record<string, string> = { market: '行情快照', related_futures: '关联期货', technical: '已完成日线', purchase_price: '收购价', truck_arrivals: '到车与待卸', weather_forecast: '天气预报', weather: '天气观测', import: '进口相关资料', related_price: '关联现货', industry: '其他产业资料', event: '公告与事件' };
const statusName: Record<ReportJob['status'], string> = { queued: '等待执行', running: '正在生成', succeeded: '已完成', failed: '生成失败', interrupted: '执行中断', missed: '已错过窗口' };
const stageName: Record<string, string> = { queued: '任务已排队', collecting: '正在同步数据', syncing: '正在同步数据', evidence: '正在整理可得依据', gathering: '正在整理可得依据', analyzing: '正在分析证据', generating: '正在生成报告', llm: '等待模型回复', saving: '正在保存报告', journal: '正在保存复盘', completed: '已完成', succeeded: '已完成' };
const modeName = (mode: ReportMode) => mode === 'llm' ? 'AI 分析' : '规则预览';
const errorText = (error: unknown, secret = '') => {
  const text = error instanceof Error ? error.message : '操作失败，请稍后重试。';
  return secret ? text.split(secret).join('[密钥已隐藏]') : text;
};
function comparable(input: ReportSettingsInput) {
  return JSON.stringify({ base_url: input.base_url, model: input.model, enabled: input.enabled, pre_time: input.pre_time, post_time: input.post_time, auto_save: input.auto_save, skip_dates: [...input.skip_dates].sort() });
}
function makeDraft(settings: ReportSettings): SettingsDraft {
  const input = { revision: settings.revision, base_url: settings.base_url, model: settings.model, api_key: '', clear_api_key: false, enabled: settings.enabled, pre_time: settings.pre_time, post_time: settings.post_time, auto_save: settings.auto_save, skip_dates: [...(settings.skip_dates || [])] };
  return { ...input, original: comparable(input) };
}
const dirtyDraft = (draft: SettingsDraft | null) => !!draft && (comparable(draft) !== draft.original || !!draft.api_key || draft.clear_api_key);
function safeURL(value: string) {
  try { const url = new URL(value); return ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password ? value : undefined; }
  catch { return undefined; }
}
function exportReport(report: DailyReport) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(report, null, 2)], { type: 'application/json' }));
  const anchor = document.createElement('a');
  anchor.href = url; anchor.download = `cornwatch-${report.date}-${report.kind}-${report.mode}-${report.id.slice(0, 8)}.json`; anchor.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function CitationButtons({ ids, evidence, onSelect }: { ids: string[]; evidence: ReportEvidence[]; onSelect: (id: string) => void }) {
  return <div className="dr-citations">{[...new Set(ids)].map(id => {
    const index = evidence.findIndex(item => item.id === id);
    return <button type="button" className={`dr-citation ${index < 0 ? 'missing' : ''}`} key={id} title={index < 0 ? id : `${evidence[index].title} · ${id}`} disabled={index < 0} onClick={() => onSelect(id)}>{index < 0 ? '来源未找到' : `依据 ${index + 1}`}</button>;
  })}</div>;
}

function ReportDocument({ report, saving, onSave, onOpenJournal }: { report: DailyReport; saving: boolean; onSave: () => void; onOpenJournal: (id: string) => void }) {
  const [expanded, setExpanded] = useState(false);
  const [focused, setFocused] = useState('');
  const evidence = report.bundle.evidence || [];
  const focusEvidence = (id: string) => {
    setExpanded(true); setFocused(id);
    requestAnimationFrame(() => document.getElementById(`daily-evidence-${encodeURIComponent(id)}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' }));
  };
  const warnings = [...new Set([...(report.warnings || []), ...(report.bundle.warnings || [])])];
  return <article className="panel dr-report" aria-label="每日分析报告">
    <header className="dr-report-heading">
      <div className="dr-report-meta"><span className={`dr-pill ${report.mode === 'rules' ? 'dr-mode-rule' : ''}`}>{modeName(report.mode)}</span><span>{report.date} · {kindName[report.kind]}研究日</span><span>{report.symbol.toUpperCase()}</span></div>
      <h2>{report.date} {kindName[report.kind]}市场分析</h2>
      <p>生成 {time(report.created_at, true)} · 依据截止 {time(report.bundle.as_of, true)}<br />{report.mode === 'llm' ? `模型 ${report.output.model || report.model} · AI 推断仍需人工复核；引用匹配不代表推断正确。` : '本地规则整理 · 未调用大模型，不作为 AI 报告。'}</p>
    </header>
    <div className="dr-report-actions">
      <button className="secondary-button" onClick={() => exportReport(report)}><Download size={14} />导出 JSON 与依据</button>
      {report.journal_id ? <button className="secondary-button" onClick={() => onOpenJournal(report.journal_id!)}><BookOpen size={14} />查看已保存复盘</button> : <button className="primary-button" disabled={saving} onClick={onSave}><Busy busy={saving}><NotebookPen size={14} />保存到复盘</Busy></button>}
      {report.journal_id && <span className="dr-saved-id"><Check size={11} /> 已关联复盘 {report.journal_id.slice(0, 8)}</span>}
    </div>
    <div className="dr-summary">{report.output.summary || report.summary}</div>
    <div className="dr-report-body">
      {report.output.sections.map((section, index) => <section className="dr-section" key={`${section.title}-${index}`}>
        <h3>{section.title}</h3><p>{section.body}</p>
        {!!section.quotes?.length && <div>{section.quotes.map((quote, qIndex) => <blockquote className="dr-claim" key={`${quote.evidence_id}-${qIndex}`}><strong>对应原文片段</strong><p>“{quote.text}”</p><CitationButtons ids={[quote.evidence_id]} evidence={evidence} onSelect={focusEvidence} /></blockquote>)}</div>}
        <CitationButtons ids={section.evidence_ids || []} evidence={evidence} onSelect={focusEvidence} />
      </section>)}
      {!!report.output.scenarios?.length && <section className="dr-section"><h3><ArrowRight size={15} />观察情景与失效条件</h3>{report.output.scenarios.map((scenario, index) => <article className="dr-claim" key={`${scenario.name}-${index}`}><strong>{scenario.name}</strong><p>触发观察：{scenario.condition}</p><p>失效条件：{scenario.invalidation || '报告未提供，需补充核验。'}</p><CitationButtons ids={scenario.evidence_ids || []} evidence={evidence} onSelect={focusEvidence} /></article>)}</section>}
      {!!report.output.risks?.length && <section className="dr-section"><h3><ShieldCheck size={15} />风险与反证</h3><div className="dr-risk-list"><ul>{report.output.risks.map((risk, index) => <li key={index}>{risk}</li>)}</ul></div></section>}
      {!!warnings.length && <section className="dr-section"><h3><AlertCircle size={15} />数据范围与缺口</h3><ul>{warnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul></section>}
    </div>
    <details className="dr-evidence" open={expanded} onToggle={event => setExpanded(event.currentTarget.open)}>
      <summary>查看原始依据与时间口径 · {evidence.length} 条</summary>
      <div className="dr-evidence-list">{evidence.map((item, index) => <article id={`daily-evidence-${encodeURIComponent(item.id)}`} className={`dr-evidence-item ${focused === item.id ? 'focused' : ''}`} key={item.id}>
        <header><strong>依据 {index + 1} · {item.title}</strong>{item.is_forecast && <span className="dr-pill warning">预测目标</span>}</header>
        <p>{item.text}</p>
        {item.note && <p>口径：{item.note}</p>}
        <div className="dr-evidence-times"><span>{item.is_forecast ? '预报目标日 / 有效时刻' : '观测 / 发布时刻'}<b>{time(item.observed_at, true)}</b></span><span>本系统首次可用<b>{time(item.available_at, true)}</b></span><span>来源<b>{safeURL(item.source_url) ? <a href={safeURL(item.source_url)} target="_blank" rel="noreferrer noopener">{item.source} <ExternalLink size={10} /></a> : item.source}</b></span><span>类型 / 单位<b>{evidenceNames[item.category] || item.category}{item.unit ? ` · ${item.unit}` : ''}</b></span></div>
        {item.is_forecast && <p>这是当时可获得的预报；未来目标日不代表未来实况已经发生。</p>}
        <code>{item.id}</code>{!!item.original_ids?.length && <p>底层观测 ID：<code>{item.original_ids.join(" · ")}</code></p>}
      </article>)}</div>
      <p className="dr-provenance">依据指纹 {report.bundle.input_sha256}<br />输出引用指纹 {report.output.input_sha256}<br />提示词版本 {report.output.prompt_version} · 报告 ID {report.id}</p>
    </details>
  </article>;
}

export default function DailyReports({ active = true, onOpenJournal, onDataSources }: { active?: boolean; onOpenJournal: (id: string) => void; onDataSources: () => void }) {
  const [settings, setSettings] = useState<ReportSettings | null>(null);
  const [draft, setDraft] = useState<SettingsDraft | null>(null);
  const [data, setData] = useState<ReportList | null>(null);
  const [report, setReport] = useState<DailyReport | null>(null);
  const [selected, setSelected] = useState('');
  const [loading, setLoading] = useState(true);
  const [reportLoading, setReportLoading] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [reportError, setReportError] = useState('');
  const [notice, setNotice] = useState<Notice | null>(null);
  const [operation, setOperation] = useState('');
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [excludeDate, setExcludeDate] = useState('');
  const [kindFilter, setKindFilter] = useState('all');
  const [modeFilter, setModeFilter] = useState('all');
  const [search, setSearch] = useState('');
  const [previewKind, setPreviewKind] = useState<ReportKind>('pre');
  const requestSequence = useRef(0);
  const reportSequence = useRef(0);
  const selectedRef = useRef('');
  const followJob = useRef('');
  const operationRef = useRef(false);
  const draftRef = useRef<SettingsDraft | null>(null);
  const settingsPanel = useRef<HTMLElement>(null);
  draftRef.current = draft;
  const dirty = dirtyDraft(draft);
  const conflict = !!draft && !!settings && dirty && draft.revision !== settings.revision;
  const running = data?.jobs.find(job => job.status === 'queued' || job.status === 'running');
  const locked = !!operation;

  const readReport = useCallback(async (id: string) => {
    const sequence = ++reportSequence.current;
    selectedRef.current = id; setSelected(id); setReport(null); setReportLoading(true); setReportError('');
    try { const incoming = await reportAPI.read(id); if (sequence === reportSequence.current) setReport(incoming); }
    catch (error) { if (sequence === reportSequence.current) setReportError(errorText(error)); }
    finally { if (sequence === reportSequence.current) setReportLoading(false); }
  }, []);

  const refresh = useCallback(async (visible = false) => {
    const sequence = ++requestSequence.current;
    if (visible) setLoading(true);
    const incoming = await Promise.allSettled([reportAPI.list(), reportAPI.settings()]);
    if (sequence !== requestSequence.current) return;
    const failures: string[] = [];
    if (incoming[0].status === 'fulfilled') {
      const next = incoming[0].value; setData(next);
      const job = next.jobs.find(item => item.id === followJob.current);
      if (job && ['succeeded', 'failed', 'interrupted', 'missed'].includes(job.status)) {
        followJob.current = '';
        if (job.status === 'succeeded' && job.report_id) { setNotice({ text: `${kindName[job.kind]}${modeName(job.mode)}已生成，依据与报告已保存。${job.error ? ` ${job.error}` : ''}`, error: !!job.error }); void readReport(job.report_id); }
        else setNotice({ text: job.error || `任务${statusName[job.status]}。没有生成可用报告，请检查任务状态。`, error: true });
      } else if (!selectedRef.current && next.reports[0]) void readReport(next.reports[0].id);
    } else failures.push(errorText(incoming[0].reason, draftRef.current?.api_key));
    if (incoming[1].status === 'fulfilled') {
      const next = incoming[1].value; setSettings(next);
      setDraft(previous => dirtyDraft(previous) ? previous : makeDraft(next));
    } else failures.push(errorText(incoming[1].reason, draftRef.current?.api_key));
    setLoadError(failures.join('；')); setLoading(false);
  }, [readReport]);

  useEffect(() => {
    if (!active) return;
    void refresh();
    const timer = setInterval(() => void refresh(), 5000);
    let updateTimer: ReturnType<typeof setTimeout>;
    const unsubscribe = window.cornwatch?.onUpdate(update => {
      if (update.kind === 'daily-reports' || update.kind === 'report-settings') { clearTimeout(updateTimer); updateTimer = setTimeout(() => void refresh(), 150); }
    });
    return () => { clearInterval(timer); clearTimeout(updateTimer); unsubscribe?.(); };
  }, [active, refresh]);
  useEffect(() => () => { ++requestSequence.current; ++reportSequence.current; }, []);
  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => { if (dirtyDraft(draftRef.current)) { event.preventDefault(); event.returnValue = ''; } };
    window.addEventListener('beforeunload', beforeUnload); return () => window.removeEventListener('beforeunload', beforeUnload);
  }, []);

  const showSettings = () => { setSettingsOpen(true); requestAnimationFrame(() => settingsPanel.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })); };
  const change = (update: Partial<SettingsDraft>) => setDraft(previous => previous ? { ...previous, ...update } : previous);
  const runOperation = async (name: string, action: () => Promise<void>) => {
    if (operationRef.current) return;
    operationRef.current = true; setOperation(name); setNotice(null);
    try { await action(); }
    catch (error) { setNotice({ text: errorText(error, draftRef.current?.api_key), error: true }); }
    finally { operationRef.current = false; setOperation(''); }
  };
  const saveSettings = async (event: FormEvent) => {
    event.preventDefault(); if (!draft || conflict) return;
    const { original: _original, ...input } = draft;
    await runOperation('settings', async () => {
      const saved = await reportAPI.saveSettings({ ...input, api_key: input.api_key || undefined, skip_dates: [...input.skip_dates].sort() });
      setSettings(saved); setDraft(makeDraft(saved));
      setNotice({ text: '报告专用设置已保存。密钥输入已清空；后续任务使用已保存配置。' });
      await refresh();
    });
  };
  const addExcludedDate = () => {
    if (!draft || !excludeDate || !/^\d{4}-\d{2}-\d{2}$/.test(excludeDate)) return;
    change({ skip_dates: [...new Set([...draft.skip_dates, excludeDate])].sort() }); setExcludeDate('');
  };
  const generate = (kind: ReportKind, mode: ReportMode) => void runOperation(`generate-${kind}-${mode}`, async () => {
    const result = await reportAPI.generate(kind, mode);
    followJob.current = result.job.id;
    setData(previous => previous ? { ...previous, busy: true, jobs: [result.job, ...previous.jobs.filter(job => job.id !== result.job.id)] } : previous);
    setNotice({ text: `${kindName[kind]}${modeName(mode)}已提交。${mode === 'llm' ? '会向已保存的报告模型发送本次市场依据。' : '本次不调用大模型；生成结果会明确标为规则预览。'}` });
    await refresh();
  });
  const saveJournal = () => {
    if (!report) return; const reportId = report.id;
    void runOperation(`journal-${reportId}`, async () => {
      const saved = await reportAPI.journal(reportId);
      setReport(previous => previous?.id === reportId ? { ...previous, journal_id: saved.journal_id } : previous);
      setNotice({ text: saved.already_saved ? '这份报告已关联复盘，未重复创建。' : '报告已保存到复盘日记，可以继续补充个人判断与执行记录。' });
      await refresh();
    });
  };
  const visible = useMemo(() => (data?.reports || []).filter(item => (kindFilter === 'all' || item.kind === kindFilter) && (modeFilter === 'all' || item.mode === modeFilter) && `${item.date} ${item.symbol} ${item.summary}`.toLowerCase().includes(search.toLowerCase())), [data, kindFilter, modeFilter, search]);
  const generationDisabled = locked || !!data?.busy || !data;

  return <section className="daily-reports" aria-label="每日分析工作区">
    <div className="dr-topline"><div><span className="section-kicker">DAILY RESEARCH BRIEFING</span><h2>让每日判断，有时点、有依据、可复盘</h2><p>盘前整理已知信息，盘后核对变化。真实依据与模型推断分别保留，报告可以进入你的复盘日记。</p></div><div className="dr-toolbar"><button className="secondary-button" onClick={showSettings}><Settings2 size={14} />报告设置{dirty ? ' · 未保存' : ''}</button><button className="icon-button" title="刷新报告与任务状态" aria-label="刷新报告与任务状态" disabled={loading} onClick={() => void refresh(true)}><RefreshCw size={16} className={loading ? 'spin' : ''} /></button></div></div>

    {loadError && <div className="dr-notice error" role="alert"><AlertCircle size={16} /><span>{loadError}</span></div>}
    {notice && <div className={`dr-notice ${notice.error ? 'error' : ''}`} role={notice.error ? 'alert' : 'status'}>{notice.error ? <AlertCircle size={16} /> : <Check size={16} />}<span>{notice.text}</span></div>}

    <div className="dr-status-grid">
      {(['pre', 'post'] as const).map(kind => {
        const latestJob = data?.jobs.find(job => job.kind === kind && job.trigger === 'schedule');
        const latestSuccess = data?.jobs.find(job => job.kind === kind && job.trigger === 'schedule' && job.status === 'succeeded');
        const next = data?.next_runs.find(item => item.kind === kind);
        const Icon = kind === 'pre' ? Sunrise : Sunset;
        return <article className="dr-status-card" key={kind}><header><span><Icon size={14} /> {kindName[kind]}自动分析</span><span className={`dr-pill ${data?.scheduler_paused || !settings?.enabled ? 'off' : !settings.configured ? 'warning' : ''}`}>{!settings ? '加载中' : data?.scheduler_paused ? '服务已暂停调度' : !settings.enabled ? '已停用' : !settings.configured ? '待配置模型' : '已启用'}</span></header><strong>{(kind === 'pre' ? settings?.pre_time : settings?.post_time) || (kind === 'pre' ? '08:30' : '15:30')}<small>北京时间</small></strong><p>下次：{data?.scheduler_paused ? '环境设置已暂停自动任务' : next ? time(next.scheduled_at, true) : settings?.enabled && !settings.configured ? '模型配置完成后可执行' : '暂无待执行安排'}</p><p>最近自动任务：{latestJob ? `${latestJob.date} · ${statusName[latestJob.status]}` : '尚无记录'}</p>{latestSuccess && latestSuccess.id !== latestJob?.id && <p>上次自动完成：{time(latestSuccess.updated_at, true)}</p>}</article>;
      })}
      <article className="dr-status-card"><header><span><Sparkles size={14} /> 报告专用模型</span><span className={`dr-pill ${settings?.configured ? '' : 'off'}`}>{settings?.configured ? '已配置' : '尚未配置'}</span></header><strong className="dr-model-name">{settings?.model || '先用规则预览了解报告'}</strong><p>{settings?.source === 'environment' ? '初始设置继承自环境变量' : '使用已保存的报告专用设置'}</p><p>{settings?.auto_save ? 'AI 报告自动保存到复盘' : '报告保存后，由你选择是否写入复盘'}</p></article>
    </div>
    <p className="dr-runtime-note"><Clock3 size={13} /><span>{data?.scheduler_paused && '自动调度已被服务环境暂停；手动生成仍可使用。 '}{data?.scheduler_note || '应用和本地服务需要持续运行，才能执行定时分析。北京时间周一至周五并扣除排除日期，尚未接交易所节假日日历；最多 60 分钟窗口，盘前截至 09:00；每研究日/时段一次，不无限补跑、不自动重试付费请求。'}</span></p>

    <div className="dr-toolbar">
      <button className="primary-button" disabled={generationDisabled || !settings?.configured} onClick={() => generate('pre', 'llm')}><Busy busy={operation === 'generate-pre-llm'}><Sunrise size={15} />生成盘前 AI 分析</Busy></button>
      <button className="primary-button" disabled={generationDisabled || !settings?.configured} onClick={() => generate('post', 'llm')}><Busy busy={operation === 'generate-post-llm'}><Sunset size={15} />生成盘后 AI 分析</Busy></button>
      <label className="dr-preview-kind">规则时段<select aria-label="规则预览时段" value={previewKind} onChange={event => setPreviewKind(event.target.value as ReportKind)} disabled={generationDisabled}><option value="pre">盘前</option><option value="post">盘后</option></select></label>
      <button className="secondary-button" disabled={generationDisabled} onClick={() => generate(previewKind, 'rules')}><Busy busy={operation.startsWith('generate-') && operation.endsWith('-rules')}><FileText size={14} />规则预览</Busy></button>
    </div>
    {!settings?.configured && !loading && <div className="dr-notice warning"><KeyRound size={15} /><span>尚未配置可用的报告模型。规则预览无需账号；保存服务地址、模型名称及服务所需的凭据后，才会调用 AI；本机免密服务可以不填密钥。 <button className="text-button" onClick={showSettings}>打开报告设置</button></span></div>}
    {dirty && <p className="dr-runtime-note">当前有未保存的配置修改。手动生成和自动任务始终使用已保存设置。</p>}
    {running && <div className="dr-run-card" role="status"><LoaderCircle size={21} className="spin" /><div><h3>{kindName[running.kind]}{modeName(running.mode)} · {statusName[running.status]}</h3><p>{stageName[running.stage] || running.stage || '正在准备市场依据'} · 提交于 {time(running.created_at, true)}</p><p>结果会自动进入下方历史。切换页面不会把任务误标为成功，也不会取消后端任务。</p></div></div>}

    <section className="panel dr-settings" ref={settingsPanel} hidden={!settingsOpen} aria-label="每日分析设置">
      <div className="panel-header"><h2><Settings2 size={17} />报告模型与自动任务</h2><div className="dr-toolbar">{dirty && <span className="dr-settings-state">配置草稿未保存</span>}<button className="icon-button" aria-label="收起报告设置" onClick={() => setSettingsOpen(false)}><X size={16} /></button></div></div>
      {draft && settings ? <form onSubmit={event => void saveSettings(event)}>
        {conflict && <div className="dr-notice warning" role="alert"><AlertCircle size={15} /><span>服务器配置已更新；本页草稿仍保留，当前保存版本不一致。<button type="button" className="text-button" onClick={() => setDraft(makeDraft(settings))}>放弃本页草稿，读取最新配置</button></span></div>}
        <fieldset disabled={locked} className="dr-settings-fieldset"><div className="dr-settings-grid">
          <section className="dr-settings-section"><h3>模型连接 · 仅用于每日报告</h3><label className="dr-field">服务地址<input type="url" value={draft.base_url} onChange={event => change({ base_url: event.target.value })} placeholder="https://api.example.com/v1" autoComplete="off" spellCheck={false} /><small>填写兼容服务的基础地址。更换地址且未填新密钥时会清除旧密钥，避免把原服务凭据发给新地址。</small></label><label className="dr-field">模型名称<input value={draft.model} onChange={event => change({ model: event.target.value })} maxLength={160} placeholder="服务提供方的模型 ID" autoComplete="off" spellCheck={false} /></label><label className="dr-field">API Key<input type="password" value={draft.api_key} onChange={event => change({ api_key: event.target.value, clear_api_key: false })} placeholder={settings.key_configured && draft.base_url.replace(/\/+$/, '') === settings.base_url.replace(/\/+$/, '') ? '已保存密钥；同地址留空保留原值' : '按服务要求填写；本机免密可留空'} autoComplete="new-password" spellCheck={false} /><small>凭据仅保存在本机后端，不回显；数据库备份需妥善保管。未保存密钥只留在页面内存，不写浏览器存储，保存成功后清空输入。</small></label>{settings.key_configured && <label className="dr-checkbox"><input type="checkbox" checked={draft.clear_api_key} onChange={event => change({ clear_api_key: event.target.checked, api_key: '' })} /><span><strong>保存时清除现有报告密钥</strong><small>仅清除凭据；免密的本机服务仍可运行，其他服务需重新填写有效密钥。</small></span></label>}<p>该设置不改变事件分析、复盘智能体的环境变量配置。AI 任务会把本次选取的市场依据发送到此服务；是否收费取决于服务账户。</p><button type="button" className="secondary-button" disabled={locked || dirty || !!data?.busy || !settings.configured} onClick={() => void runOperation('test', async () => { const result = await reportAPI.test(); setNotice({ text: result.message, error: !result.ok }); })}><Busy busy={operation === 'test'}><Sparkles size={13} />测试已保存的模型连接</Busy></button><p>测试会发送一条最小示例请求，可能产生少量费用；不生成报告、不写复盘。{dirty ? '请先保存配置。' : ''}</p></section>
          <section className="dr-settings-section"><h3>自动生成与研究日安排</h3><label className="dr-checkbox"><input type="checkbox" checked={draft.enabled} onChange={event => change({ enabled: event.target.checked })} /><span><strong>启用工作日自动分析</strong><small>北京时间周一至周五，扣除下列排除日期；不是交易所认证交易日历。</small></span></label><div className="dr-inline-fields"><label className="dr-field">盘前时刻<input type="time" min="06:00" max="08:59" required value={draft.pre_time} onChange={event => change({ pre_time: event.target.value })} /></label><label className="dr-field">盘后时刻<input type="time" min="15:15" max="19:00" required value={draft.post_time} onChange={event => change({ post_time: event.target.value })} /></label></div><label className="dr-checkbox"><input type="checkbox" checked={draft.auto_save} onChange={event => change({ auto_save: event.target.checked })} /><span><strong>AI 报告自动保存为复盘日记</strong><small>规则预览可以手动保存；日记保存后仍可补充个人判断。</small></span></label><div className="dr-date-add"><label className="dr-field">排除日期<input type="date" value={excludeDate} onChange={event => setExcludeDate(event.target.value)} /></label><button type="button" className="secondary-button" disabled={!excludeDate} onClick={addExcludedDate}><Plus size={13} />添加</button></div><div className="dr-exclusions">{draft.skip_dates.map(date => <span className="dr-exclusion" key={date}>{date}<button type="button" aria-label={`移除排除日期 ${date}`} onClick={() => change({ skip_dates: draft.skip_dates.filter(value => value !== date) })}><X size={12} /></button></span>)}</div><p>{draft.skip_dates.length ? `已排除 ${draft.skip_dates.length} 天。` : '尚未添加排除日期。'} 可设盘前 06:00—08:59、盘后 15:15—19:00。请按交易所休市公告维护节假日；自动任务不会因周末调休变成周六/周日执行。</p><p>任务在计划时刻后的最多 {settings.window_minutes} 分钟内处理，盘前截至 09:00。默认 08:30 的盘前任务仅在 08:30—08:59 补执行；应用关闭时无法执行，窗口外不无限补跑。</p></section>
        </div></fieldset>
        <div className="dr-settings-footer"><p>草稿切页保留；关闭应用前请保存。当前配置 v{settings.revision} · {settings.source === 'environment' ? '环境变量初始值' : '本地已保存'}</p><div className="dr-toolbar"><button type="button" className="text-button" disabled={locked || !dirty} onClick={() => setDraft(makeDraft(settings))}>撤销配置修改</button><button type="submit" className="primary-button" disabled={locked || !dirty || conflict}><Busy busy={operation === 'settings'}><Check size={14} />保存报告设置</Busy></button></div></div>
      </form> : <div className="dr-history-empty">{loadError || '正在读取报告设置…'}</div>}
    </section>

    {!!data?.jobs.length && <section className="panel"><details className="dr-jobs"><summary>任务记录 · {data.jobs.length} 条 · 失败与错过单独保留</summary>{data.jobs.slice(0, 12).map(job => <article className="dr-job-row" key={job.id}><div><strong>{job.date} {kindName[job.kind]} · {modeName(job.mode)} · {job.trigger === 'schedule' ? '自动任务' : '手动生成'}</strong><p>{stageName[job.stage] || job.stage} · 更新 {time(job.updated_at, true)}</p>{job.error && <p className="dr-job-error">{job.error}</p>}</div><div><span className={`dr-pill ${['failed', 'interrupted'].includes(job.status) ? 'error' : job.status === 'missed' ? 'warning' : ''}`}>{statusName[job.status]}</span>{job.report_id && <button className="text-button" onClick={() => { followJob.current = ''; void readReport(job.report_id!); }}>查看报告</button>}</div></article>)}</details></section>}

    <div className="dr-workspace">
      <aside className="panel dr-history"><div className="panel-header"><h2><History size={15} />报告历史</h2><span className="muted">{data?.reports.length || 0} 份</span></div><div className="dr-history-filter"><select aria-label="筛选报告时段" value={kindFilter} onChange={event => setKindFilter(event.target.value)}><option value="all">所有时段</option><option value="pre">盘前</option><option value="post">盘后</option></select><select aria-label="筛选报告模式" value={modeFilter} onChange={event => setModeFilter(event.target.value)}><option value="all">所有模式</option><option value="llm">AI 分析</option><option value="rules">规则预览</option></select><input aria-label="搜索报告" placeholder="日期、合约或摘要" value={search} onChange={event => setSearch(event.target.value)} /></div><div className="dr-history-list">{visible.map(item => <button className={`dr-history-row ${selected === item.id ? 'selected' : ''}`} key={item.id} onClick={() => { followJob.current = ''; void readReport(item.id); }}><header><span>{item.date} · {kindName[item.kind]}</span><span className={`dr-pill ${item.mode === 'rules' ? 'dr-mode-rule' : ''}`}>{modeName(item.mode)}</span></header><strong>{item.summary || `${item.symbol.toUpperCase()} 每日研究记录`}</strong><p>{item.symbol.toUpperCase()} · {time(item.created_at)}</p>{!!item.warnings?.length && <span className="dr-history-warnings" title={item.warnings.join("；")}>{item.warnings.length} 条范围提示 · 打开报告查看全部</span>}<footer><span>{item.journal_id ? '已保存复盘' : '未保存复盘'}</span><ArrowRight size={12} /></footer></button>)}{!visible.length && <div className="dr-history-empty">{loading ? '正在读取历史…' : data?.reports.length ? '没有匹配的报告，请调整筛选。' : '生成的报告会按研究日期保存，可随时回看当时依据。'}</div>}</div></aside>
      {reportLoading ? <section className="panel dr-report-empty"><LoaderCircle size={27} className="spin" /><h3>正在读取完整报告</h3><p>报告与生成时使用的依据会一同显示。</p></section> : reportError ? <section className="panel dr-report-empty"><AlertCircle size={30} /><h3>报告读取失败</h3><p>{reportError}</p><button className="secondary-button" onClick={() => void readReport(selected)}>重试读取</button></section> : report ? <ReportDocument key={report.id} report={report} saving={operation === `journal-${report.id}`} onSave={saveJournal} onOpenJournal={onOpenJournal} /> : <section className="panel dr-report-empty"><CalendarDays size={42} strokeWidth={1.3} /><h3>从一份可核验的日常报告开始</h3><p>先在数据来源中同步真实行情与产业资料，再生成盘前或盘后报告。无需模型账号也可以查看规则预览；两种模式会明确区分。</p><button className="secondary-button" onClick={onDataSources}>查看数据来源 <ArrowRight size={14} /></button></section>}
    </div>
  </section>;
}
