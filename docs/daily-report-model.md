# 盘前与盘后报告的证据及模型协议

`backend/internal/dailyreport` 是独立、无存储副作用的报告内核。它不管理调度、任务记录、账户、订单或日记。服务器负责建立任务、冻结输入、调用此包、保存成功结果及失败状态。模型失败时不能用规则结果替换并标记为 AI 成功。

## 接口

```go
Build(kind, date string, current *currentanalysis.Report,
    observations []domain.Observation, events []domain.Event,
    now time.Time) (Bundle, error)

BuildWithQuotes(kind, date string, current *currentanalysis.Report,
    observations []domain.Observation, events []domain.Event,
    quotes []market.Quote, now time.Time) (Bundle, error)

New(analysis.Config) (*Generator, error)
(*Generator).Configured() bool
(*Generator).Generate(context.Context, Bundle) (Output, error)
(*Generator).Test(context.Context) error
Rules(Bundle) Output
```

`kind` 为 `pre` / `post`，`date` 为上海时区的 `YYYY-MM-DD`。`AsOf` 才是资料截止时刻，标签日期不能把后来采集的资料变成历史可得资料。日期与截止日不同时增加醒目警告；服务器可以进一步限制任务日期。`Build` 要求已有有效的真实当前分析，`BuildWithQuotes` 额外收纳关联期货快照。

`Test` 是显式调用才执行的真实模型连接测试；它使用一条明确标记为非市场的本地样例，同样验证 JSON 和精确引文，不访问行情、不自动保存。离线单元测试使用 `httptest` 模拟服务，没有真实模型费用。

## 冻结证据与时间规则

`Bundle` 包含 `as_of, kind, date, symbol, evidence, warnings, current, input_sha256`。每条 `Evidence` 包含 `id, category, title, text, source, source_url, observed_at, available_at, is_forecast`，可附 `unit, note, original_ids`。

- 非演示资料只有在 `available_at <= as_of` 时可进入输入。缺少首次可用时间不能用发布日期补成历史可得时间。非预测观测、公告发布日期、行情及接收时间也不能在截止之后。
- 行情保留被当前分析选中的准确快照，不被稍后读取的 live 同合约值替换。其余期货快照各自带时刻和来源；此包不自动计算不同步价差。
- 技术证据只来自 `currentanalysis.Report` 的已完成日 K。拒绝演示数据、不同合约、未来可用日线、没有有效交易日期或尚未到该日 15:15 保守确认窗口的日线，与 `marketdaily` 的成熟时间规则一致。保留原始技术报告、数据 URL、可用时间和全部元数据用于核验；公开日线的完整性校验仍由上游负责。
- 天气允许目标日晚于截止时刻，但必须已采集，并明确 `is_forecast=true`。同一来源、地区、目标日和指标只取截止前最新可用版本。保留当天至未来 7 天窗口中的有限样本。原有 5 地区 × 3 日 × 3 指标可聚合成 15 条地区/目标日证据，保留各指标原值、单位、首次可用时间及 `original_ids`。各指标的首次可用时间不同，不能冒充统一气象发布时间；聚合条目 `available_at` 取所含指标最晚可用时间。
- 收购价单位缺失时显示“未注明”，保留原值和原始水分、税费、等级、地点备注；不得猜测元/吨或元/斤。官方监测均价和企业收购价按实体、来源及口径分别呈现，不合并成工厂当天报价。
- 到车保留相同来源、地区、实体和单位的最近两期，供模型做有来源的比较。晨间到车、待卸排队、全天到货和卸粮吨数不得混同。
- 进口资料明确区分进口存量拍卖、海关进口量、新签海外采购和到港。关联现货与关联期货分别列证据。
- 已公布公告使用发布日期和首次可用时间，不以正文中的未来生效日过滤，也不把公告自动转成已生效报价。最近 30 天公告按来源限量选取；长期政策全文检索不在本版本范围内。
- 所有资料保留统计/目标时点与可用时间。历史观察明确说明距截止的时长；缺项加入警告，不能解释为零、无变化或没有事件。

最多 64 条证据，正文及备注合计 32,000 Unicode 字符，冻结包最多 1 MiB。每个产业类别、来源和口径均有取样额度；类别轮流占用正文预算，防止大量企业条目挤掉天气、进口和公告。取样、截取与缺项均进入警告。输入哈希覆盖完整 `Bundle`（计算时排除哈希字段自身），包括深复制的完整当前分析、截止时刻及选入资料，不仅覆盖价格数值。

模型仅接收白名单字段 `as_of/kind/date/symbol/evidence/warnings`，不会接收完整日线数组、连接密钥、账户或未选原始资料。哈希保留完整快照供本地审计；它不证明外部来源真实，也不是历史回测数据证明。

## 模型输出与校验

输出结构：

```text
mode: "llm" | "rules"
summary: string
sections: [{title, body, evidence_ids: string[],
            quotes: [{evidence_id, text}]}]
scenarios: [{name, condition, invalidation, evidence_ids: string[]}]
risks: string[]
model, prompt_version, generated_at, input_sha256
```

模型只可填写 `summary/sections/scenarios/risks`；模式、模型名、提示版本、生成时间和输入哈希由服务器填写。每章节必须有有效证据 ID，且每个 ID 必须有对应原文中精确连续的引文。情景必须有条件、失效条件和有效引用。事实与推断在文字中分别说明；模型没有工具或订单能力。最多 12 章节、6 情景及 12 条模型风险；程序会追加原始证据警告和人工核验提示。

兼容现有配置的 `POST {BaseURL}/chat/completions`，以 `response_format={type:"json_object"}` 请求单个 JSON 对象。不假定所有兼容服务支持严格 JSON Schema，因此在本地执行字段、类型、长度、引用和精确引文校验。协议依据 [Chat Completions 官方接口](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create) 和 [Structured Outputs / JSON mode 官方说明](https://developers.openai.com/api/docs/guides/structured-outputs?api-mode=chat)：JSON 模式本身不保证业务 schema，仍须处理不完整响应。

独立 HTTP 客户端与上下文均为 60 秒，单实例一次生成，禁止重定向。远程地址要求 HTTPS，本机回环地址允许 HTTP。请求最多 512 KiB、响应最多 1 MiB、模型内层 JSON 最多 128 KiB。以下均拒绝：截断或非 `stop` 结束、拒绝、工具调用、多个结果、重复 JSON 字段、额外字段、无效/多对象 JSON、未知引用、伪造引文、缺少失效条件、超长响应以及配置 API key 回显。错误不回显供应商原始响应或凭据。不自动重试付费请求。

精确引文只能核验引用存在，不能证明解释正确、事实真实或因果成立；输出风险中明确保留这一限制。规则预览始终显示 `mode=rules`、`model=rules-daily-v1` 和“规则预览”，只组织资料，不伪装成大模型分析。

## 离线验证

```bash
cd backend
go test -race ./internal/dailyreport
go vet ./internal/dailyreport
```

测试覆盖首次可用时间、未来天气目标、预报版本更新、公告未生效、演示与未来资料排除、5 地区 3 日天气覆盖、未注明单位、上期到车、来源取样、输入顺序及哈希、当前快照深复制、完成日线与合约一致性、关联报价、规则标记、请求协议、严格引文与 JSON、工具拒绝、凭据脱敏、重定向、取消和并发繁忙。持久化、重启调度与页面操作由服务器及 UI 的整合测试覆盖。
