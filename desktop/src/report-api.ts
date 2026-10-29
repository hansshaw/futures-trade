import { api } from './api';

export type ReportKind = 'pre' | 'post';
export type ReportMode = 'rules' | 'llm';
export interface ReportSettings {
  revision: number;
  base_url: string;
  model: string;
  key_configured: boolean;
  configured: boolean;
  enabled: boolean;
  pre_time: string;
  post_time: string;
  auto_save: boolean;
  skip_dates: string[];
  timezone: 'Asia/Shanghai';
  window_minutes: number;
  source: 'environment' | 'saved';
}
export interface ReportSettingsInput {
  revision: number;
  base_url: string;
  model: string;
  api_key?: string;
  clear_api_key?: boolean;
  enabled: boolean;
  pre_time: string;
  post_time: string;
  auto_save: boolean;
  skip_dates: string[];
}
export interface ReportJob {
  id: string;
  kind: ReportKind;
  date: string;
  mode: ReportMode;
  trigger: 'manual' | 'schedule';
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'interrupted' | 'missed';
  stage: string;
  created_at: string;
  updated_at: string;
  report_id?: string;
  error?: string;
}
export interface ReportSummary {
  id: string;
  kind: ReportKind;
  date: string;
  symbol: string;
  mode: ReportMode;
  summary: string;
  created_at: string;
  model: string;
  journal_id?: string;
  warnings: string[];
}
export interface ReportEvidence {
  id: string;
  category: string;
  title: string;
  text: string;
  source: string;
  source_url: string;
  observed_at: string;
  available_at: string;
  is_forecast: boolean;
  unit?: string;
  note?: string;
  original_ids?: string[];
}
export interface ReportBundle {
  as_of: string;
  kind: ReportKind;
  date: string;
  symbol: string;
  evidence: ReportEvidence[];
  warnings: string[];
  current?: unknown;
  input_sha256: string;
}
export interface ReportOutput {
  mode: ReportMode;
  summary: string;
  sections: { title: string; body: string; evidence_ids: string[]; quotes: { evidence_id: string; text: string }[] }[];
  scenarios: { name: string; condition: string; invalidation: string; evidence_ids: string[] }[];
  risks: string[];
  model: string;
  prompt_version: string;
  generated_at: string;
  input_sha256: string;
}
export interface DailyReport extends ReportSummary { bundle: ReportBundle; output: ReportOutput }
export interface ReportList {
  reports: ReportSummary[];
  jobs: ReportJob[];
  busy: boolean;
  scheduler_paused: boolean;
  next_runs: { kind: ReportKind; scheduled_at: string }[];
  scheduler_note: string;
}

export const reportAPI = {
  settings: () => api<ReportSettings>('/api/report-settings'),
  saveSettings: (input: ReportSettingsInput) => api<ReportSettings>('/api/report-settings', { method: 'POST', body: input }),
  test: () => api<{ ok: boolean; message: string }>('/api/report-settings/test', { method: 'POST', body: {} }),
  list: () => api<ReportList>('/api/daily-reports'),
  read: (id: string) => api<DailyReport>(`/api/daily-reports/${encodeURIComponent(id)}`),
  generate: (kind: ReportKind, mode: ReportMode) => api<{ job: ReportJob }>('/api/daily-reports/generate', { method: 'POST', body: { kind, mode } }),
  journal: (id: string) => api<{ journal_id: string; already_saved: boolean }>(`/api/daily-reports/${encodeURIComponent(id)}/journal`, { method: 'POST', body: {} }),
};
