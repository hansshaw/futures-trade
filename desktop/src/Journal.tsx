import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Archive, ArrowLeft, BookOpen, BrainCircuit, Check, CheckCheck, ChevronLeft, ChevronRight, ClipboardList, Download, FileText, ImagePlus, LoaderCircle, Maximize2, Plus, RefreshCw, Save, Search, ShieldCheck, Sparkles, Star, Trash2, Undo2, X } from 'lucide-react';
import { Busy, Empty, Modal, number, time } from './components';
import { blankEntry, downloadFile, journalAPI, journalMarkdown, newDraftKey, readDrafts, readImage, shanghaiDate, writeDrafts, type Drafts, type JournalAsset, type JournalEntry, type JournalLesson, type JournalReview, type JournalSnapshot, type JournalSeed } from './journal-api';
import './journal.css';

type View = 'write' | 'agent' | 'lessons';
type Notice = { text: string; error?: boolean };
const bodyFields = [
  { key: 'plan', title: '盘前计划', kicker: '01 / PLAN', placeholder: '先写交易条件，再看结果。关注哪个价位、事件与数据？入场、止损、仓位和不交易的条件是什么？' },
  { key: 'thesis', title: '核心假设与失效条件', kicker: '02 / HYPOTHESIS', placeholder: '为什么可能有优势？区分已确认事实与推断；记录信息来源和当时可得时间。什么证据会推翻判断？' },
  { key: 'execution', title: '执行记录', kicker: '03 / EXECUTION', placeholder: '实际开平仓时间、价格、手数、手续费与滑点；是否按计划执行？观望也是一种决定。' },
  { key: 'review', title: '盘后复盘', kicker: '04 / REFLECTION', placeholder: '预期与盘面发生了什么差异？盈亏来自策略、执行还是偶然？记录支持和反对原判断的证据。' },
  { key: 'lessons', title: '经验与下一步', kicker: '05 / NEXT STEP', placeholder: '提炼一个可验证的改进：下次触发什么条件时做什么？需要新增哪组回测或数据来验证？' },
] as const;
const emotions = ['平静', '专注', '犹豫', '焦虑', '兴奋', '懊恼'];
const errorMessage = (error: unknown) => error instanceof Error ? error.message : '操作失败，请重试';
const monthLabel = (month: string) => `${month.slice(0, 4)} 年 ${Number(month.slice(5))} 月`;
const shortId = (id: string) => id.length > 12 ? `${id.slice(0, 10)}…` : id;

function Calendar({ month, entries, day, onMonth, onDay }: { month: string; entries: JournalEntry[]; day: string; onMonth: (month: string) => void; onDay: (day: string) => void }) {
  const [year, monthNumber] = month.split('-').map(Number);
  const start = (new Date(year, monthNumber - 1, 1).getDay() + 6) % 7;
  const days = new Date(year, monthNumber, 0).getDate();
  const counts = new Map<string, number>();
  entries.filter(entry => !entry.archived).forEach(entry => counts.set(entry.date, (counts.get(entry.date) || 0) + 1));
  function shift(offset: number) {
    const value = new Date(year, monthNumber - 1 + offset, 1);
    onMonth(`${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}`);
  }
  return <div className="journal-calendar">
    <div className="journal-calendar-head">
      <button className="icon-button" aria-label="上个月" onClick={() => shift(-1)}><ChevronLeft size={16} /></button>
      <strong>{monthLabel(month)}</strong>
      <button className="icon-button" aria-label="下个月" onClick={() => shift(1)}><ChevronRight size={16} /></button>
    </div>
    <div className="journal-calendar-grid">
      {['一', '二', '三', '四', '五', '六', '日'].map(value => <span className="journal-weekday" key={value}>{value}</span>)}
      {Array.from({ length: start }, (_, index) => <span key={`pad-${index}`} />)}
      {Array.from({ length: days }, (_, index) => {
        const date = `${month}-${String(index + 1).padStart(2, '0')}`;
        const count = counts.get(date) || 0;
        return <button key={date} aria-label={`${date}${count ? `，${count} 篇复盘` : '，暂无复盘'}`} aria-pressed={day === date} className={`${date === shanghaiDate() ? 'today' : ''} ${day === date ? 'selected' : ''}`} onClick={() => onDay(day === date ? '' : date)}>{index + 1}{count > 0 && <i />}</button>;
      })}
    </div>
    <div className="journal-calendar-foot"><span><i /> 已记录的日期</span><button className="text-button" onClick={() => { onMonth(shanghaiDate().slice(0, 7)); onDay(''); }}>回到本月</button></div>
  </div>;
}

function AssetGallery({ ids, assets, errors, onRetry, onRemove, onView, disabled }: { ids: string[]; assets: Record<string, JournalAsset>; errors: Record<string, string>; onRetry: (id: string) => void; onRemove: (id: string) => void; onView: (asset: JournalAsset) => void; disabled: boolean }) {
  return <div className="journal-assets">{ids.map(id => {
    const asset = assets[id];
    return <div className="journal-asset" key={id}>
      {asset?.data_url ? <button type="button" className="journal-image-button" aria-label={`放大 ${asset.name}`} onClick={() => onView(asset)}><img src={asset.data_url} alt={asset.name} /><span><Maximize2 size={13} /> 查看原图</span></button> : <div className="journal-asset-loading">{errors[id] ? <><span>{errors[id]}</span><button type="button" className="text-button" onClick={() => onRetry(id)}>重新读取</button></> : <><LoaderCircle size={18} className="spin" /><span>读取截图</span></>}</div>}
      <div className="journal-asset-caption"><span title={asset?.name || id}>{asset?.name || '截图附件'}</span><button type="button" className="icon-button" title="从本篇移除截图" aria-label={`移除 ${asset?.name || '截图'}`} disabled={disabled} onClick={() => onRemove(id)}><X size={14} /></button></div>
    </div>;
  })}</div>;
}

function SourceLinks({ ids, entries, onOpen }: { ids: string[]; entries: JournalEntry[]; onOpen: (entry: JournalEntry) => void }) {
  return <div className="journal-source-links">{ids.map(id => {
    const entry = entries.find(item => item.id === id);
    return entry ? <button key={id} className="journal-source-link" title={`来源 ID：${id}`} onClick={() => onOpen(entry)}><FileText size={11} />{entry.date} · {entry.symbol.toUpperCase()}</button> : <code key={id} title={id}>来源 {shortId(id)}</code>;
  })}</div>;
}

function ReviewCard({ review, entries, accepting, onAccept, onOpen, open }: { review: JournalReview; entries: JournalEntry[]; accepting: string; onAccept: (reviewId: string, lessonId: string) => void; onOpen: (entry: JournalEntry) => void; open: boolean }) {
  const [snapshot, setSnapshot] = useState<JournalSnapshot | null>(null);
  const [snapshotBusy, setSnapshotBusy] = useState(false);
  const [snapshotOpen, setSnapshotOpen] = useState(false);
  const [snapshotError, setSnapshotError] = useState('');
  const [snapshotImage, setSnapshotImage] = useState<JournalAsset | null>(null);
  const stale = review.journal_ids.some(id => entries.some(entry => entry.id === id && entry.revision !== review.input_revisions[id]));
  const missing = review.journal_ids.some(id => !entries.some(entry => entry.id === id));
  async function readSnapshot(exportOnly = false) {
    if (snapshotBusy) return;
    setSnapshotBusy(true); setSnapshotError('');
    try {
      const source = snapshot || await journalAPI.snapshot(review.id); setSnapshot(source);
      if (exportOnly) downloadFile(`cornwatch-review-${review.id}.json`, JSON.stringify({ schema: 'cornwatch.review.export.v1', source: 'immutable_review_snapshot', exported_at: new Date().toISOString(), review, snapshot: source, note: '包含分析当时的原始文字、经验、截图 ID 与图像指纹；截图字节保存在本机附件库。' }, null, 2), 'application/json');
      else setSnapshotOpen(true);
    } catch (error) { setSnapshotError(errorMessage(error)); }
    finally { setSnapshotBusy(false); }
  }
  async function viewSnapshotImage(id: string) {
    setSnapshotBusy(true); setSnapshotError('');
    try { const asset = await journalAPI.asset(id); if (!asset.data_url || !/^data:image\/(png|jpeg|webp);base64,/.test(asset.data_url)) throw new Error('截图格式不受支持'); setSnapshotImage(asset); }
    catch (error) { setSnapshotError(errorMessage(error)); }
    finally { setSnapshotBusy(false); }
  }
  return <details className="journal-review-card panel" open={open || undefined}>
    <summary><div><span className="section-kicker">{time(review.created_at, true)} · {review.model}</span><h3>{review.summary || '复盘整理结果'}</h3><div className="journal-review-badges"><span>{review.journal_ids.length} 篇来源 · {review.image_count || 0} 张截图</span><span className={stale || missing ? 'journal-badge warning' : 'journal-badge'}>{stale ? '源记录已更新 · 需重新整理' : missing ? '输入版本未核验' : '与当前保存版本一致'}</span></div></div><ChevronRight size={18} /></summary>
    <div className="journal-review-content">
      <SourceLinks ids={review.journal_ids} entries={entries} onOpen={onOpen} />
      {!!review.patterns?.length && <section><h4>观察到的模式</h4>{review.patterns.map((pattern, index) => <div className="journal-pattern" key={index}><strong>{pattern.title}</strong><p>{pattern.observation}</p><SourceLinks ids={pattern.journal_ids || []} entries={entries} onOpen={onOpen} /></div>)}</section>}
      <div className="journal-review-columns">
        {!!review.actions?.length && <section><h4><ClipboardList size={14} />下一步行动</h4><ul>{review.actions.map((action, index) => <li key={index}>{action}</li>)}</ul></section>}
        {!!review.questions?.length && <section><h4>需要你回答的问题</h4><ul>{review.questions.map((question, index) => <li key={index}>{question}</li>)}</ul></section>}
      </div>
      {!!review.risk_flags?.length && <div className="journal-review-risks"><ShieldCheck size={16} /><div>{review.risk_flags.map((flag, index) => <p key={index}>{flag}</p>)}</div></div>}
      {!!review.lessons?.length && <section><h4>候选经验 · 由你决定是否采纳</h4><div className="journal-candidate-list">{review.lessons.map(lesson => <div className="journal-candidate" key={lesson.id}><div><p>{lesson.text}</p><SourceLinks ids={lesson.evidence_ids || []} entries={entries} onOpen={onOpen} /></div><button className="secondary-button" disabled={lesson.status === 'accepted' || !!accepting} onClick={() => onAccept(review.id, lesson.id)}><Busy busy={accepting === lesson.id}>{lesson.status === 'accepted' ? <><CheckCheck size={14} />已采纳</> : <><Plus size={14} />采纳经验</>}</Busy></button></div>)}</div></section>}
      <div className="journal-snapshot-actions"><button className="secondary-button" disabled={snapshotBusy} onClick={() => void readSnapshot()}><Busy busy={snapshotBusy}><FileText size={13} />查看当时原始记录</Busy></button><button className="text-button" disabled={snapshotBusy} onClick={() => void readSnapshot(true)}><Download size={13} />导出分析与输入快照</button></div>
      {snapshotError && <p className="journal-snapshot-error" role="alert">{snapshotError}</p>}
      <details className="journal-provenance"><summary>查看分析追溯信息</summary><p>分析 ID：<code>{review.id}</code></p><p>输入指纹：<code>{review.input_sha256}</code></p>{review.journal_ids.map(id => <p key={id}>来源 <code>{id}</code> · 输入版本 v{review.input_revisions[id]}{entries.find(entry => entry.id === id)?.revision !== undefined ? ` · 当前 v${entries.find(entry => entry.id === id)?.revision}` : ''}</p>)}{!!review.context_lesson_ids?.length && <p>引用经验：{review.context_lesson_ids.map(id => <code key={id}>{id} </code>)}</p>}</details>
    </div>
    {snapshotOpen && snapshot && <Modal title="分析当时的原始依据" onClose={() => { setSnapshotOpen(false); setSnapshotImage(null); }}><div className="journal-snapshot-view"><p className="journal-snapshot-note">这是分析发生时保存的输入快照。之后编辑日记，不会改变这些原始依据。{snapshot.include_images ? '本次整理使用了截图。' : '本次整理未使用截图；附件保留用于核验记录。'}</p>{snapshot.journals.map(source => <details key={source.id} open className="journal-snapshot-entry"><summary>{source.date} · {source.title} <span>v{source.revision}</span></summary><p className="journal-snapshot-meta">合约 {source.symbol.toUpperCase()} · 纪律 {source.discipline || '未评分'} · 情绪 {source.emotion || '未填写'} · 自记盈亏 {source.pnl == null ? '未填写' : `${number(source.pnl, 2)} 元`}</p>{bodyFields.map(field => <section key={field.key}><h4>{field.title}</h4><p>{source[field.key] || '（未填写）'}</p></section>)}{!!source.attachment_ids.length && <div className="journal-snapshot-images">{source.attachment_ids.map((id, index) => <button className="secondary-button" key={id} disabled={snapshotBusy} onClick={() => void viewSnapshotImage(id)}><ImagePlus size={13} />查看截图 {index + 1}</button>)}</div>}<p className="journal-snapshot-meta">来源 ID：<code>{source.id}</code></p></details>)}{!!snapshot.accepted_lessons?.length && <section className="journal-snapshot-context"><h4>当时引用的已采纳经验</h4>{snapshot.accepted_lessons.map(lesson => <p key={lesson.id}>{lesson.text}<small>来源经验 {lesson.id} · 分析 {lesson.review_id}</small></p>)}</section>}{snapshotError && <p className="journal-snapshot-error" role="alert">{snapshotError}</p>}</div></Modal>}
    {snapshotImage?.data_url && <Modal title={snapshotImage.name} onClose={() => setSnapshotImage(null)}><div className="journal-image-preview"><img src={snapshotImage.data_url} alt={snapshotImage.name} /><p>{snapshotImage.width} × {snapshotImage.height} · {shortId(snapshotImage.id)}</p></div></Modal>}
  </details>;
}

export default function Journal({ llmConfigured, visionEnabled, seed, onSeedConsumed, active = true }: { llmConfigured: boolean; visionEnabled: boolean; seed?: JournalSeed; onSeedConsumed?: () => void; active?: boolean }) {
  const [boot] = useState(() => {
    const drafts = readDrafts();
    const latest = Object.entries(drafts).sort((a, b) => b[1].changed_at.localeCompare(a[1].changed_at))[0];
    return { drafts, key: latest?.[0] || newDraftKey(), entry: latest?.[1].entry || blankEntry() };
  });
  const [entries, setEntries] = useState<JournalEntry[]>([]);
  const [reviews, setReviews] = useState<JournalReview[]>([]);
  const [lessons, setLessons] = useState<JournalLesson[]>([]);
  const [drafts, setDrafts] = useState<Drafts>(boot.drafts);
  const draftsRef = useRef<Drafts>(boot.drafts);
  const [editorKey, setEditorKey] = useState(boot.key);
  const [entry, setEntry] = useState<JournalEntry>(boot.entry);
  const [tagsText, setTagsText] = useState(boot.entry.tags.join('，'));
  const [view, setView] = useState<View>('write');
  useEffect(()=>{if(active) requestAnimationFrame(()=>document.querySelector('.journal-page')?.scrollIntoView({block:'start'}));},[view,active]);
  const [month, setMonth] = useState(boot.entry.date.slice(0, 7));
  const [day, setDay] = useState('');
  const [search, setSearch] = useState('');
  const [archived, setArchived] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadingError, setLoadingError] = useState('');
  const [operation, setOperation] = useState('');
  const [notice, setNotice] = useState<Notice | null>(null);
  const [localSafe, setLocalSafe] = useState(true);
  const [conflict, setConflict] = useState(false);
  const [assets, setAssets] = useState<Record<string, JournalAsset>>({});
  const [assetErrors, setAssetErrors] = useState<Record<string, string>>({});
  const [zoom, setZoom] = useState<JournalAsset | null>(null);
  const [dragging, setDragging] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [agentSearch, setAgentSearch] = useState('');
  const [useLLM, setUseLLM] = useState(false);
  const [includeImages, setIncludeImages] = useState(false);
  const [useLessons, setUseLessons] = useState(true);
  const [reviewing, setReviewing] = useState(false);
  const [accepting, setAccepting] = useState('');
  const [latestReview, setLatestReview] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const latestSeed = useRef<JournalSeed | undefined>(undefined);
  const assetRequests = useRef(new Set<string>());
  const operationRef = useRef(false);
  const reviewingRef = useRef(false);
  const acceptingRef = useRef(false);
  const locked = !!operation;
  const dirty = !!drafts[editorKey];
  const activeEntries = useMemo(() => entries.filter(item => !item.archived), [entries]);
  const savedEntry = entries.find(item => item.id === entry.id);

  const storeDrafts = useCallback((next: Drafts) => {
    draftsRef.current = next; setDrafts(next); setLocalSafe(writeDrafts(next));
  }, []);
  const createEntry = useCallback((preset?: JournalSeed) => {
    if (operationRef.current) return;
    const next = blankEntry(preset); const key = newDraftKey();
    storeDrafts({ ...draftsRef.current, [key]: { entry: next, changed_at: new Date().toISOString() } });
    setEntry(next); setEditorKey(key); setTagsText(''); setConflict(false); setView('write'); setMonth(next.date.slice(0, 7)); setDay(''); setArchived(false);
  }, [storeDrafts]);
  const load = useCallback(async () => {
    setLoading(true);
    const results = await Promise.allSettled([journalAPI.list(), journalAPI.list(true), journalAPI.reviews(), journalAPI.lessons()]);
    if (results[0].status === 'fulfilled' && results[1].status === 'fulfilled') {
      const incoming = [...(results[0].value || []), ...(results[1].value || [])];
      setEntries(previous => { const merged = new Map(previous.map(item => [item.id, item])); incoming.forEach(item => { if (!merged.has(item.id) || merged.get(item.id)!.revision <= item.revision) merged.set(item.id, item); }); return [...merged.values()]; });
      setLoadingError('');
    }
    else setLoadingError(results.filter(result => result.status === 'rejected').map(result => errorMessage((result as PromiseRejectedResult).reason)).join('；'));
    if (results[2].status === 'fulfilled') setReviews(results[2].value || []);
    else setNotice({ text: `整理历史读取失败：${errorMessage(results[2].reason)}`, error: true });
    if (results[3].status === 'fulfilled') setLessons(results[3].value || []);
    else setNotice({ text: `经验库读取失败：${errorMessage(results[3].reason)}`, error: true });
    setLoading(false);
  }, []);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    if (seed && seed !== latestSeed.current && !locked) { latestSeed.current = seed; createEntry(seed); onSeedConsumed?.(); }
  }, [seed, createEntry, onSeedConsumed, locked]);
  useEffect(() => { setSelected(previous => previous.filter(id => entries.some(item => item.id === id && !item.archived))); }, [entries]);
  useEffect(() => { if (!llmConfigured) setUseLLM(false); if (!llmConfigured || !visionEnabled || !useLLM) setIncludeImages(false); }, [llmConfigured, visionEnabled, useLLM]);
  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's' && view === 'write' && active) { event.preventDefault(); if (!locked && !entry.archived) formRef.current?.requestSubmit(); }
      if (event.key === 'Escape') setZoom(null);
    }
    document.addEventListener('keydown', onKey); return () => document.removeEventListener('keydown', onKey);
  }, [view, locked, entry.archived, active]);
  useEffect(() => {
    function onBeforeUnload(event: BeforeUnloadEvent) { if (Object.keys(drafts).length > 0 && !localSafe) { event.preventDefault(); event.returnValue = ''; } }
    window.addEventListener('beforeunload', onBeforeUnload); return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [drafts, localSafe]);
  const fetchAsset = useCallback(async (id: string) => {
    if (assetRequests.current.has(id)) return;
    assetRequests.current.add(id);
    setAssetErrors(previous => { const next = { ...previous }; delete next[id]; return next; });
    try {
      const asset = await journalAPI.asset(id);
      if (!asset.data_url || !/^data:image\/(png|jpeg|webp);base64,/.test(asset.data_url)) throw new Error('附件格式不受支持');
      setAssets(previous => ({ ...previous, [id]: asset }));
    } catch (error) { setAssetErrors(previous => ({ ...previous, [id]: errorMessage(error) })); }
    finally { assetRequests.current.delete(id); }
  }, []);
  useEffect(() => { entry.attachment_ids.forEach(id => { if (!assets[id] && !assetErrors[id]) void fetchAsset(id); }); }, [entry.attachment_ids, assets, assetErrors, fetchAsset]);

  function change(patch: Partial<JournalEntry>) {
    if (operationRef.current || entry.archived) return;
    const next = { ...entry, ...patch };
    setEntry(next); storeDrafts({ ...draftsRef.current, [editorKey]: { entry: next, changed_at: new Date().toISOString() } });
  }
  function openEntry(item: JournalEntry) {
    if (locked) return;
    const local = draftsRef.current[item.id]?.entry;
    setEntry(local || item); setEditorKey(item.id); setTagsText((local || item).tags.join('，')); setConflict(!!local && local.revision !== item.revision); setView('write'); setArchived(item.archived); setMonth(item.date.slice(0, 7)); setDay('');
  }
  function openDraft(key: string) {
    if (locked) return;
    const local = draftsRef.current[key]?.entry;
    if (!local) return;
    setEntry(local); setEditorKey(key); setTagsText(local.tags.join('，')); setView('write'); setMonth(local.date.slice(0, 7)); setDay(''); setConflict(!!local.id && !!entries.find(item => item.id === local.id && item.revision !== local.revision));
  }
  async function save() {
    if (operationRef.current || entry.archived) return;
    operationRef.current = true;
    setOperation('save'); setNotice(null);
    try {
      const result = await journalAPI.save({ ...entry, title: entry.title.trim() || `${entry.date} ${entry.symbol.toUpperCase()} 复盘` });
      setEntries(previous => [result, ...previous.filter(item => item.id !== result.id)]);
      const next = { ...draftsRef.current }; delete next[editorKey]; storeDrafts(next);
      setEntry(result); setEditorKey(result.id); setConflict(false); setNotice({ text: `复盘已保存 · v${result.revision}` });
    } catch (error) {
      const message = errorMessage(error);
      setNotice({ text: `${message}。本地草稿已保留。`, error: true });
      if (/409|冲突|版本|revision|conflict/i.test(message)) setConflict(true);
    } finally { operationRef.current = false; setOperation(''); }
  }
  async function archiveEntry() {
    if (!entry.id || operationRef.current || dirty) return;
    operationRef.current = true;
    setOperation('archive');
    try {
      const result = await journalAPI.archive(entry);
      setEntries(previous => [result, ...previous.filter(item => item.id !== result.id)]);
      setEntry(result); setArchived(result.archived); setNotice({ text: result.archived ? '已归档。可在归档列表中恢复。' : '已恢复到复盘列表。' });
    } catch (error) { setNotice({ text: errorMessage(error), error: true }); }
    finally { operationRef.current = false; setOperation(''); }
  }
  async function refreshCurrent() {
    if (!entry.id || operationRef.current) return;
    operationRef.current = true;
    setOperation('refresh');
    try { const current = await journalAPI.get(entry.id); setEntries(previous => [current, ...previous.filter(item => item.id !== current.id)]); setConflict(current.revision !== entry.revision); setNotice({ text: `已读取服务器 v${current.revision}；本地编辑继续保留，可另存为新篇。` }); }
    catch (error) { setNotice({ text: errorMessage(error), error: true }); }
    finally { operationRef.current = false; setOperation(''); }
  }
  function saveCopy() {
    const next = { ...entry, id: '', revision: 0, archived: false, title: `${entry.title || '复盘'}（副本）`, created_at: '', updated_at: '' };
    const key = newDraftKey();
    storeDrafts({ ...draftsRef.current, [key]: { entry: next, changed_at: new Date().toISOString() } });
    setEditorKey(key); setEntry(next); setConflict(false); setNotice({ text: '已创建独立草稿副本，点击保存即可入库；原草稿仍保留。' });
  }
  function discardDraft() {
    const next = { ...draftsRef.current }; delete next[editorKey]; storeDrafts(next);
    if (savedEntry) { setEntry(savedEntry); setTagsText(savedEntry.tags.join('，')); setConflict(false); }
    else { const value = blankEntry({ symbol: entry.symbol, date: entry.date }); setEntry(value); setTagsText(''); setEditorKey(newDraftKey()); }
    setNotice({ text: '已撤销当前本地草稿。' });
  }
  async function upload(files: File[]) {
    if (operationRef.current || entry.archived || !files.length) return;
    const remaining = 6 - entry.attachment_ids.length;
    if (files.length > remaining) { setNotice({ text: `每篇最多 6 张截图，当前还可添加 ${remaining} 张。`, error: true }); return; }
    const invalid = files.find(file => !['image/png', 'image/jpeg', 'image/webp'].includes(file.type) || file.size > 4 * 1024 * 1024);
    if (invalid) { setNotice({ text: `${invalid.name}：仅支持 PNG / JPEG / WebP，单张不超过 4 MiB。`, error: true }); return; }
    operationRef.current = true; setOperation('upload'); setNotice(null);
    const uploaded: string[] = [];
    try {
      for (const file of files) {
        const dataUrl = await readImage(file); const asset = await journalAPI.upload(file.name || '截图.png', dataUrl);
        uploaded.push(asset.id); setAssets(previous => ({ ...previous, [asset.id]: { ...asset, data_url: dataUrl } }));
      }
      setNotice({ text: `已添加 ${uploaded.length} 张截图。保存日记后建立附件关联。` });
    } catch (error) { setNotice({ text: `${errorMessage(error)}${uploaded.length ? `；已成功添加 ${uploaded.length} 张` : ''}`, error: true }); }
    finally {
      if (uploaded.length) { const next = { ...entry, attachment_ids: [...entry.attachment_ids, ...uploaded] }; setEntry(next); storeDrafts({ ...draftsRef.current, [editorKey]: { entry: next, changed_at: new Date().toISOString() } }); }
      operationRef.current = false; setOperation('');
    }
  }
  async function exportEntry(format: 'md' | 'json') {
    if (operationRef.current) return;
    operationRef.current = true;
    setOperation('export');
    try {
      const images = await Promise.all(entry.attachment_ids.map(id => assets[id]?.data_url ? assets[id] : journalAPI.asset(id)));
      const filename = `cornwatch-${entry.date}-${entry.symbol || 'review'}`;
      if (format === 'md') downloadFile(`${filename}.md`, journalMarkdown(entry, images, dirty || !entry.id), 'text/markdown;charset=utf-8');
      else downloadFile(`${filename}.json`, JSON.stringify({ schema: 'cornwatch.journal.export.v1', source: 'user_journal', timezone: 'Asia/Shanghai', exported_at: new Date().toISOString(), includes_unsaved_changes: dirty || !entry.id, pnl_verified: false, journal: entry, attachments: images }, null, 2), 'application/json');
      setNotice({ text: format === 'md' ? 'Markdown 已导出，含来源、版本及截图索引。' : 'JSON 已导出，含来源、版本和截图原件。' });
    } catch (error) { setNotice({ text: `导出失败：${errorMessage(error)}`, error: true }); }
    finally { operationRef.current = false; setOperation(''); }
  }
  function toggleSelected(id: string) {
    setSelected(previous => {
      if (previous.includes(id)) return previous.filter(item => item !== id);
      if (previous.length >= 20) { setNotice({ text: '每次最多整理 20 篇，请先取消部分选择。', error: true }); return previous; }
      return [...previous, id];
    });
  }
  async function runReview() {
    if (reviewingRef.current || !selected.length) return;
    if (useLLM && includeImages && selectedImages > 8) { setNotice({ text: '视觉整理每次最多 8 张不重复截图、总计 8 MiB，请减少所选记录或关闭截图发送。', error: true }); return; }
    reviewingRef.current = true;
    setReviewing(true); setNotice(null);
    try {
      const result = await journalAPI.review(selected, useLLM, useLLM && includeImages && visionEnabled, useLessons);
      setReviews(previous => [result, ...previous.filter(item => item.id !== result.id)].slice(0, 20)); setLatestReview(result.id);
      setNotice({ text: `${useLLM ? '模型整理' : '规则整理'}完成。候选经验需要你核验后采纳。` });
    } catch (error) { setNotice({ text: errorMessage(error), error: true }); }
    finally { reviewingRef.current = false; setReviewing(false); }
  }
  async function acceptLesson(reviewId: string, lessonId: string) {
    if (acceptingRef.current) return;
    acceptingRef.current = true;
    setAccepting(lessonId);
    try {
      const lesson = await journalAPI.accept(reviewId, lessonId);
      setLessons(previous => [lesson, ...previous.filter(item => item.id !== lesson.id)]);
      setReviews(previous => previous.map(review => review.id === reviewId ? { ...review, lessons: review.lessons.map(item => item.id === lessonId ? { ...item, status: 'accepted' } : item) } : review));
      setNotice({ text: '经验已采纳。后续整理会引用最近采纳的经验作为个人上下文。' });
    } catch (error) { setNotice({ text: errorMessage(error), error: true }); }
    finally { acceptingRef.current = false; setAccepting(''); }
  }
  async function revokeLesson(lessonId: string) {
    if (acceptingRef.current) return;
    acceptingRef.current = true; setAccepting(lessonId);
    try {
      await journalAPI.revoke(lessonId);
      setLessons(previous => previous.filter(item => item.id !== lessonId));
      setReviews(previous => previous.map(review => ({ ...review, lessons: review.lessons.map(item => item.id === lessonId ? { ...item, status: 'candidate' } : item) })));
      setNotice({ text: '已撤回采纳，之后整理不再引用这条经验；历史输入快照保持原样，可在原整理结果中重新采纳。' });
      const refreshed = await Promise.allSettled([journalAPI.lessons(), journalAPI.reviews()]);
      if (refreshed[0].status === 'fulfilled') setLessons(refreshed[0].value || []);
      if (refreshed[1].status === 'fulfilled') setReviews(refreshed[1].value || []);
      if (refreshed.some(result => result.status === 'rejected')) setNotice({ text: '撤回已完成，历史列表刷新失败，可点击页面右上角刷新。', error: true });
    } catch (error) { setNotice({ text: errorMessage(error), error: true }); }
    finally { acceptingRef.current = false; setAccepting(''); }
  }

  const visibleEntries = entries.filter(item => item.archived === archived && (search.trim() ? `${item.title} ${item.symbol} ${item.tags.join(' ')} ${item.plan} ${item.thesis} ${item.execution} ${item.review} ${item.lessons}`.toLowerCase().includes(search.toLowerCase()) : item.date.startsWith(month) && (!day || item.date === day))).sort((a, b) => b.date.localeCompare(a.date) || b.updated_at.localeCompare(a.updated_at));
  const monthEntries = activeEntries.filter(item => item.date.startsWith(month));
  const reviewedDays = new Set(monthEntries.map(item => item.date)).size;
  const completeCount = bodyFields.filter(field => entry[field.key].trim()).length;
  const agentEntries = activeEntries.filter(item => `${item.date} ${item.title} ${item.symbol} ${item.tags.join(' ')}`.toLowerCase().includes(agentSearch.toLowerCase())).sort((a, b) => b.date.localeCompare(a.date));
  const selectedEntries = activeEntries.filter(item => selected.includes(item.id));
  const selectedImages = new Set(selectedEntries.flatMap(item => item.attachment_ids)).size;
  const localDraftItems = Object.entries(drafts).sort((a, b) => b[1].changed_at.localeCompare(a[1].changed_at));
  const currentVersionConflict = !!savedEntry && savedEntry.revision !== entry.revision;

  return <div className="journal-page">
    <div className="journal-intro"><div><span className="eyebrow">OBSERVE · RECORD · REFINE</span><h2>把每次判断，变成下一次的依据</h2><p>先记录当时的计划，再复核执行。让经验留下来源，让改进可以验证。</p></div><button className="primary-button" disabled={locked} onClick={() => createEntry({ symbol: entry.symbol, date: day || shanghaiDate() })}><Plus size={16} />新建复盘</button></div>
    <div className="journal-topline"><div className="journal-tabs" role="tablist" aria-label="复盘工作区"><button role="tab" aria-selected={view === 'write'} className={view === 'write' ? 'active' : ''} onClick={() => setView('write')}><BookOpen size={15} />复盘日记<span>{activeEntries.length}</span></button><button role="tab" aria-selected={view === 'agent'} className={view === 'agent' ? 'active' : ''} onClick={() => { if (!selected.length && entry.id && !entry.archived) setSelected([entry.id]); setView('agent'); }}><BrainCircuit size={16} />复盘智能体</button><button role="tab" aria-selected={view === 'lessons'} className={view === 'lessons' ? 'active' : ''} onClick={() => setView('lessons')}><Star size={15} />经验库<span>{lessons.length}</span></button></div><button className="icon-button" disabled={loading || locked || reviewing || !!accepting} title="刷新日记与整理历史" aria-label="刷新日记与整理历史" onClick={() => void load()}><RefreshCw size={16} className={loading ? 'spin' : ''} /></button></div>
    {notice && <div className={`journal-notice ${notice.error ? 'error' : ''}`} role={notice.error ? 'alert' : 'status'}><span>{notice.text}</span><button className="icon-button" aria-label="关闭提示" onClick={() => setNotice(null)}><X size={14} /></button></div>}
    {loadingError && <div className="journal-notice error" role="alert"><span>复盘列表暂不可用：{loadingError}。本地草稿可继续编辑。</span><button className="text-button" onClick={() => void load()}>重试</button></div>}
    {!localSafe && <div className="journal-notice error" role="alert">浏览器草稿存储不可用，请及时保存到后端或导出；离开页面可能丢失未保存内容。</div>}

    {view === 'write' && <div className="journal-layout">
      <aside className="journal-index panel">
        <Calendar month={month} entries={entries} day={day} onMonth={value => { setMonth(value); setDay(''); }} onDay={setDay} />
        <div className="journal-month-stat"><strong>{reviewedDays}<small>记录天数</small></strong><strong>{monthEntries.length}<small>本月复盘</small></strong><strong>{localDraftItems.length}<small>本地草稿</small></strong></div>
        <div className="journal-index-search"><Search size={14} /><input value={search} onChange={event => setSearch(event.target.value)} placeholder="搜索所有日期的复盘" aria-label="搜索所有日期的复盘" /></div>
        <div className="journal-list-head"><span>{search ? '搜索结果' : day || monthLabel(month)} · {visibleEntries.length} 篇</span><button className={`text-button ${archived ? 'accent' : ''}`} onClick={() => setArchived(!archived)}><Archive size={12} />{archived ? '回到日记' : '归档'}</button></div>
        {!!localDraftItems.length && <details className="journal-local-drafts" open><summary>本地草稿 · {localDraftItems.length}</summary>{localDraftItems.map(([key, draft]) => <button disabled={locked} className={editorKey === key ? 'selected' : ''} key={key} onClick={() => openDraft(key)}><span>{draft.entry.title || `${draft.entry.date} 未命名复盘`}</span><small>{draft.entry.id ? '有未保存编辑' : '尚未入库'} · {draft.entry.symbol}</small></button>)}</details>}
        <div className="journal-entry-list">{visibleEntries.map(item => <button className={`journal-entry-row ${entry.id === item.id ? 'selected' : ''}`} key={item.id} disabled={locked} onClick={() => openEntry(item)}><div><time>{item.date.slice(5).replace('-', ' / ')}</time><span>{item.symbol.toUpperCase()}</span></div><strong>{item.title || '未命名复盘'}</strong><p>{item.review || item.plan || '还没有填写复盘正文'}</p><footer><span>{item.tags.slice(0, 2).join(' · ') || '日常观察'}{item.attachment_ids.length ? ` · ${item.attachment_ids.length} 图` : ''}</span>{drafts[item.id] && <i title="有本地未保存编辑" />}</footer></button>)}{!visibleEntries.length && !loading && <div className="journal-list-empty"><BookOpen size={22} /><p>{archived ? '没有匹配的归档记录' : '这里还没有复盘记录'}</p><span>{search ? '试试合约、标签或正文关键词' : '选择日期后新建，或切换月份查看'}</span>{day && <button className="text-button" onClick={() => setDay('')}>查看整月</button>}</div>}{loading && <div className="journal-list-loading"><LoaderCircle size={16} className="spin" />正在读取记录</div>}</div>
      </aside>
      <section className="journal-editor panel" onPaste={event => { if (!active) return; const files = Array.from(event.clipboardData.items).filter(item => item.kind === 'file').map(item => item.getAsFile()).filter((file): file is File => !!file); if (files.length) { event.preventDefault(); void upload(files); } }}>
        <form ref={formRef} onSubmit={event => { event.preventDefault(); void save(); }}>
          <div className="journal-editor-toolbar"><div className={`journal-save-state ${dirty ? 'unsaved' : ''}`} aria-live="polite">{operation === 'save' ? <LoaderCircle size={14} className="spin" /> : dirty ? <span className="journal-draft-dot" /> : <Check size={14} />}<span>{operation === 'save' ? '正在保存…' : dirty ? localSafe ? '本地草稿已保留 · 待保存' : '有未保存更改' : entry.id ? `已保存 · v${entry.revision}` : '尚未开始记录'}</span></div><div className="journal-editor-actions"><button type="button" className="icon-button" title="导出 Markdown（含截图索引）" aria-label="导出 Markdown" disabled={locked} onClick={() => void exportEntry('md')}><Download size={16} /></button><button type="button" className="text-button" disabled={locked} onClick={() => void exportEntry('json')}>JSON</button>{entry.id && <button type="button" className="icon-button" title={dirty ? '请先保存或撤销编辑，再归档' : entry.archived ? '恢复日记' : '归档日记'} aria-label={entry.archived ? '恢复日记' : '归档日记'} disabled={locked || dirty} onClick={() => void archiveEntry()}>{entry.archived ? <Undo2 size={16} /> : <Archive size={16} />}</button>}<button type="submit" className="primary-button" disabled={locked || entry.archived || (!!entry.id && !dirty)}><Busy busy={operation === 'save'}><Save size={14} />保存复盘</Busy></button></div></div>
          {(conflict || currentVersionConflict) && <div className="journal-conflict" role="alert"><strong>这篇日记的保存版本已发生变化</strong><p>当前编辑基于 v{entry.revision}{savedEntry ? `，最新已保存为 v${savedEntry.revision}` : ''}。草稿继续保留；可先读取最新版本，或将当前内容另存为新篇。</p><div><button type="button" className="secondary-button" disabled={locked} onClick={() => void refreshCurrent()}>读取最新版本</button><button type="button" className="secondary-button" disabled={locked} onClick={saveCopy}>保留并另存为新篇</button></div></div>}
          {entry.archived && <div className="journal-archive-note"><Archive size={15} /><span>本篇已归档。恢复后可继续编辑。</span><button type="button" className="text-button" disabled={locked} onClick={() => void archiveEntry()}>恢复日记</button></div>}
          <fieldset disabled={locked || entry.archived}>
            <div className="journal-editor-body">
              <input className="journal-title-input" maxLength={200} value={entry.title} onChange={event => change({ title: event.target.value })} placeholder="为今天的判断写一个标题" aria-label="复盘标题" />
              <div className="journal-metadata"><label>复盘日期 · 上海<input type="date" required value={entry.date} onChange={event => { if (event.target.value) change({ date: event.target.value }); }} /></label><label>关注合约<input maxLength={24} required value={entry.symbol} onChange={event => change({ symbol: event.target.value.toLowerCase() })} placeholder="c2701" /></label><label className="journal-tags-label">标签<input maxLength={400} value={tagsText} onChange={event => { setTagsText(event.target.value); change({ tags: event.target.value.split(/[,，、]/).map(tag => tag.trim()).filter(Boolean).slice(0, 12) }); }} placeholder="趋势跟随，等待确认，天气" /></label></div>
              <div className="journal-progress"><div><span>记录完整度</span><b>{completeCount} / 5</b></div><span className="journal-progress-track"><i style={{ width: `${completeCount * 20}%` }} /></span><small>允许留空，从一条观察开始。</small></div>
              {bodyFields.map(field => <label className="journal-section-field" key={field.key}><span><b>{field.title}</b><small>{field.kicker}</small></span><textarea value={entry[field.key]} maxLength={4000} rows={field.key === 'review' ? 5 : 3} onChange={event => change({ [field.key]: event.target.value })} placeholder={field.placeholder} /></label>)}
              <div className="journal-reflection-grid"><div><span className="journal-field-label">今天的情绪</span><div className="journal-emotions">{emotions.map(emotion => <button type="button" className={entry.emotion === emotion ? 'selected' : ''} key={emotion} aria-pressed={entry.emotion === emotion} onClick={() => change({ emotion: entry.emotion === emotion ? '' : emotion })}>{emotion}</button>)}</div></div><div><span className="journal-field-label">执行纪律 <small>{entry.discipline ? `${entry.discipline} / 5` : '未评分'}</small></span><div className="journal-stars">{[1, 2, 3, 4, 5].map(value => <button type="button" key={value} aria-label={`执行纪律 ${value} 分，再次点击取消`} aria-pressed={entry.discipline === value} className={entry.discipline >= value ? 'active' : ''} onClick={() => change({ discipline: entry.discipline === value ? 0 : value })}><Star size={22} /></button>)}</div><p className="journal-field-hint">1 = 严重偏离 · 5 = 按计划执行</p></div><label>自记盈亏（元，选填）<input type="number" step="0.01" value={entry.pnl ?? ''} onChange={event => change({ pnl: event.target.value === '' ? null : Number(event.target.value) })} placeholder="留空表示未记录" /><span className="journal-field-hint">仅自记，不作为已验证交易绩效</span></label></div>
            </div>
          </fieldset>
          <div className="journal-attachment-section"><div className="journal-section-label"><h3><ImagePlus size={16} />盘面与证据截图</h3><span>{entry.attachment_ids.length} / 6 张</span></div><AssetGallery ids={entry.attachment_ids} assets={assets} errors={assetErrors} onRetry={id => void fetchAsset(id)} onRemove={id => change({ attachment_ids: entry.attachment_ids.filter(value => value !== id) })} onView={setZoom} disabled={locked || entry.archived} />{!entry.archived && entry.attachment_ids.length < 6 && <div className={`journal-dropzone ${dragging ? 'dragging' : ''}`} onDragOver={event => { event.preventDefault(); if (!locked) setDragging(true); }} onDragLeave={() => setDragging(false)} onDrop={event => { event.preventDefault(); setDragging(false); void upload(Array.from(event.dataTransfer.files)); }}><button type="button" className="secondary-button" disabled={locked} onClick={() => fileRef.current?.click()}><Busy busy={operation === 'upload'}><ImagePlus size={15} />添加截图</Busy></button><span>拖拽图片到这里，或在编辑区直接粘贴截图</span><small>PNG / JPEG / WebP · 单张 ≤ 4 MiB · 只保存在本机，发送模型需另行勾选</small><input ref={fileRef} type="file" hidden multiple accept="image/png,image/jpeg,image/webp" onChange={event => { void upload(Array.from(event.target.files || [])); event.target.value = ''; }} /></div>}</div>
          <footer className="journal-editor-footer"><span>{entry.id ? <><code title={entry.id}>{shortId(entry.id)}</code> · 更新于 {time(entry.updated_at)}</> : '正文支持纯文本与 Markdown 书写'} · Ctrl / ⌘ S 保存</span>{dirty && <details className="journal-discard"><summary>撤销草稿</summary><p>撤销本地编辑，已入库版本仍保留。</p><button type="button" className="text-button" disabled={locked} onClick={discardDraft}><Trash2 size={12} />确认撤销本地编辑</button></details>}</footer>
        </form>
      </section>
    </div>}

    {view === 'agent' && <div className="journal-agent-layout">
      <section className="panel journal-agent-setup"><div className="panel-header"><div><span className="section-kicker">PERSONAL REVIEW AGENT</span><h2><BrainCircuit size={18} />从记录到可验证的经验</h2></div></div><div className="journal-agent-body"><p className="journal-agent-description">选择已保存的复盘，寻找重复模式、核验纪律，形成下一步行动。整理引用原始记录；采纳后的经验会参与后续检索，不改变模型权重。</p><div className="journal-selection-head"><strong>选择复盘 <span>{selected.length} / 20</span></strong><button className="text-button" disabled={reviewing} onClick={() => setSelected(selected.length ? [] : agentEntries.slice(0, 20).map(item => item.id))}>{selected.length ? '清空选择' : '选最近 20 篇'}</button></div><div className="journal-index-search"><Search size={14} /><input aria-label="搜索待整理复盘" value={agentSearch} onChange={event => setAgentSearch(event.target.value)} placeholder="按日期、合约、标签筛选" /></div><div className="journal-agent-entries">{agentEntries.map(item => <label className={`journal-select-entry ${selected.includes(item.id) ? 'selected' : ''}`} key={item.id}><input type="checkbox" checked={selected.includes(item.id)} disabled={reviewing} onChange={() => toggleSelected(item.id)} /><span><strong>{item.title || `${item.date} 复盘`}</strong><small>{item.date} · {item.symbol.toUpperCase()} · v{item.revision}{drafts[item.id] ? ' · 有草稿，整理使用已保存版本' : ''}</small></span></label>)}{!agentEntries.length && <Empty title="先保存一篇复盘" detail="本地草稿保存入库后，就可以交给智能体整理。" />}</div><div className="journal-agent-settings"><label className="journal-checkbox"><input type="checkbox" checked={useLLM} disabled={!llmConfigured || reviewing} onChange={event => setUseLLM(event.target.checked)} /><span><strong>使用已配置的大模型</strong><small>{llmConfigured ? '所选复盘文字及最近采纳的经验将发送至配置的模型服务。' : '尚未配置模型。现在可以先使用本地规则整理。'}</small></span></label><label className="journal-checkbox"><input type="checkbox" checked={includeImages} disabled={!useLLM || !visionEnabled || reviewing} onChange={event => setIncludeImages(event.target.checked)} /><span><strong>同时发送所选复盘的截图 · {selectedImages} 张</strong><small>{!visionEnabled ? '需启用视觉模型支持后可用；默认不发送截图。' : '默认关闭；开启后发送原图；每次最多 8 张、总计 8 MiB。'}</small></span></label><label className="journal-checkbox"><input type="checkbox" checked={useLessons} disabled={reviewing} onChange={event => setUseLessons(event.target.checked)} /><span><strong>引用已采纳的个人经验</strong><small>默认启用，最近最多 30 条。可关闭以独立检验当前记录；旧结论始终保留原始输入。</small></span></label></div><button className="primary-button full-width" disabled={!selected.length || reviewing || loading || (useLLM && includeImages && selectedImages > 8)} onClick={() => void runReview()}><Busy busy={reviewing}><Sparkles size={16} />{reviewing ? '正在整理所选复盘…' : useLLM ? '交给 AI 整理' : '使用本地规则整理'}</Busy></button><p className="journal-agent-footnote">规则模式依据缺项、纪律与重复标签整理，不调用模型。任何模式都不替代真实成交、样本外回测与独立判断。</p></div></section>
      <div className="journal-review-history"><div className="journal-results-title"><h3>整理历史 <span>{reviews.length}</span></h3><small>保存来源版本 · 最近 20 次</small></div>{reviews.map((review, index) => <ReviewCard review={review} key={review.id} entries={entries} accepting={accepting} onAccept={(reviewId, lessonId) => void acceptLesson(reviewId, lessonId)} onOpen={openEntry} open={latestReview ? review.id === latestReview : index === 0} />)}{!reviews.length && <section className="panel journal-agent-empty"><div><BrainCircuit size={40} strokeWidth={1.3} /></div><h3>先留下证据，再沉淀经验</h3><p>选择几篇复盘，整理将返回可追溯的模式、行动清单和候选经验。原始日记与截图会完整保留。</p><ol><li><b>01</b>选择保存记录</li><li><b>02</b>整理并核对来源</li><li><b>03</b>自主采纳经验</li></ol></section>}</div>
    </div>}

    {view === 'lessons' && <section className="panel journal-library"><div className="panel-header"><div><span className="section-kicker">PERSONAL LEARNING CONTEXT</span><h2><Star size={18} />个人经验库</h2></div><span className="journal-badge">{lessons.length} 条已采纳</span></div><p className="journal-library-description">这些经验由你确认采纳。后续整理会引用最近最多 30 条作为个人上下文；它们仍需市场数据验证，可以随研究演进持续审视。</p>{lessons.map((lesson, index) => <article className="journal-library-item" key={lesson.id}><span className="journal-lesson-number">{String(index + 1).padStart(2, '0')}</span><div><p>{lesson.text}</p><SourceLinks ids={lesson.evidence_ids || []} entries={entries} onOpen={openEntry} /><small>{lesson.created_at ? time(lesson.created_at, true) : '已采纳'} · 经验 ID <code title={lesson.id}>{shortId(lesson.id)}</code>{lesson.review_id && <> · 分析 <code title={lesson.review_id}>{shortId(lesson.review_id)}</code></>}</small></div><div className="journal-lesson-actions"><CheckCheck size={16} /><button className="text-button" disabled={!!accepting} onClick={() => void revokeLesson(lesson.id)}><Busy busy={accepting === lesson.id}><Undo2 size={12} />撤回采纳</Busy></button></div></article>)}{!lessons.length && <Empty title="经验从一次认真复核开始" detail="在复盘智能体的整理结果中，核验候选经验的原始依据，再点击「采纳经验」。" />}<div className="journal-library-footer"><button className="text-button" onClick={() => setView('agent')}><ArrowLeft size={14} />回到复盘智能体</button></div></section>}
    {zoom?.data_url && <Modal title={zoom.name} onClose={() => setZoom(null)}><div className="journal-image-preview"><img src={zoom.data_url} alt={zoom.name} /><p>{zoom.width} × {zoom.height} · {number(zoom.size / 1024, 1)} KiB · <code>{shortId(zoom.id)}</code></p></div></Modal>}
  </div>;
}
