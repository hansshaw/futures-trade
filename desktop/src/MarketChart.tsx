import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ArrowLeft, ArrowRight, Download, RefreshCw, RotateCcw, Settings2, ZoomIn, ZoomOut } from 'lucide-react';
import { api } from './api';
import { Busy, Empty, number, time } from './components';
import { bollingerBands, movingAverage, parsePeriods } from './chart-indicators';
import { quoteTime, type ChartInterval, type HistoryBar, type MarketHistory } from './market-api';

const intervals: [ChartInterval,string][] = [['1m','1分'],['5m','5分'],['15m','15分'],['30m','30分'],['60m','60分'],['day','日K']];
const colors=['#e4c06d','#9cb9f5','#ce98da','#69c9b6'];
const emptySamples: {observed_at:string;last:number}[]=[];
function shortTime(value:string, daily:boolean) { return new Date(value).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',month:'2-digit',day:'2-digit',...(daily?{}:{hour:'2-digit',minute:'2-digit',hour12:false})}); }
function dateOf(value:string) { return new Intl.DateTimeFormat('en-CA',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date(value)); }
function safeURL(value?:string) { try { const u=new URL(value||'');return ['https:','http:'].includes(u.protocol)&&!u.username&&!u.password?u.href:undefined; }catch{return undefined} }

export default function MarketChart({symbol,trail=emptySamples}:{symbol:string;trail?:{observed_at:string;last:number}[]}) {
 const [interval,setIntervalValue]=useState<ChartInterval>('day'),[source,setSource]=useState<'public'|'imported'|'samples'>('public');
 const [history,setHistory]=useState<MarketHistory|null>(null),[loading,setLoading]=useState(false),[error,setError]=useState('');
 const [maEnabled,setMAEnabled]=useState(true),[bollEnabled,setBollEnabled]=useState(true),[settings,setSettings]=useState(false);
 const [periods,setPeriods]=useState([5,10,20,60]),[maDraft,setMADraft]=useState('5,10,20,60'),[bollPeriod,setBollPeriod]=useState(20),[bollMultiple,setBollMultiple]=useState(2);
 const [bollDraft,setBollDraft]=useState('20'),[multipleDraft,setMultipleDraft]=useState('2'),[parameterError,setParameterError]=useState('');
 const [from,setFrom]=useState(''),[to,setTo]=useState(''),[span,setSpan]=useState(90),[back,setBack]=useState(0),[hover,setHover]=useState<number|null>(null);
 const [table,setTable]=useState(false);
 const requestID=useRef(0), drag=useRef<{x:number;back:number}|null>(null), plot=useRef<SVGSVGElement|null>(null), loadBusy=useRef(false);
 const samples=source==='samples';
 const load=useCallback(async()=>{
  if(source==='samples'||loadBusy.current)return;
  const request=++requestID.current;
  loadBusy.current=true;setLoading(true);
  try {
   const result=await api<MarketHistory>(`/api/market/history?symbol=${encodeURIComponent(symbol)}&interval=${source==='imported'?'day':interval}&limit=6000&source=${source}`);
   if(request===requestID.current){setHistory(result);setError('')}
  }catch(e){if(request===requestID.current)setError(e instanceof Error?e.message:'历史行情读取失败')}
  finally{if(request===requestID.current){setLoading(false);loadBusy.current=false}}
 },[symbol,source,interval]);
 useEffect(()=>{
  ++requestID.current;loadBusy.current=false;setLoading(false);setHistory(null);setError('');setBack(0);setHover(null);setFrom('');setTo('');
  void load();
  const timer=source==='public'?window.setInterval(()=>void load(),60000):undefined;
  return()=>{++requestID.current;loadBusy.current=false;if(timer)clearInterval(timer)};
 },[load,source]);
 const bars=useMemo<HistoryBar[]>(()=>samples?trail.map(p=>({symbol,timestamp:p.observed_at,trade_date:'',open:p.last,high:p.last,low:p.last,close:p.last,volume:0,open_interest:0,is_demo:false})):history?.bars||[],[samples,trail,symbol,history]);
 const averages=useMemo(()=>periods.map(p=>movingAverage(bars,p)),[bars,periods]);
 const bands=useMemo(()=>bollingerBands(bars,bollPeriod,bollMultiple),[bars,bollPeriod,bollMultiple]);
 const filtered=useMemo(()=>bars.map((bar,index)=>({bar,index})).filter(({bar})=>{const d=bar.trade_date||dateOf(bar.timestamp);return(!from||d>=from)&&(!to||d<=to)}),[bars,from,to]);
 const effectiveBack=Math.min(back,Math.max(0,filtered.length-1));
 const end=Math.max(1,filtered.length-effectiveBack),start=Math.max(0,end-span),visible=filtered.slice(start,end);
 const active=visible[hover!==null?Math.min(hover,visible.length-1):visible.length-1];
 useEffect(()=>{
  const element=plot.current;if(!element)return;
  const wheel=(event:WheelEvent)=>{event.preventDefault();setSpan(value=>Math.max(15,Math.min(1000,Math.round(value*(event.deltaY>0?1.15:.85)))));setHover(null)};
  element.addEventListener('wheel',wheel,{passive:false});return()=>element.removeEventListener('wheel',wheel);
 },[visible.length]);
 const w=1000,h=405,left=58,right=76,top=20,priceBottom=278,volumeTop=304,volumeBottom=352,plotWidth=w-left-right;
 const ys=visible.flatMap(({bar,index})=>[bar.low,bar.high,...(maEnabled?averages.map(values=>values[index]).filter((v):v is number=>v!==null):[]),...(bollEnabled&&bands[index]?[bands[index]!.upper,bands[index]!.lower]:[])]);
 const low=ys.length?Math.min(...ys):0,high=ys.length?Math.max(...ys):1,padding=Math.max((high-low)*.08,.5),min=low-padding,max=high+padding;
 const px=(i:number)=>left+(i+.5)*plotWidth/Math.max(visible.length,1),py=(price:number)=>priceBottom-(price-min)/(max-min)*(priceBottom-top);
 const maxVolume=Math.max(1,...visible.map(({bar})=>bar.volume));
 const path=(values:(number|null)[])=>{let open=false;return visible.map(({index},i)=>{const value=values[index];if(value===null||value===undefined){open=false;return''}const text=`${open?'L':'M'}${px(i).toFixed(2)},${py(value).toFixed(2)}`;open=true;return text}).join(' ')};
 const zoom=(factor:number)=>{setSpan(value=>Math.max(15,Math.min(1000,Math.round(value*factor))));setHover(null)};
 const pan=(count:number)=>{setBack(value=>Math.max(0,Math.min(Math.max(0,filtered.length-1),value+count)));setHover(null)};
 const reset=()=>{setSpan(90);setBack(0);setFrom('');setTo('');setHover(null)};
 const applyParameters=()=>{
  const parsed=parsePeriods(maDraft),period=Number(bollDraft),multiple=Number(multipleDraft);
  if(!parsed||!Number.isInteger(period)||period<2||period>250||!Number.isFinite(multiple)||multiple<=0||multiple>5){setParameterError('MA最多4条，周期2—250；BOLL周期2—250，倍数大于0且不超过5。');return}
  setPeriods(parsed);setBollPeriod(period);setBollMultiple(multiple);setParameterError('');setSettings(false);
 };
 const exportCSV=()=>{
  const csv=(samples?['合约,源报价时间(含时区),采样价格',...filtered.map(({bar})=>[symbol,bar.timestamp,bar.close].join(','))]:['合约,时间(含时区),原始交易日,开盘,最高,最低,收盘,成交量,持仓量',...filtered.map(({bar})=>[symbol,bar.timestamp,bar.trade_date,bar.open,bar.high,bar.low,bar.close,bar.volume,history?.has_open_interest===false?'':bar.open_interest].join(','))]).join('\n');
  const url=URL.createObjectURL(new Blob(['\uFEFF'+csv],{type:'text/csv;charset=utf-8'}));const link=document.createElement('a');link.href=url;link.download=`${symbol}-${samples?'采样':interval}-${dateOf(new Date().toISOString())}.csv`;link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
 };
 const daily=source==='imported'||interval==='day';
 const sourceURL=safeURL(history?.source_url);
 return <div className="market-chart-workspace" data-testid="market-chart">
  <div className="mc-toolbar"><div className="mc-tabs" role="group" aria-label="行情图数据来源"><button className={source==='public'?'active':''} onClick={()=>setSource('public')}>公开历史K线</button><button className={source==='samples'?'active':''} onClick={()=>setSource('samples')}>实时采样</button><button className={source==='imported'?'active':''} onClick={()=>{setSource('imported');setIntervalValue('day')}}>本地导入日线</button></div><button className="text-button" disabled={loading||samples} onClick={()=>void load()}><Busy busy={loading}><RefreshCw size={13}/>刷新历史</Busy></button></div>
  <div className="mc-toolbar"><div className="mc-periods" role="group" aria-label="K线周期">{intervals.map(([value,label])=><button key={value} disabled={samples||source==='imported'&&value!=='day'} className={!samples&&interval===value?'active':''} onClick={()=>setIntervalValue(value)}>{label}</button>)}</div><div className="mc-indicator-toggles"><label><input type="checkbox" checked={maEnabled} onChange={e=>setMAEnabled(e.target.checked)}/>MA</label><label><input type="checkbox" checked={bollEnabled} onChange={e=>setBollEnabled(e.target.checked)}/>BOLL</label><button className="text-button" aria-expanded={settings} onClick={()=>setSettings(!settings)}><Settings2 size={13}/>指标参数</button></div></div>
  {settings&&<div className="mc-parameters"><label>MA周期（最多4条）<input aria-label="MA周期" value={maDraft} onChange={e=>setMADraft(e.target.value)}/></label><label>BOLL周期<input type="number" min={2} max={250} aria-label="BOLL周期" value={bollDraft} onChange={e=>setBollDraft(e.target.value)}/></label><label>标准差倍数<input type="number" min={.1} max={5} step="any" aria-label="BOLL标准差倍数" value={multipleDraft} onChange={e=>setMultipleDraft(e.target.value)}/></label><button className="secondary-button" onClick={applyParameters}>应用指标</button>{parameterError&&<p role="alert">{parameterError}</p>}</div>}
  <div className="mc-range-controls"><label>起始日期<input aria-label="历史起始日期" type="date" value={from} onChange={e=>{setFrom(e.target.value);setBack(0)}}/></label><span>—</span><label>结束日期<input aria-label="历史结束日期" type="date" min={from||undefined} value={to} onChange={e=>{setTo(e.target.value);setBack(0)}}/></label><button className="text-button" onClick={reset}><RotateCcw size={13}/>最近 / 重置</button><div className="mc-zoom"><button className="icon-button" title="查看更早" aria-label="查看更早行情" disabled={start===0} onClick={()=>pan(Math.max(5,Math.round(span/3)))}><ArrowLeft size={15}/></button><button className="icon-button" title="查看更新" aria-label="查看更新行情" disabled={effectiveBack===0} onClick={()=>pan(-Math.max(5,Math.round(span/3)))}><ArrowRight size={15}/></button><button className="icon-button" aria-label="放大K线" onClick={()=>zoom(.7)}><ZoomIn size={15}/></button><button className="icon-button" aria-label="缩小K线" onClick={()=>zoom(1.4)}><ZoomOut size={15}/></button></div></div>
  {from&&to&&from>to&&<div className="mc-warning" role="alert">起始日期不能晚于结束日期，请调整日期范围。</div>}
  {(error||history?.last_error)&&<div className="mc-warning" role="alert">{error||history?.last_error}{history?.bars.length?'；保留上一份历史快照，请核对时点。':''}</div>}
  {!visible.length?<Empty title={loading?'正在读取历史行情…':from||to?'此日期范围内没有数据':'暂无可用历史'} detail={samples?'仅积累实际接收到的报价；不会补造之前的价格。':source==='imported'?'请在数据与设置中导入该实际合约的真实日线CSV。':'来源返回的可用窗口有限；可切换周期、合约或清除日期筛选。'}/>:<>
   <div className="mc-ohlc" aria-live="off"><strong>{samples?quoteTime(active?.bar.timestamp):time(active?.bar.timestamp,true)}</strong>{active&&<>{!samples&&<><span>开 <b>{number(active.bar.open,2)}</b></span><span>高 <b>{number(active.bar.high,2)}</b></span><span>低 <b>{number(active.bar.low,2)}</b></span></>}<span>{samples?'采样价':'收'} <b className={active.bar.close>=active.bar.open?'price-up':'price-down'}>{number(active.bar.close,2)}</b></span>{!samples&&<><span>量 <b>{number(active.bar.volume)}</b></span><span>持仓 <b>{history?.has_open_interest===false?'未提供':number(active.bar.open_interest)}</b></span></>}</>}</div>
   <div className="mc-indicator-legend">{maEnabled&&periods.map((p,i)=><span key={p} style={{color:colors[i]}}>MA{p} {active?number(averages[i][active.index]??undefined,2):'—'}</span>)}{bollEnabled&&<><span className="mc-boll-mid">BOLL({bollPeriod},{bollMultiple}) 中 {active?number(bands[active.index]?.mid,2):'—'}</span><span className="mc-boll">上 {active?number(bands[active.index]?.upper,2):'—'} / 下 {active?number(bands[active.index]?.lower,2):'—'}</span></>}</div>
   <svg ref={plot} className="mc-plot" viewBox={`0 0 ${w} ${h}`} role="img" aria-label={`${symbol.toUpperCase()} ${samples?'实际报价采样':daily?'日线':'分钟线'}与MA BOLL指标图`} onPointerDown={e=>{if(e.button!==0)return;drag.current={x:e.clientX,back};e.currentTarget.setPointerCapture(e.pointerId)}} onPointerUp={e=>{drag.current=null;if(e.currentTarget.hasPointerCapture(e.pointerId))e.currentTarget.releasePointerCapture(e.pointerId)}} onPointerCancel={()=>{drag.current=null}} onPointerLeave={()=>{if(!drag.current)setHover(null)}} onPointerMove={e=>{const rect=e.currentTarget.getBoundingClientRect();const x=(e.clientX-rect.left)*w/rect.width;if(drag.current){setBack(Math.max(0,Math.min(Math.max(0,filtered.length-1),drag.current.back+Math.round((e.clientX-drag.current.x)/rect.width*w/plotWidth*visible.length))));setHover(null)}else setHover(Math.max(0,Math.min(visible.length-1,Math.floor((x-left)/plotWidth*visible.length))))}}>
    {[0,1,2,3,4].map(i=>{const y=top+i*(priceBottom-top)/4;return <g key={i}><line x1={left} x2={w-right} y1={y} y2={y} className="mc-grid"/><text x={w-right+10} y={y+4}>{number(max-i*(max-min)/4,1)}</text></g>})}
    {bollEnabled&&visible.some(({index})=>bands[index])&&<path className="mc-boll-fill" d={`${visible.filter(({index})=>bands[index]).map(({index},i,all)=>`${i===0?'M':'L'}${px(visible.indexOf(all[i]))},${py(bands[index]!.upper)}`).join(' ')} ${visible.filter(({index})=>bands[index]).slice().reverse().map(item=>`L${px(visible.indexOf(item))},${py(bands[item.index]!.lower)}`).join(' ')} Z`}/>}
    {samples?<path d={path(bars.map(b=>b.close))} className="mc-sample-line"/>:visible.map(({bar},i)=>{const color=bar.close>=bar.open?'#e18685':'#67c6ab';const width=Math.max(1,Math.min(12,plotWidth/visible.length*.64));return <g key={bar.timestamp}><line x1={px(i)} x2={px(i)} y1={py(bar.high)} y2={py(bar.low)} stroke={color}/><rect x={px(i)-width/2} y={Math.min(py(bar.open),py(bar.close))} width={width} height={Math.max(1,Math.abs(py(bar.open)-py(bar.close)))} fill={color}/><rect x={px(i)-width/2} y={volumeBottom-bar.volume/maxVolume*(volumeBottom-volumeTop)} width={width} height={bar.volume/maxVolume*(volumeBottom-volumeTop)} fill={color} opacity=".45"/></g>})}
    {maEnabled&&averages.map((values,i)=><path key={periods[i]} d={path(values)} fill="none" stroke={colors[i]} strokeWidth="1.3"/>)}
    {bollEnabled&&(['mid','upper','lower'] as const).map(part=><path key={part} d={path(bands.map(b=>b?.[part]??null))} fill="none" stroke={part==='mid'?'#80b5cb':'#6c8fa6'} strokeWidth="1.2" strokeDasharray={part==='mid'?'4 3':undefined}/>)}
    {!samples&&<><line x1={left} x2={w-right} y1={volumeBottom} y2={volumeBottom} className="mc-grid"/><text x={left} y={volumeTop-8}>成交量</text><text x={w-right+10} y={volumeTop+10}>{number(maxVolume/10000,1)}万</text></>}
    {[0,Math.floor((visible.length-1)/2),visible.length-1].filter((v,i,a)=>a.indexOf(v)===i).map(i=><text key={i} x={px(i)} y={379} textAnchor={i===0?'start':i===visible.length-1?'end':'middle'}>{shortTime(visible[i].bar.timestamp,daily&&!samples)}</text>)}
    {hover!==null&&active&&<g><line x1={px(Math.min(hover,visible.length-1))} x2={px(Math.min(hover,visible.length-1))} y1={top} y2={volumeBottom} className="mc-crosshair"/><line x1={left} x2={w-right} y1={py(active.bar.close)} y2={py(active.bar.close)} className="mc-crosshair"/><circle cx={px(Math.min(hover,visible.length-1))} cy={py(active.bar.close)} r="3" fill="#c5ebdf"/></g>}
   </svg>
   <div className="mc-navigation"><span>{visible.length} / {filtered.length} {samples?'点':'根'} · 拖动平移，滚轮缩放</span><input type="range" aria-label="历史位置" min={0} max={Math.max(0,filtered.length-1)} value={Math.max(0,filtered.length-1-effectiveBack)} onChange={e=>{setBack(filtered.length-1-Number(e.target.value));setHover(null)}}/><span>{effectiveBack?'正在查看历史':'最新位置'}</span></div>
  </>}
  <div className="mc-provenance"><p>{samples?'实时采样按实际收到的源时刻记录；横轴是采样序列，MA/BOLL周期单位为采样点，不是分钟。':`${history?.source||'历史行情'} · ${source==='imported'?'导入的完整日线':daily?'已完成日线':'已完成分钟K线'} · 不拼接连续合约，不复权。`}</p><p>{!samples&&<>获取 {source==='imported'?'未记录':time(history?.available_at,true)} · 最新K线 {time(history?.last_bar_at||history?.bars.at(-1)?.timestamp,true)} · {history?.stale?'旧缓存':history?.cached?'缓存复用':'源返回'}{source==='public'?' · 每60秒检查历史':''}{sourceURL&&<> · <a href={sourceURL} target="_blank" rel="noreferrer noopener">原始来源</a></>}</>}{samples?'相同源时刻不重复绘图；采样并非逐笔成交或完整历史。':'指标在已获取的完整窗口计算后再筛选日期；样本不足显示 —。BOLL采用总体标准差，缺口不补价格。'}</p>{history?.warnings?.length&&!samples?<details><summary>历史覆盖与口径 · {history.warnings.length} 条</summary>{history.warnings.map((warning,i)=><p key={i}>{warning}</p>)}</details>:null}</div>
  <div className="mc-table-actions"><button className="text-button" onClick={()=>setTable(!table)}>{table?'收起数据表':'查看历史数据表'}</button><button className="text-button" disabled={!filtered.length} onClick={exportCSV}><Download size={13}/>导出当前范围CSV</button></div>
  {table&&<div className="table-scroll mc-history-table"><table><thead><tr><th>时点（上海）</th>{!samples&&<><th>开</th><th>高</th><th>低</th></>}<th>{samples?'采样价':'收'}</th>{!samples&&<><th>成交量</th><th>持仓</th></>}</tr></thead><tbody>{filtered.slice(-200).reverse().map(({bar})=><tr key={bar.timestamp}><td>{samples?quoteTime(bar.timestamp):time(bar.timestamp,true)}</td>{!samples&&<><td>{number(bar.open,2)}</td><td>{number(bar.high,2)}</td><td>{number(bar.low,2)}</td></>}<td>{number(bar.close,2)}</td>{!samples&&<><td>{number(bar.volume)}</td><td>{history?.has_open_interest===false?'—':number(bar.open_interest)}</td></>}</tr>)}</tbody></table><p>表格显示筛选范围内最近200条，导出包含当前已加载且符合范围的全部记录。</p></div>}
 </div>;
}
