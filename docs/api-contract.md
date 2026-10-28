# CornWatch API v1 — 开发契约

所有时间 RFC3339，日期 YYYY-MM-DD，snake_case JSON。默认监听 127.0.0.1:8765；Electron 通过主进程限定路径代理访问并携带临时 Bearer token；浏览器开发由 Vite /api 代理。错误 `{error:string}`。列表直接数组。演示数据永远标记 `is_demo:true`，默认无实时行情连接。

## GET /api/health
`{status:"ok",version:"0.1.0",mode:"research",live_trading:false}`

## GET /api/dashboard
`{as_of:string,mode:"demo"|"imported"|"empty",quotes:Quote[],events:Event[],observations:Observation[],main_contract:MainDecision,sources:SourceStatus[],bar_count:number,event_count:number,llm_configured:boolean}`

Quote: `{symbol,name,last,previous_close,change_pct,volume,open_interest,observed_at,source,is_demo}`

Event: `{id,title,body,category,region,products:string[],source,source_url,published_at,ingested_at,available_at,severity:"low"|"medium"|"high",is_demo:boolean,analysis?:Analysis}`

Analysis: `{summary,direction:"bullish"|"bearish"|"mixed"|"unknown",confidence:number,reasoning:string[],evidence:string[],risk_flags:string[],model,analyzed_at}`. confidence 0..1，不是收益概率。

Observation: `{id,kind:"purchase_price"|"truck_arrivals"|"weather"|"related_price",region,entity,value,unit,observed_at,available_at,source,source_url,is_demo,note}`。

MainDecision: `{symbol,previous_symbol,reason,as_of,pending_symbol,confirmation_days,required_days,is_demo}`。数据不足时 symbol 可以空，不用日历硬切换。

SourceStatus: `{id,name,kind,status:"demo"|"configured"|"not_configured"|"ok"|"error",last_success?,message}`

## GET /api/bars?symbol=c2701&demo=true
Bar[]。Bar: `{symbol,timestamp,trade_date,open,high,low,close,settlement,volume,open_interest,limit_up,limit_down,is_demo}`。timestamp 为K线结束时间，日K至少包含显式交易日。demo=false选择导入数据。

## GET /api/events / GET /api/observations / GET /api/sources
对应数组。GET /api/events?q=关键词&category=weather 可筛选。

## POST /api/events
Event 输入不需要 id/ingested_at/available_at，服务端赋值；source_url 可空。published_at 必填。响应完整 Event，去重时200，否则201。

## POST /api/events/{id}/analyze
body `{use_llm:boolean}`，默认规则分析。LLM 未配置且 use_llm=true 返回400，不冒充AI。返回带 analysis 的 Event。外发数据只含该事件正文，不带账户信息。

## POST /api/observations
Observation，id和available_at服务端生成。必填 kind/region/entity/value/unit/observed_at/source。返回完整 Observation。

## POST /api/import/bars
body `{csv:string,source:string}`。CSV列：symbol,timestamp,trade_date,open,high,low,close,settlement,volume,open_interest,limit_up,limit_down。全量校验成功后原子入库；唯一键symbol+timestamp；响应 `{imported:number,duplicates:number}`。禁止混淆样例与导入，导入 is_demo=false。

## POST /api/backtests
body `{symbol:string,demo:boolean,fast_window:number,slow_window:number,initial_capital:number,lots:number,fee_per_lot:number,slippage_ticks:number,margin_rate:number,max_drawdown_pct:number}`。手续费每手每边，滑点每边ticks；示例参数 5,20,100000,1,1.2,1,0.12,20。首版固定玉米合约乘数10/tick1；仅接受单个 cYYMM 合约，不支持主力连续拼接；保证金为研究参数。
结果 `{id,created_at,symbol,is_demo,config:{...},input_sha256,engine_version,bar_count,metrics:{initial_capital,final_equity,total_return_pct,max_drawdown_pct,total_fees,total_slippage,trade_count,win_rate_pct,open_position,unrealized_pnl},equity_curve:[{timestamp,equity,drawdown_pct,position}],trades:[{timestamp,symbol,side:"buy"|"sell",offset:"open"|"close",price,lots,fee,slippage,reason,pnl}],warnings:string[]}`。下一根bar open执行；按收盘估值，尚未模拟真实逐日结算。日末volume为事后可成交性过滤；末尾尝试平仓，不能成交则保留持仓并披露未发生退出成本。trade_count为完成往返数，滑点独立现金扣除。

## GET /api/backtests
最近结果数组（最多20）。

## POST /api/collect/weather
无需body。按配置城市从 Open-Meteo 拉取天气（5个区域），真实请求成功才记录；返回 `{imported:number}`。由手动刷新或环境变量启用定时抓取，不混入演示天气。UI需显示 Open-Meteo attribution。

## POST /api/collect/feeds
无需body。从服务端配置的授权RSS/Atom feed收集；未配置返回400。返回 `{imported:number}`。

## GET /api/settings
只读，`{llm:{configured,base_url,model},feed_count,weather_enabled,storage,live_trading:false}`。密钥仅后端环境变量。

## GET /api/stream
SSE事件 `update`，data `{kind,at}`，客户端重新读取dashboard，断线回退定期刷新。Electron通过主进程订阅转发，渲染端不用拿token。

## Desktop bridge
`window.cornwatch.request(path:string, {method?:string,body?:unknown}):Promise<any>` 限制/api/路径、GET/POST。
`window.cornwatch.onUpdate(callback):()=>void` 可选。浏览器fallback fetch同源/api。


## v0.2 扩展

盘面、仓位风险、时间留出、复盘日记/图片/智能体/经验/审计快照接口见 [v2 API契约](v2-api-contract.md)。原接口保持兼容；回测引擎版本升级为 `sma-daily-v2`，新增 `evaluation_start` 标记。
