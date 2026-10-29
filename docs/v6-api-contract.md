# v0.6 行情频率与历史图表接口

沿用本地鉴权、只读行情与真实/演示隔离。Electron仅新增放行 `GET /api/market/history`。数据源配置的 `interval_seconds`、`min_interval_seconds`、`max_interval_seconds` 改为JSON数值，支持0.5秒；整数格式仍兼容。新浪与授权行情桥接默认/最低0.5秒，其他源不变。

## 行情运行统计

`GET /api/market/live` 及每个数据源增加 `polling`：

```json
{
  "target_interval_ms": 500,
  "target_requests_per_minute": 120,
  "requests_last_minute": 118,
  "last_latency_ms": 18.4,
  "last_request_at": "2026-10-29T02:00:00Z",
  "stats_since": "2026-10-29T01:59:00Z",
  "persisted_at": "2026-10-29T01:59:57Z",
  "persistence_note": "内存报价即时更新；自动采集最多每5秒保存快照，正常退出刷新。异常退出可能丢失末尾未保存快照。"
}
```

上例仅描述结构，不是真实市场记录。运行统计不持久化，计数为滚动60秒内实际开始的采集尝试，不包含缓存命中；零次表示本进程没有该窗口内的尝试。`last_request_at`、`stats_since`、`persisted_at` 尚未产生时省略。请求耗时包含获取及解析，不是行情源延迟。源时效继续用报价 `observed_at` 判断。

快速行情成功缓存截止于请求开始时刻加 `min(interval, 5s)`，失败冷却至少5秒。正常自动请求采用开始到开始节奏；低频源沿用完成后计时。相同源不重叠，慢请求不补发积压任务。行情同步完成的SSE事件类型为 `market`，配置及非行情来源变化仍为 `data-sources`。

配置POST优先排队等待该源正在运行的请求，等待期间不启动同源新采集，最多20秒且响应调用方取消。其他源继续运行；重复排队返回429，版本冲突仍为409，取消或超时不修改配置。已有直接 `Hub.Save` 忙碌语义保留，HTTP层使用 `SaveWhenIdle`。

## 历史查询

`GET /api/market/history?symbol=c2701&interval=5m&source=public&limit=1000`

| 参数 | 约束 |
| --- | --- |
| symbol | 必填，c/cs/lh/m实际合约，校验交割月；拒绝主连/指数 |
| interval | day（默认）、1m、5m、15m、30m、60m |
| source | public（默认）、imported；imported仅支持day |
| from / to | 可选有效YYYY-MM-DD，按北京时间自然日期筛选，起止不能颠倒 |
| limit | 默认1000，1至6000；返回所选范围内最后N根 |

未知或重复查询参数返回400，鉴权失败401；无可用历史且更新失败返回502；失败时如存在缓存则200并明示 `stale`、`last_error`。没有匹配日期的记录返回空数组，不把超出覆盖范围视为零行情。

返回对象字段：`symbol, interval, bars, source, source_url, available_at, last_bar_at, cached, stale, last_error?, warnings, total_available, range_start?, range_end?, refresh_after_seconds, has_open_interest, has_settlement`。`bars`沿用OHLC/成交量/持仓等字段，公开历史额外保留每根 `available_at`；未提供的结算值不参与真实结算推算。本地CSV缺逐根首次可用时间，接口明确附注该限制。

`total_available/range_start/range_end`描述缓存覆盖，`bars`为日期和条数筛选后的结果。来源窗口可能小于请求limit。公共历史每个合约/周期最多6000根，持久化到独立 `market_chart_v1_` 元数据，不写可回测日线。相同历史查询共享进行中的请求及至少60秒缓存/失败冷却，不因500ms行情通知而获取历史。

程序退出先取消HTTP、报告及历史共享请求，等待采集调度结束并刷新快速行情快照，最后关闭数据库。正常退出流程已覆盖；强制终止仍有末尾未保存采样损失的可能。
