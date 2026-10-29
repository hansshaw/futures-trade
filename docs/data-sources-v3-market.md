# v0.3 行情来源核验与接入决策

核验日期：2026-10-28，北京时间。本文区分官方文档声明、实际公开端点探测和本项目的研究规则。未登录 TqSdk，未获得任何商业数据授权，也未测量交易时段的端到端延迟。公开端点的可访问性不等于稳定接口承诺或再分发许可。

## 1. 结论和本轮落地

当前可直接接通新浪的公开研究报价：玉米 `c`、玉米淀粉 `cs`、生猪 `lh`、豆粕 `m` 均可获得实际合约代码、源时间、最新价、成交量和持仓量。Go 后端的 `internal/market` 已实现一次 HTTPS 批量请求、GB18030 解码、严格字段校验和收盘主力确认；不把报价写入历史日线。失败返回错误，由调用方保留上次成功快照，不能换成演示数据冒充真实行情。

授权路线已提供 `adapters/tqsdk_bridge.py` 只读适配进程，由 Go 管理状态、持久化和前端推送。免费账号可先验证期货行情需求；历史批量下载、专业服务和云端用途应按实际权益配置。适配器通过标准库 mock 测试，本次没有完成账号登录验证。

大商所公开日行情有上游实现，但本环境 HTTPS 请求失败，HTTP 返回访问保护页面。保留人工下载后导入或将来的授权接入路径，不绕过保护，不让它阻塞本轮上线。

| 来源 | 账号 / 费用 | c / cs / lh / m | 本次证据 | 用途定位 |
| --- | --- | --- | --- | --- |
| 新浪公开批量报价 | 本次公开请求无登录；未发现接口级 SLA 或商业授权承诺 | 四品种实际合约已实测 | 2026-10-28 17:51:37 成功，26 个实际合约 | 本地研究展示、审计快照 |
| 新浪公开日线 | 本次公开请求无登录 | 四个 2701 合约均实测 | 最新日期均为 2026-10-28 | 待完善版本与收盘校验后用于研究历史补充 |
| DCE 公共日行情 | 官网公开页面；访问条件不等于开放 API 保证 | 上游接口按全部品种查询 | HTTPS EOF；HTTP 412 HTML | 官方核对和人工文件导入 |
| TqSdk | 快期账号；免费 / 专业 / 企业权益不同 | 官方文档覆盖全部期货，四个 DCE 品种可用规范代码表示 | 官方文档确认；未登录实测 | 后续授权行情与历史研究通道 |
| AKShare | 开源 Python 接口库；不是行情版权转授权 | 上游封装了相关来源 | 阅读官方文档与源码 | 研究接口结构、交叉校验；不是另一个独立数据源 |

## 2. 新浪公开行情：实际核验

### 2.1 已实现的批量报价

端点示例：

```text
GET https://hq.sinajs.cn/list=nf_C2701,nf_CS2701,nf_LH2701,nf_M2701
Referer: https://finance.sina.com.cn/
```

响应为 GB18030 编码的 JavaScript 变量赋值文本。代码只接受已请求的 `var hq_str_nf_<实际合约>="...";` 结构，不运行 JavaScript、不使用 `eval`。当前实测每条 44 字段；兼容上游已记录的 28 字段版本，其他字段数拒绝并保留旧快照。不存在或尚未上市的合约可能返回空字符串。

本轮按当月及未来 13 个月生成实际交割月代码。`c/cs/lh` 使用奇数月；`m` 还包含 8 月与 12 月，不能把玉米的月份限制误用于豆粕。生成范围有意有限，不声称覆盖全部远月或完整合约生命周期；未上市代码的空响应被排除。代码始终不请求 `C0/CS0/LH0/M0` 来冒充实际合约。

2026-10-28 17:51:37 的真实 Go 适配器探测结果：共 `c=6、cs=6、lh=6、m=8` 个实际合约。

| 实际合约 | 最新价 | 成交量 | 持仓量 | 原始报价日期 / 时间 |
| --- | ---: | ---: | ---: | --- |
| c2701 | 2224 | 609271 | 1276926 | 2026-10-28 15:04:55 |
| cs2701 | 2482 | 104846 | 275989 | 2026-10-28 15:04:00 |
| lh2701 | 9840 | 204480 | 163204 | 2026-10-28 15:04:55 |
| m2701 | 3208 | 705130 | 2099138 | 2026-10-28 15:04:55 |

这些是收盘后的网页源快照；`15:04:55` 不表示交易所仍在此时撮合，也不能用这一次收盘后探测证明交易时段“实时”。保留 `observed_at` 与 `received_at` 两个时间，源日期不是经过交易日历校验的夜盘交易日期。

字段采用零基索引：`0` 名称、`1` 时钟、`2/3/4` 开高低、`8` 最新价、`10` 昨结算、`13` 持仓量、`14` 成交量、`17` 日期。实测字段 `5=2224`，而另一公开接口的明确 `preclose=2217`；因此不能照搬上游某列的 `last_close` 标签。本项目将涨跌幅定义为 `(最新价 / 昨结算 - 1) × 100`，返回 `change_basis=previous_settlement`。

网络层强制 HTTPS、禁止重定向、12 秒请求截止、512 KiB 响应上限。解析校验来源、请求白名单、重复/缺失合约响应、字段版本、编码、有限价格、OHLC 关系、非负整数成交量/持仓量及未来时间。任一非空记录格式异常则整批失败；每个请求品种至少要有一条有效实际合约，防止局部错误被展示为完整刷新。

参考的是 [AKShare 新浪期货上游实现](https://github.com/akfamily/akshare/blob/4771e6ea34f6139f226e2bf4fab88ce17579831d/akshare/futures/futures_zh_sina.py)，对应文件提交时间 2026-08-13 06:13:07 UTC。核验时下载文件 SHA-256：`9aee5f73cc9367a4574a6ebfde0eab9e6ca1cf65d62ad79042281e0271228211`。上游用于 `match_main_contract` 的重复字段匹配不能替代实际合约证据，本项目没有复制该推断。

### 2.2 可作为后续合约发现补充的 JSON 接口

以下四个节点均已低频实测 HTTP 200：

```text
GET https://vip.stock.finance.sina.com.cn/quotes_service/api/json_v2.php/Market_Center.getHQFuturesData?page=1&num=100&sort=position&asc=0&node=hym_qh&base=futures

hym_qh = 玉米
ymdf_qh = 玉米淀粉
lh_qh = 生猪
dp_qh = 豆粕
```

节点来自公开的 `https://vip.stock.finance.sina.com.cn/quotes_service/view/js/qihuohangqing.js`。响应是 JSON 数组，有明确 `symbol`、`exchange`、`tradedate`、`ticktime`、`preclose`、`presettlement` 等字段。大部分数值仍是字符串，需逐项校验。应过滤 `C0/CS0/LH0/M0`，并为未来分页做好处理，不能把第一页永远视为完整市场。

实际 `C2701` 行的关键值：

```json
{
  "symbol": "C2701", "exchange": "dce", "trade": "2224.00",
  "open": "2220.00", "high": "2229.00", "low": "2211.00",
  "volume": "609271", "position": "1276926",
  "tradedate": "2026-10-28", "ticktime": "15:04:55",
  "preclose": "2217.000", "presettlement": "2208.000",
  "settlement": "2219.00", "changepercent": "0.0072464"
}
```

`changepercent` 是比例而非已经乘 100 的百分数。这条记录按昨结上涨约 `0.72464%`，按昨收上涨约 `0.31574%`，两者不能混用。

[新浪玉米行情页](https://finance.sina.com.cn/futures/quotes/C0.shtml) 源码在本次核验中含 `chicang_code="C2701"`，可作为带采集时间的“网页标示主力”证据；但它不是有文档保证的主力映射 API。页面另一合约数组还包含已过期的 `C2411`，不能直接当作活跃合约全集。连续代码的报价字段本身不携带实际合约标识；连续与实际价格相等不是可靠映射依据。

### 2.3 日线可获取性与解析示例

```text
GET https://stock2.finance.sina.com.cn/futures/api/jsonp.php/var%20_cornwatch=/InnerFuturesNewService.getDailyKLine?symbol=C2701
```

同样替换为 `CS2701/LH2701/M2701`，本次均 HTTP 200。第一日分别为 2026-01-19、01-20、01-28、01-19，末日均为 2026-10-28。字段 `d/o/h/l/c/v/p/s` 分别表示日期、开高低收、成交量、持仓量、结算价。原始响应包含注释和 JSONP 包装；绝不能执行它。

以下仅演示严格解包，不是完整的网络/数据质检实现：

```python
import json
import re

def unwrap_daily(text):
    # 只容许本次实测的可选固定注释和固定 callback；不运行脚本。
    prefix = "/*<script>location.href='//sina.com';</script>*/"
    body = text.strip()
    if body.startswith(prefix):
        body = body[len(prefix):].lstrip()
    match = re.fullmatch(r"var _cornwatch=\((\[.*\])\);?", body, flags=re.S)
    if not match:
        raise ValueError("unexpected daily response wrapper")
    rows = json.loads(match.group(1))
    if not isinstance(rows, list):
        raise ValueError("daily response is not a list")
    return rows  # 下一步仍须逐字段、逐日期、OHLC、量仓与结算值校验。
```

当前日线可能仍在更新，历史值也可能更正。日线导入前应记录源、采集时点、版本和完成状态；当前交易日先进入待确认区，不能把盘中快照当不可变历史数据。本轮未增加自动历史写入，避免与现有不可变导入键、结算字段和回测可得时间规则冲突。

### 2.4 权利、稳定性与频率

[新浪财经首页声明](https://finance.sina.com.cn/) 对部分国内期货使用实时行情描述，同时声明信息由合作方提供且可能存在错误、延迟或缺失；不能把“全部新浪期货延迟 15 分钟”当作事实。另一方面，本次公开网页接口没有经验证的 API SLA、重连保障、调用配额或云端转售权利。建议本地研究默认低频、单采集器缓存、人工刷新合并和失败退避；例如分钟级刷新是本项目保守工程选择，不是源方公布的允许频率。

[AKShare 官方项目说明](https://akshare.akfamily.xyz/introduction.html) 明确其公开数据接口面向学术研究并提示商业风险。开源代码许可不能被理解为对新浪或交易所数据的商业转授权。云服务器自用、多人共享和商业再分发是不同授权场景，应在实际部署前核对供应商条件。

## 3. 大商所官方日行情

[AKShare 官方上游日行情源码](https://github.com/akfamily/akshare/blob/master/akshare/futures/futures_daily_bar.py) 的 `get_dce_daily` 已使用以下请求，不能继续按旧博客的导出页面假设接口永远不变：

```text
POST http://www.dce.com.cn/dcereport/publicweb/dailystat/dayQuotes
Content-Type: application/json
```

```json
{"contractId":"","lang":"zh","optionSeries":"","statisticsType":"0","tradeDate":"20261028","tradeType":"1","varietyId":"all"}
```

上游期望 JSON `.data`，字段含 `variety/contractId/open/high/low/close/lastClear/clearPrice/volumn/openInterest/turnover`，其中成交量键确实拼作 `volumn`。必须滤去汇总行，保留实际合约与官方结算价。

本次先探测 HTTPS 同路径，约 5 秒后发生 TLS unexpected EOF；再按上游 HTTP 地址探测一次，收到 HTTP 412、约 3 KiB HTML 保护页面，而非 JSON。没有伪造鉴权、自动求解访问挑战或关闭 TLS 验证。这个结果仅说明当前环境不可直接可靠获取，不表示所有网络都永久不可用。项目应将这一来源标记为未连通，保留官网人工文件导入与供应商授权数据的替代路径。

## 4. TqSdk 账号、权益和集成边界

2026-10-28 核验的文档版本为 v3.10.4。[官方购买页](https://www.shinnytech.com/store/tqsdk) 年付视图显示免费版 0 元、专业版 14,888 元/年、企业版 30,000 元/年；页面同时显示划线原价。未核验月付价，实际成交以账户购买页/合同为准。免费版目前列明不限次数回测，不能沿用旧材料的“每天三次”限制。

[官方专业版说明](https://doc.shinnytech.com/tqsdk/latest/profession.html) 说明免费行情覆盖全部期货，专业版可申请 15 天试用，历史批量 `DataDownloader` 属于专业功能；免费与专业的“3 个账户”指可绑定的期货实盘账户，不是三条行情进程或无限并发保证。专业 EDB 服务说明提供 2021-01-01 起分钟/日线与库存、交割、现货等非价量数据。购买页另述 2016 起的期货历史，这是不同服务口径，不能混成同一个接口承诺。

行情只读入门需要快期账号和密码 `TqAuth`，不需要为了读取报价配置真实期货交易账号。[官方快速入门](https://doc.shinnytech.com/tqsdk/latest/quickstart.html) 给出了 `TqApi`、`get_quote` 和 `underlying_symbol` 的用法。后续适配器应从后端环境/凭据存储读取账号，不进入 Electron renderer、日志、导出和复盘附件；启动失败明确显示未授权/未配置，不能自动退化为伪造行情。

| 本项目品种 | Tq 实际合约示例 | Tq 主连订阅代码 |
| --- | --- | --- |
| c | `DCE.c2701` | `KQ.m@DCE.c` |
| cs | `DCE.cs2701` | `KQ.m@DCE.cs` |
| lh | `DCE.lh2701` | `KQ.m@DCE.lh` |
| m | `DCE.m2701` | `KQ.m@DCE.m` |

[官方行情文档](https://doc.shinnytech.com/tqsdk/latest/usage/mddatas.html) 规定代码大小写敏感，主连不是可交易实际合约；主连的 `underlying_symbol` 可给实际标的，供应商规则为量仓均居首后在下一交易日开盘切换。行情同时提供 `pre_close/pre_settlement/settlement` 等明确字段。适配器需持续观察标的变化、订阅新的实际合约、保存映射生效时点，不能只在程序启动时读取一次，也不能与本项目两日研究确认规则混称为同一主力。

只读形态的最小结构如下；尚未以用户账号执行，不代表集成已获授权验证：

```python
import os
from tqsdk import TqApi, TqAuth, TqSim

api = TqApi(account=TqSim(), auth=TqAuth(os.environ["TQ_USER"], os.environ["TQ_PASSWORD"]),
            debug=False, disable_print=True)
try:
    main = {p: api.get_quote("KQ.m@DCE." + p) for p in ("c", "cs", "lh", "m")}
    actual = {}
    while True:
        api.wait_update()
        for product, continuous in main.items():
            symbol = continuous.underlying_symbol
            if symbol and symbol not in actual:
                actual[symbol] = api.get_quote(symbol)
            # 校验实际品种/过期状态；将 actual[symbol] 的有限数值、源时间、
            # 本地接收时间与映射版本发给 Go。不要把 API 对象直接 JSON 序列化。
finally:
    api.close()
```

已落地桥接器通过 `query_quotes(ins_class="FUTURE", exchange_id="DCE", product_id=[...], expired=False)` 动态发现全部有效实际合约，每个北京时间自然日重新查询；示例的主连映射与实际合约快照是不同信息。本轮桥接只导出实际合约。特别核验了上游实现：`TqMultiAccount([])` 会回落到内部 `TqSim()`，所以不能宣称传空列表就创建零账户环境。桥接器明确使用本地 `TqSim()` 对象，不提供真实券商账号、不调用下单/持仓/交易接口。[官方 API](https://doc.shinnytech.com/tqsdk/latest/reference/tqsdk.api.html)、[上游多账户源码](https://github.com/shinnytech/tqsdk-python/blob/master/tqsdk/multiaccount.py)

桥接只监听 `127.0.0.1`，默认端口 `8787`，令牌通过 `CORNWATCH_BRIDGE_TOKEN` 配置。`GET /quotes` 要求 Bearer 认证；初始化、SDK 连接失败或工作线程超过 15 秒没有更新健康状态返回 503。`generated_at` 表示 HTTP 导出时刻，`received_at` 只在行情实际字段变化时更新；反复请求不会把陈旧行情重新盖章。SDK 为可选依赖，独立 requirements 固定 `tqsdk==3.10.4`，已通过 [PyPI 元信息](https://pypi.org/project/tqsdk/3.10.4/) 确认该版本存在且要求 Python 3.9 以上，没有为测试安装到用户环境。

K 线订阅单序列最多 8,000 根，最后一根可能继续变化；时间戳表示 K 线起点，夜盘日期不能直接取时间字符串日期来假造交易日。标准 K 线的 OHLC/volume/open_oi/close_oi 不足以凭空补齐结算价。完整回测仍需明确历史范围、结算数据、交易日历、合约规则、数据版本和权限；不能用 `close` 静默替代 `settlement`。具体批量下载与历史时间处理应以授权后的 SDK/EDB 实测为准。[官方行情与历史数据说明](https://doc.shinnytech.com/tqsdk/latest/usage/mddatas.html)

## 5. 本项目研究主力状态机

实现入口：`market.New().Collect(ctx) ([]Quote, error)` 和 `market.Update(state, quotes, now) State`。调用方负责持久化成功快照和状态。它与 `quant.ResolveMain` 的历史回放是不同用途：这里处理当前收到的公开报价，不以当前主力反写历史。

1. 只比较实际玉米合约；剔除交割月及过去月份、零量仓、未来时点、重复记录、不同源和不同时段记录。至少两个有效可比较合约。
2. 首次候选需要成交量与持仓量同时居前；分裂领导或量仓完全并列不强选。首次显示 `provisional_symbol`，不声称已经两日确认。
3. 换月候选只能比已确认主力更远；成交量和持仓量都必须严格超过原合约 10%。缺少原主力的可比较报价会中断该次收盘确认。
4. 成熟收盘条件采用当前保守规则：源时间为当日 15:00～15:20，采集/评估在北京时间 15:15～20:55，源日期与实际接收日期均为当日。盘中要求接收时间距评估不超过 5 分钟；正常收盘后的不变报价保留实际接收时间，允许继续确认。调用方必须只在本次成功获取后调用 `Update`，桥接另行验证新的 `generated_at` 和健康状态，不能用失败后的本地旧缓存推进确认。这个窗口是本项目对这四个品种网页更新行为的保护条件，不是交易所发布的市场状态 API。
5. 连续两个“实际观察到的不同收盘日期”确认。每日期只消费首次成熟快照，同日刷新不加天数；某个被评估收盘日条件不满足即清零。未观测日期不会自动补齐，也不声称两个观察日期必为交易日历相邻日。正式交易策略需再引入交易日历与缺测处理。
6. 盘中/夜盘仅更新候选。休市旧快照可展示，但状态为陈旧/未推进；夜盘不推断官方交易日。没有新合格数据绝不自动按月份换月。
7. 确认时记录旧合约、新合约、实际决策生效时间、对应源时间、依据和快照；不伪造“下个交易日开盘”的时间。生效时间不回填到早于本程序收到信息的时点。

状态中的 `Decision.PendingSymbol` 表示已取得收盘确认的待确认合约，`ProvisionalSymbol` 表示本次候选，两者可能不同。`CloseSnapshot.Accepted` 表示该日候选满足条件，不等于已确认主力。当前保留最近 64 个收盘审计快照和 100 个变更事件；长期审计应由持久层归档。仅观察首个收盘日期时，界面应同时显示“候选 c2701”和“1/2”，不能把未确认空值当数据错误。

## 6. 验证与剩余工作

已通过 `go test -race ./internal/market` 与 `go vet ./internal/market`。测试覆盖严格报价包装、中文编码、空合约、字段版本、非有限值、OHLC/量仓/时间错误、来源与大小限制、日期去重、双条件阈值、禁止向近月切换、交割月剔除、缺失原主力、夜盘、陈旧数据和状态不修改调用者数组。公开端点测试默认跳过，需显式运行：

```bash
cd backend
CORNWATCH_PUBLIC_QUOTE_PROBE=1 go test ./internal/market -run TestPublicLiveProbe -v
```

下一步优先完成授权 TqSdk 的真实账号验证、实际交易日历与会话状态、收盘数据更正版审计、历史结算数据导入。任何自动执行交易前仍需交易账户权限、风控与回报核对、断线恢复、成交模型和实盘影子验证；公开网页行情不承担这些保障。以上事项不影响本轮本地真实参考报价和研究主力可视化的使用。
