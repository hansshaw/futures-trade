export interface IndicatorRow { close: number }
export interface Bollinger { mid: number; upper: number; lower: number }
// Full-window indicators. Missing warm-up stays null; no future values or zero fills.
export function movingAverage(rows: readonly IndicatorRow[], period: number): (number | null)[] {
  if (!Number.isInteger(period) || period < 2 || period > 250) throw new Error('MA周期需为2至250的整数');
  return rows.map((_, index) => {
    if (index < period - 1) return null;
    const values = rows.slice(index - period + 1, index + 1).map(row => row.close);
    return values.every(Number.isFinite) ? values.reduce((sum, value) => sum + value, 0) / period : null;
  });
}
export function bollingerBands(rows: readonly IndicatorRow[], period: number, multiplier: number): (Bollinger | null)[] {
  if (!Number.isInteger(period) || period < 2 || period > 250 || !Number.isFinite(multiplier) || multiplier <= 0 || multiplier > 5) throw new Error('BOLL参数无效');
  const averages = movingAverage(rows, period);
  return averages.map((mid, index) => {
    if (mid === null) return null;
    // Population standard deviation (divide by N), computed around the mean to
    // avoid catastrophic cancellation for large prices with small volatility.
    const variance = rows.slice(index - period + 1, index + 1).reduce((sum, row) => sum + (row.close - mid) ** 2, 0) / period;
    const deviation = Math.sqrt(variance) * multiplier;
    return { mid, upper: mid + deviation, lower: mid - deviation };
  });
}
export function parsePeriods(value: string): number[] | null {
  const pieces = value.split(/[,，\s]+/).filter(Boolean);
  if (!pieces.length || pieces.length > 4) return null;
  const periods = pieces.map(Number);
  return periods.every(value => Number.isInteger(value) && value >= 2 && value <= 250) ? [...new Set(periods)] : null;
}
