# v0.3 数据接入接口契约

核心将市场快照与回测日线独立存储，公开参考报价不标为交易所授权实时行情。主力规则基于实际合约收盘快照，历史研究主力保持原有逻辑。

## 数据源控制台

GET `/api/data-sources` -> `{sources:Source[],active_market_source:string,auto_sync_paused:boolean}`

固定source ids：`sina`（公开期货快照，涵盖c/cs/lh/m），`liaoning`（辽宁粮储周报），`moa`（农业农村部畜产品饲料周报），`weather`（Open-Meteo），`market_bridge`（授权行情JSON桥接），`industry_bridge`（授权收购价/到车量JSON桥接）。

Source:
```
{id,name,kind,description,official_url,account_required,cost_note,license_note,
 configurable_endpoint:boolean,min_interval_seconds,max_interval_seconds,
 config:{revision,enabled,interval_seconds,endpoint,token_configured},
 status:'disabled'|'unconfigured'|'idle'|'running'|'ok'|'error',
 freshness:'never'|'fresh'|'stale'|'unknown'|'market_closed',freshness_note,
 last_attempt?:RFC3339,last_success?:RFC3339,latest_data_at?:RFC3339,next_run?:RFC3339,
 last_error,consecutive_failures,added,duplicates,total_records,coverage:string[]}
```

POST `/api/data-sources/{id}/config` with `{revision,enabled,interval_seconds,endpoint,token?,clear_token?}` -> Source. 409冲突。同源地址下token空/省略保留现有，clear_token=true删除；跨协议/主机/端口不会自动沿用旧令牌。只有bridge可改endpoint和token；固定源不传token。保存成功后新配置生效，不保存任何前端localStorage凭据。

POST `/api/data-sources/{id}/test` `{}` -> `{ok:boolean,message,record_count,latest_data_at?:RFC3339,coverage:string[]}`。只测试已保存配置，成功不入库，失败用非2xx `{error}`；不可把测试通过当成同步成功。

POST `/api/data-sources/{id}/sync` `{}` -> `{ok:boolean,message,record_count,added,duplicates,latest_data_at?:RFC3339,coverage:string[]}`；完成写状态/发SSE，任务并发冲突429，失败保留旧数据与last_success，错误不包含密钥。

GET `/api/market/live` -> `{source_id,source_name,quotes:Quote[],main:LiveMain,state_history:[],freshness,freshness_note,last_success?,latest_data_at?,selection_note}`。无数据返回空数组，不伪造报价。还返回status/last_error及price_history（每合约最多120个实际采样）；Quote包括实际合约、品种、原始日期/时间、接收时间、昨结算、价格、量仓和来源，具体见market/types.go及data-bridge.md。

GET `/api/industry/series?kind=purchase_price&entity=...` 暂不实现；沿用 `/api/observations`，实际来源UI可按source筛选。

`total_records`是上次成功批次的有效条数（含事件），不是累计库条数。`status`反映自动开关/同步结果，`freshness`独立反映数据时刻；停用源仍可手工同步。天气使用实际采集时刻判定新鲜度。`price_history`是接入后的有限采样，没有逐笔和K线语义。

自动采集受服务端 `CORNWATCH_AUTO_SYNC=false` 全局暂停开关控制（测试/离线场景），无此变量时启用用户已开启的数据源。固定源推荐低频：sina60s，官方12h，weather1h；bridge行情最短5s，产业最短60s。各源独立超时、锁、指数失败退避。配置中的频率为采集频率，不等于源数据的更新频率。
