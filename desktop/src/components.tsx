import { useId, useMemo, useState, type ReactNode } from 'react';
import { X, CircleHelp, LoaderCircle } from 'lucide-react';

export const number = (value: number | undefined, digits = 0) => value == null || !Number.isFinite(value) ? '—' : value.toLocaleString('zh-CN', { maximumFractionDigits: digits, minimumFractionDigits: digits });
export const time = (value?: string, full = false) => !value ? '—' : new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', ...(full ? { year: 'numeric' } : {}), hour: '2-digit', minute: '2-digit', hour12: false });
export const localNow = () => { const date = new Date(); return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16); };
export function Badge({ demo }: { demo: boolean }) { return <span className={`badge ${demo ? 'demo' : 'real'}`}>{demo ? '演示' : '已入库'}</span>; }
export function Empty({ title, detail }: { title: string; detail?: string }) { return <div className="empty"><CircleHelp size={26} /><strong>{title}</strong>{detail && <p>{detail}</p>}</div>; }
export function Busy({ children, busy }: { children: ReactNode; busy: boolean }) { return <>{busy && <LoaderCircle size={15} className="spin" />}{children}</>; }
export function Modal({ title, children, onClose }: { title: string; children: ReactNode; onClose: () => void }) {
  return <div className="modal-backdrop" onClick={onClose}><section className="modal" role="dialog" aria-modal="true" aria-label={title} onClick={event => event.stopPropagation()}><header><h2>{title}</h2><button className="icon-button" aria-label="关闭弹窗" onClick={onClose}><X size={20} /></button></header>{children}</section></div>;
}

interface Point { label: string; value: number; volume?: number }
export function LineChart({ points: originalPoints, unit = '元 / 吨', color = '#4edbc3', height = 255 }: { points: Point[]; unit?: string; color?: string; height?: number }) {
  const gradient = useId().replace(/:/g, '');
  const [hover, setHover] = useState<number | null>(null);
  const points = useMemo(() => {
    if (originalPoints.length <= 902) return originalPoints;
    const sampled = [originalPoints[0]];
    const bucket = Math.ceil((originalPoints.length - 2) / 450);
    for (let start = 1; start < originalPoints.length - 1; start += bucket) {
      const end = Math.min(start + bucket, originalPoints.length - 1);
      let low = start; let high = start;
      for (let index = start + 1; index < end; index++) {
        if (originalPoints[index].value < originalPoints[low].value) low = index;
        if (originalPoints[index].value > originalPoints[high].value) high = index;
      }
      sampled.push(originalPoints[Math.min(low, high)]);
      if (low !== high) sampled.push(originalPoints[Math.max(low, high)]);
    }
    sampled.push(originalPoints[originalPoints.length - 1]);
    return sampled;
  }, [originalPoints]);
  if (!points.length) return <Empty title="暂无可绘制数据" detail="选择已有数据的合约，或在「数据与设置」中导入日线。" />;
  const width = 880; const left = 70; const right = 18; const top = 30; const bottom = 36;
  const [min, max] = points.reduce(([low, high], point) => [Math.min(low, point.value), Math.max(high, point.value)], [Infinity, -Infinity]);
  const spread = max - min || Math.abs(max) * .02 || 1; const low = min - spread * .12; const high = max + spread * .12;
  const plotWidth = width - left - right; const plotHeight = height - top - bottom;
  const x = (i: number) => left + i / Math.max(points.length - 1, 1) * plotWidth;
  const y = (value: number) => top + (high - value) / (high - low) * plotHeight;
  const line = points.map((point, i) => `${i ? 'L' : 'M'}${x(i).toFixed(2)},${y(point.value).toFixed(2)}`).join(' ');
  const selected = hover === null ? points.length - 1 : Math.max(0, Math.min(points.length - 1, hover));
  const ticks = [...new Set([0, Math.floor((points.length - 1) / 3), Math.floor((points.length - 1) * 2 / 3), points.length - 1])];
  return <div className="chart-wrap"><div className="chart-legend"><span><i style={{ background: color }} />{unit}</span><span>{points[selected].label} <b style={{ color }}>{number(points[selected].value, 2)}</b></span></div><svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${unit}走势，共 ${points.length} 个数据点`} onMouseLeave={() => setHover(null)} onMouseMove={event => { const rect = event.currentTarget.getBoundingClientRect(); const cursor = (event.clientX - rect.left) / rect.width * width; setHover(Math.max(0, Math.min(points.length - 1, Math.round((cursor - left) / plotWidth * (points.length - 1))))); }}>
    <defs><linearGradient id={gradient} x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor={color} stopOpacity=".23" /><stop offset="100%" stopColor={color} stopOpacity="0" /></linearGradient></defs>
    {[0, 1, 2, 3, 4].map(index => { const value = high - (high - low) * index / 4; return <g key={index}><line x1={left} x2={width - right} y1={y(value)} y2={y(value)} className="chart-grid" /><text x={left - 14} y={y(value) + 4} textAnchor="end" className="chart-label">{number(value, Math.abs(value) < 100 ? 1 : 0)}</text></g>; })}
    <path d={`${line} L${x(points.length - 1)},${height - bottom} L${left},${height - bottom} Z`} fill={`url(#${gradient})`} />
    <path d={line} fill="none" stroke={color} strokeWidth="2.3" strokeLinejoin="round" />
    {ticks.map(index => <text key={index} x={x(index)} y={height - 8} textAnchor={index === 0 ? 'start' : index === points.length - 1 ? 'end' : 'middle'} className="chart-label">{/^\d{4}-\d{2}-\d{2}/.test(points[index].label) ? points[index].label.slice(5, 10) : points[index].label}</text>)}
    {hover !== null && <g><line x1={x(selected)} x2={x(selected)} y1={top} y2={height - bottom} stroke="#6c8092" strokeDasharray="4 4" /><circle cx={x(selected)} cy={y(points[selected].value)} r="4" fill={color} stroke="#0c131c" strokeWidth="2" /></g>}
  </svg></div>;
}
