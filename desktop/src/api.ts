import type {LiveMarket} from './market-api';
export interface Quote { product?:string;previous_settlement?:number;change_basis?:string;received_at?:string;source_url?:string;freshness?:string;freshness_note?:string; symbol: string; name: string; last: number; previous_close: number; change_pct: number; volume: number; open_interest: number; observed_at: string; source: string; is_demo: boolean }
export interface Analysis { summary: string; direction: 'bullish' | 'bearish' | 'mixed' | 'unknown'; confidence: number; reasoning: string[]; evidence: string[]; risk_flags: string[]; model: string; analyzed_at: string }
export interface MarketEvent { id: string; title: string; body: string; category: string; region: string; products: string[]; source: string; source_url: string; published_at: string; ingested_at: string; available_at: string; severity: 'low' | 'medium' | 'high'; is_demo: boolean; analysis?: Analysis }
export interface Observation { id: string; kind: string; region: string; entity: string; value: number; unit: string; observed_at: string; available_at: string; source: string; source_url: string; is_demo: boolean; note: string }
export interface MainDecision { symbol: string; previous_symbol: string; reason: string; as_of: string; pending_symbol: string; confirmation_days: number; required_days: number; is_demo: boolean }
export interface SourceStatus { id: string; name: string; kind: string; status: string; last_success?: string; message: string }
export interface Dashboard { as_of: string; mode: 'demo' | 'imported' | 'empty' | 'public_quotes' | 'authorized_quotes'; market_live?:LiveMarket|null; quotes: Quote[]; events: MarketEvent[]; observations: Observation[]; main_contract: MainDecision; sources: SourceStatus[]; bar_count: number; event_count: number; llm_configured: boolean }
export interface Bar { symbol: string; timestamp: string; trade_date: string; open: number; high: number; low: number; close: number; settlement: number; volume: number; open_interest: number; limit_up: number; limit_down: number; is_demo: boolean }
export interface BacktestConfig { symbol: string; demo: boolean; fast_window: number; slow_window: number; initial_capital: number; lots: number; fee_per_lot: number; slippage_ticks: number; margin_rate: number; max_drawdown_pct: number }
export interface Backtest { id: string; created_at: string; symbol: string; is_demo: boolean; config: BacktestConfig; metrics: { initial_capital: number; final_equity: number; total_return_pct: number; max_drawdown_pct: number; total_fees: number; total_slippage: number; trade_count: number; win_rate_pct: number; sharpe_ratio?: number; open_position?: number; unrealized_pnl?: number }; equity_curve: { timestamp: string; equity: number; drawdown_pct: number; position: number }[]; trades: { timestamp: string; symbol: string; side: string; offset: string; price: number; lots: number; fee: number; slippage?: number; reason: string; pnl: number }[]; warnings: string[] }
export interface Settings { data_sources_available?:boolean; llm: { configured: boolean; base_url: string; model: string }; journal?: {available:boolean;vision_enabled:boolean}; feed_count: number; weather_enabled: boolean; storage: string; live_trading: boolean }
export interface RequestOptions { method?: 'GET' | 'POST'; body?: unknown }
declare global { interface Window { cornwatch?: { readJournalDrafts:()=>string|null; writeJournalDrafts:(value:string)=>boolean; captureChart: (rect: {x:number;y:number;width:number;height:number}) => Promise<string>; request: (path: string, options?: RequestOptions) => Promise<unknown>; onUpdate: (callback: (update: { kind: string; at: string }) => void) => () => void } } }
export async function api<T>(path: string, options?: RequestOptions): Promise<T> {
  if (window.cornwatch) {
    try { return await window.cornwatch.request(path, options) as T; }
    catch (error) { throw new Error(error instanceof Error ? error.message.replace(/^Error invoking remote method 'cornwatch:request': Error: /, '') : '请求失败'); }
  }
  const response = await fetch(path, { method: options?.method ?? 'GET', headers: { 'Content-Type': 'application/json' }, body: options?.body !== undefined ? JSON.stringify(options.body) : undefined, signal: AbortSignal.timeout(120000) });
  const data = await response.json();
  if (!response.ok) throw new Error(`[HTTP ${response.status}] ${data.error || '请求失败'}`);
  return data as T;
}
