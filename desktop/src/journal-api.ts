import { api } from './api';

export interface JournalEntry {
  id: string; date: string; title: string; symbol: string; tags: string[];
  plan: string; thesis: string; execution: string; review: string; lessons: string;
  emotion: string; discipline: number; pnl: number | null; attachment_ids: string[];
  created_at: string; updated_at: string; revision: number; archived: boolean;
}
export interface JournalAsset {
  id: string; name: string; mime: string; size: number; width: number; height: number;
  created_at?: string; data_url?: string;
}
export interface JournalLesson {
  id: string; text: string; evidence_ids: string[]; status: 'candidate' | 'accepted';
  review_id?: string; created_at?: string;
}
export interface JournalReview {
  id: string; created_at: string; model: string; journal_ids: string[];
  input_revisions: Record<string, number>; summary: string;
  patterns: { title: string; observation: string; journal_ids: string[] }[];
  actions: string[]; questions: string[]; risk_flags: string[];
  image_count: number; lessons: JournalLesson[]; input_sha256: string;
  context_lesson_ids?: string[];
}
export interface JournalSnapshot { journals: JournalEntry[]; accepted_lessons: JournalLesson[]; include_images: boolean; image_sha256: Record<string, string> }
export interface JournalSeed { symbol: string; date?: string; title?: string; plan?: string; thesis?: string; attachment_ids?: string[] }
export interface LocalDraft { entry: JournalEntry; changed_at: string }
export type Drafts = Record<string, LocalDraft>;

export const journalAPI = {
  list: (archived = false) => api<JournalEntry[]>(`/api/journals${archived ? '?archived=true' : ''}`),
  get: (id: string) => api<JournalEntry>(`/api/journals/${encodeURIComponent(id)}`),
  save: (entry: JournalEntry) => api<JournalEntry>(entry.id ? `/api/journals/${encodeURIComponent(entry.id)}` : '/api/journals', { method: 'POST', body: { date: entry.date, title: entry.title, symbol: entry.symbol, tags: entry.tags, plan: entry.plan, thesis: entry.thesis, execution: entry.execution, review: entry.review, lessons: entry.lessons, emotion: entry.emotion, discipline: entry.discipline, pnl: entry.pnl, attachment_ids: entry.attachment_ids, ...(entry.id ? { revision: entry.revision } : {}) } }),
  archive: (entry: JournalEntry) => api<JournalEntry>(`/api/journals/${encodeURIComponent(entry.id)}/archive`, { method: 'POST', body: { archived: !entry.archived, revision: entry.revision } }),
  upload: (name: string, data_url: string) => api<JournalAsset>('/api/journal-assets', { method: 'POST', body: { name, data_url } }),
  asset: (id: string) => api<JournalAsset>(`/api/journal-assets/${encodeURIComponent(id)}`),
  reviews: () => api<JournalReview[]>('/api/journal-reviews'),
  snapshot: (id: string) => api<JournalSnapshot>(`/api/journal-reviews/${encodeURIComponent(id)}/snapshot`),
  review: (journal_ids: string[], use_llm: boolean, include_images: boolean, use_accepted_lessons = true) => api<JournalReview>('/api/journal-reviews', { method: 'POST', body: { journal_ids, use_llm, include_images, use_accepted_lessons } }),
  lessons: () => api<JournalLesson[]>('/api/journal-lessons'),
  accept: (reviewId: string, lessonId: string) => api<JournalLesson>(`/api/journal-reviews/${encodeURIComponent(reviewId)}/lessons/${encodeURIComponent(lessonId)}/accept`, { method: 'POST', body: {} }),
  revoke: (lessonId: string) => api<JournalLesson>(`/api/journal-lessons/${encodeURIComponent(lessonId)}/revoke`, { method: 'POST', body: {} }),
};

export function shanghaiDate(): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date());
}
export function blankEntry(seed?: JournalSeed): JournalEntry {
  return { id: '', date: seed?.date || shanghaiDate(), title: seed?.title || '', symbol: seed?.symbol || 'c2701', tags: [], plan: seed?.plan || '', thesis: seed?.thesis || '', execution: '', review: '', lessons: '', emotion: '', discipline: 0, pnl: null, attachment_ids: seed?.attachment_ids?.slice(0, 6) || [], created_at: '', updated_at: '', revision: 0, archived: false };
}

const draftsKey = 'cornwatch.journal.drafts.v1';
function validEntry(value: unknown): value is JournalEntry {
  if (!value || typeof value !== 'object') return false;
  const item = value as Record<string, unknown>;
  if (!['id', 'date', 'title', 'symbol', 'plan', 'thesis', 'execution', 'review', 'lessons', 'emotion', 'created_at', 'updated_at'].every(key => typeof item[key] === 'string')) return false;
  if (!/^\d{4}-\d{2}-\d{2}$/.test(String(item.date))) return false;
  const parsed = new Date(`${item.date}T00:00:00Z`);
  if (!Number.isFinite(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== item.date) return false;
  if (!Array.isArray(item.tags) || !item.tags.every(tag => typeof tag === 'string') || !Array.isArray(item.attachment_ids) || item.attachment_ids.length > 6 || !item.attachment_ids.every(id => typeof id === 'string')) return false;
  return typeof item.revision === 'number' && Number.isSafeInteger(item.revision) && item.revision >= 0 && typeof item.archived === 'boolean' && typeof item.discipline === 'number' && Number.isInteger(item.discipline) && item.discipline >= 0 && item.discipline <= 5 && (item.pnl === null || typeof item.pnl === 'number' && Number.isFinite(item.pnl));
}
interface DraftBridge { readJournalDrafts?: () => string | null; writeJournalDrafts?: (value: string) => boolean }
export function readDrafts(): Drafts {
  try {
    const bridge = window.cornwatch as DraftBridge | undefined;
    const text = bridge?.readJournalDrafts ? bridge.readJournalDrafts() : localStorage.getItem(draftsKey);
    const raw: unknown = JSON.parse(text || '{}');
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {};
    return Object.fromEntries(Object.entries(raw).filter(([key, value]) => key !== '__proto__' && key !== 'constructor' && value && typeof value === 'object' && typeof value.changed_at === 'string' && Number.isFinite(Date.parse(value.changed_at)) && validEntry(value.entry)));
  } catch { return {}; }
}
export function writeDrafts(drafts: Drafts): boolean {
  try {
    const text = JSON.stringify(drafts);
    const bridge = window.cornwatch as DraftBridge | undefined;
    if (bridge?.writeJournalDrafts) return bridge.writeJournalDrafts(text);
    localStorage.setItem(draftsKey, text); return true;
  }
  catch { return false; }
}
export const newDraftKey = () => `local-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
export function readImage(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => typeof reader.result === 'string' ? resolve(reader.result) : reject(new Error('图片读取失败'));
    reader.onerror = () => reject(new Error('图片读取失败'));
    reader.readAsDataURL(file);
  });
}
export function downloadFile(name: string, content: string, type: string): void {
  const link = document.createElement('a');
  const url = URL.createObjectURL(new Blob([content], { type }));
  link.href = url; link.download = name; link.click();
  setTimeout(() => URL.revokeObjectURL(url), 5000);
}
export function journalMarkdown(entry: JournalEntry, assets: JournalAsset[], isDraft: boolean): string {
  const fields: [string, string][] = [['盘前计划', entry.plan], ['核心假设与失效条件', entry.thesis], ['执行记录', entry.execution], ['盘后复盘', entry.review], ['经验与下一步', entry.lessons]];
  return [
    `# ${entry.title || `${entry.date} 交易复盘`}`, '',
    `日期：${entry.date}（Asia/Shanghai）  \n合约：${entry.symbol}  \n标签：${entry.tags.join('、') || '未填写'}`,
    `情绪：${entry.emotion || '未填写'}  \n纪律：${entry.discipline ? `${entry.discipline} / 5` : '未评分'}  \n自记盈亏：${entry.pnl == null ? '未填写' : `${entry.pnl} 元`}`,
    ...fields.flatMap(([heading, body]) => ['', `## ${heading}`, '', body || '（未填写）']),
    '', '## 截图', '',
    ...assets.map(asset => `- ${asset.name} · ${asset.width} × ${asset.height} · 本地附件 ID：${asset.id}`),
    '', '## 来源与版本', '',
    `来源：CornWatch 用户手工复盘；盈亏未与成交账单对账。`,
    `记录 ID：${entry.id || '尚未入库'}  \n版本：${entry.revision || 0}${isDraft ? '（包含本地未保存编辑）' : ''}  \n创建时间：${entry.created_at || '未入库'}  \n更新时间：${entry.updated_at || '未入库'}  \n导出时间：${new Date().toISOString()}`,
    '截图原件保存在本机附件库；此 Markdown 仅列出附件索引，JSON 导出包含截图原件。', '',
  ].join('\n');
}
