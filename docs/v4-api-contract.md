# v0.4 公开产业与当前分析接口增量

鉴权、IPC白名单、来源生命周期与字段沿用 [v0.3契约](v3-api-contract.md)。无外部账号或交易调用。

## 新增内置来源

| source id | 默认自动间隔 / 最小间隔 | 返回数据 |
| --- | --- | --- |
| `boyar_prices` | 12小时 / 6小时 | 企业表原值、明确条件的企业通知与事件；未知单位保存“未注明” |
| `boyar_arrivals` | 12小时 / 6小时 | 山东晨间到车原统计口径及事件 |
| `import_auction` | 24小时 / 12小时 | 最近公开进口玉米库存拍卖的合计成交率及事件 |

固定来源共7个、可选授权桥接2个。三者都不能设置自定义URL或凭据。报价/到车超过4天标为陈旧；拍卖超过14天标为陈旧，备注保留按场次发布语义，阈值不是交易频率承诺。

`import_auction` 查询结构有效但没有匹配公告时，可返回成功且`record_count=0`，`message`说明暂无公告。此时不生成0%观测、不清空历史，`last_success`表示查询成功，`latest_data_at`仍是原记录日期；`freshness_note`解释本次无公告。其他来源空批次仍按失败处理，避免空数据冒充覆盖。

新增 Observation kind `import_auction_rate`，unit `%`，范围0—100。手工录入支持此类型，授权产业桥接的原协议仍只支持其已声明的三类指标，不能隐式拓展供应商协议。

## 当前分析

- GET `/api/research/current`：`{report:null}`或`{report:Report}`。只读取最近已保存报告，不触发外网或分析。
- POST `/api/research/current` `{}`：选取真实实际玉米合约，读取实际合约完成日线，与当前来源快照和本地产业证据组合，成功后保存并返回`{report:Report}`，发SSE `current-analysis`。
- 无行情/无效上游返回400；并发分析返回429；持久化失败返回500。失败保留上一报告，不向回测K线库写入任何公开行情样本。

Report包括：`generated_at, version, input_sha256, symbol, selection, headline, snapshot, market_freshness, daily_source, daily_url, daily_available_at, daily_sha256, technical, spreads, factors, scenarios, evidence, warnings`。具体结构以 `backend/internal/currentanalysis/current.go`为准。

`technical`沿用研究指标定义；只读日线采集器剔除未完成日期，15:15以后才接收当日日线，缺少结算或持仓则明确标记，不用收盘伪造结算。快照与完成日线分开记录；没有交易所假日日历时不把自然日期断言为夜盘交易日。

价差要求同来源、相同非空源日期、时间差不超过5分钟，未来时间拒绝；远月选该时刻组中活跃合约，不写死05月。证据只用非演示且本地已经可得的记录，保留所有参与到车比较的前期记录。输入hash覆盖本批市场输入、完整日线报告及引用产业证据；日线原响应SHA单列。分析不代表策略回测或未来方向预测。

界面将报价陈旧、日线过期提示放在报告顶部，支持查看原始口径、JSON导出和带入复盘。导入日记包含生成/日线/快照时刻、观察条件、警告与输入指纹；AI整理继续使用已有复盘智能体流程。
