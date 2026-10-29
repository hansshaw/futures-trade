# v0.4 公开产业、进口参考与当前盘面验收

日期：2026-10-28（北京时间）。Linux新版位于 `release/v0.4/linux-unpacked/cornwatch`，旧版本目录保留。测试均使用临时数据库和临时Electron用户目录，没有把测试记录写入日常库。

| 项目 | 已执行结果 |
| --- | --- |
| 后端回归 | `go test -race -json ./...`共388项测试/子测试通过；`go vet ./...`通过。新模块 industrypublic 30、importcorn 9、marketdaily 21、currentanalysis 33；datahub 84、server 20，其余191项为已有模块 |
| 构建 | TypeScript检查、Go生产构建、Vite与Electron独立打包通过，应用版本0.4.0 |
| HTTP及旧桌面 | `python3 scripts/smoke.py`及打包后的`electron-smoke.mjs`通过；包含导入、事件、回测、研究、日记截图、智能体、重启与草稿 |
| 数据控制台 | `data-ui-smoke.mjs`默认模式8组全部通过，9源配置、凭据不回显、409保留草稿、503保留旧数据、1080/1400视口检查通过 |
| 实际公开联网 | 22:46通过Electron逐源同步，7/7成功：26报价、44企业表观测+3事件、2到车+2事件、1拍卖+1事件、辽宁6观测+3事件、农业部9观测+3事件、45天气预报 |
| 实际分析 | UI生成C2701报告，186根实际完成日线，截至10/28 15:00；独立复算MA5/20/60、ATR14、RSI14、昨收/昨结涨跌、同期价差均一致 |
| 复盘操作 | 分析带入复盘后标题、假设、观察条件和输入指纹保持；本地草稿落盘、切页后恢复。测试未替用户保存正式日记 |
| 视觉核验 | 实际打包截图中的产业卡、进口卡、当前分析、复盘及1080/1400窗口人工查看；无横向页面溢出、无捕获到的渲染异常 |
| 时点与证据 | 未来日线/未来关联腿/未来可用产业证据拒绝；通知生效前只产生事件；报价未知单位不补元/吨；到车比较两期均进入证据和hash；日线来源、时间、覆盖能力也进入hash |

结果：[真实桌面报告](../artifacts/v4-ui/live-report.json)、[完整真实行情与产业证据](../artifacts/v4-ui/live-evidence.json)、[程序生成分析](../artifacts/v4-ui/live-current-analysis.json)、[独立专业盘面说明](current-market-analysis-2026-10-28.md)。当前日线收盘2224、MA20 2207.75、MA60 2243.65、ATR14 20.1417996731、RSI14 51.2231758479。采集于22:46的夜盘与完成日线分列，未混成同一根K线。

UI验收脚本曾因直接序列化DOM元素触发CDP对象深度错误；已将轮询条件强制转为布尔值，完整重跑通过。未用模拟数据替换真实请求。新来源均不需账号；进口公开结果仅披露成交率41%，没有吨数和均价，不代表新签海外采购或完整逐日进口成交。企业表单位未注明的44条记录保留原值，不进入基差/金额推算。限流、空公告、源结构变化与失败保留策略有离线测试。

本轮没有验证付费行情/LLM账号、完整进口成本与海外采购数据、Windows/macOS新版安装或云部署，也未验证策略盈利。来源许可、时效和缺口见[企业数据说明](public-enterprise-sources.md)、[进口数据说明](import-corn-sources.md)。接口增量见[v0.4契约](v4-api-contract.md)。

---

# v0.3 真实数据接入验收记录

日期：2026-10-28，Asia/Shanghai。Linux桌面独立构建于 `release/v0.3/linux-unpacked/cornwatch`；保留旧应用目录与用户原数据库。以下测试只使用临时数据库/临时Electron目录，不向日常库写入测试价格。

| 项目 | 实际执行结果 |
| --- | --- |
| Go回归及并发检查 | `go test -race -json ./...` 共280项测试/子测试通过：datahub73、market30、industry28、server16，原有analysis6、collector6、journal41、quant43、research33、store4；两个显式联网探测默认跳过 |
| 静态检查 | `go vet ./...`、`git diff --check`通过 |
| 前端与独立打包 | `npm run package -- --config.directories.output=release/v0.3`通过，包含TypeScript检查、Go生产构建、Vite及Electron打包 |
| 旧HTTP整合回归 | `python3 scripts/smoke.py`通过：鉴权、隔离、日K导入、事件/观察、回测、研究、复盘/图像/智能体与重启 |
| 可选TqSdk适配器 | 14项Python标准库mock测试通过；认证/未就绪503/时间/断线状态/只读调用覆盖；没有安装SDK或使用真实账号登录 |
| 来源生命周期 | 配置冲突、跨origin令牌清除、令牌轮换幂等、独立忙锁、试连接不入库、失败保留旧数据、持久化失败图表隔离、频率/退避/重启/取消均有自动测试 |
| 主力与展示隔离 | 不用C0推断实际合约；不同收盘日期才累计确认；过时数据不推进；单合约陈旧单独标识；四品种缺口不补演示价格；行情快照不写回测日线 |
| 数据质量 | 固定官方域/格式/口径验证；到车必须整数量且单位辆；严格JSON、来源URL、m08/m12、服务响应时间与行情原始接收时刻检查；天气研究阈值事件恢复 |
| 打包后旧桌面回归 | `node scripts/electron-smoke.mjs release/v0.3/linux-unpacked/cornwatch`全流程通过，包含图表、复盘、图片、规则智能体、冲突、重启、日K导入、事件、回测与SSE |
| 打包后新配置流程 | `node scripts/data-ui-smoke.mjs release/v0.3/linux-unpacked/cornwatch`全部8组检查通过；503失败保留、409配置草稿保护、令牌不回填与1080/1400窗口均通过 |
| 打包后真实联网界面 | 同脚本加`--live-public`四源全部通过；26报价、15官方观测+6事件、45天气。实际界面视检真实来源/时间、C2701候选确认1/2、5地区降水/温度、收购曲线、空到车状态均正确 |

真实联网HTTP探测已通过，报告为 `artifacts/live-data-probe.json`：新浪26个实际合约；辽宁6条观测+3事件；农业农村部9条观测+3事件；Open-Meteo45条预报（5点位×3日×3指标）。该次查询的公开源报价在15:04左右，不能当作18时的新成交；官方监测日期分别是10月27日、10月22日。没有把统计日期当作本地可得日期。

实际桌面验收的隔离mock脚本是 `scripts/data-ui-smoke.mjs`，真实公共源模式为 `--live-public`，结果见 `artifacts/v3-ui/live-report.json`；真实截图 `live-overview-1400.png`、`live-industry-1400.png`、`live-weather-1080.png`、`live-sources-1400.png` 均已人工视检。合成UI证据文件均用 `*synthetic.png` 命名，真实证据用 `live-*.png`，避免混淆。默认桌面验收覆盖六源配置、保存不回显令牌、切页保留草稿、自动关闭后仍能测试/同步、测试不入库、产业卡、503保留旧快照、409冲突处理、1080/1400窗口。

仍需实际账户或后续阶段验证：TqSdk/CTP登录与交易时段延迟、企业收购价/到车量真实供数、商用天气服务、历史订阅、官方节假日日历、连续换月账本、交易执行及云部署。本轮未构建Windows/macOS新版或发布远程Release，已有Windows打包配置保留。

---

# v0.2 期货研究与复盘验收记录

日期：2026-10-28，Asia/Shanghai。已生成新版应用目录 `release/v0.2/linux-unpacked/cornwatch`，保留之前 `release/linux-unpacked` 和正在运行的旧版，未停止用户应用、未修改用户日常数据库。

| 项目 | 实际执行结果 |
| --- | --- |
| 后端测试 | `go test -json ./...` 统计141项测试/子测试通过：analysis 6、collector 6、journal 41、quant 43、research 33、server 8、store 4 |
| 并发及静态检查 | `go test -race ./...`、`go vet ./...` 通过 |
| 前端与构建 | `npm run typecheck`、`npm run build` 通过；Electron 45 / React 19 / Go后端 |
| HTTP整合 | `python3 scripts/smoke.py` 通过：旧版全部验证，加盘面预热空值/同日期限结构/风险手数/70:30检验，以及日记图片、409、快照、经验采纳撤回、归档恢复、重启持久性 |
| 模型协议 | journal本机HTTP mock覆盖文本JSON、视觉content/data URL、默认不传图、未知引用/截断/超时/重定向拒绝、分析期间并发编辑仍保存原始输入快照；未调用付费外部模型 |
| Linux新版打包 | `npm run package -- --config.directories.output=release/v0.2` 通过，独立Go与UI资源包含在应用目录 |
| 打包后真实桌面 | `node scripts/electron-smoke.mjs release/v0.2/linux-unpacked/cornwatch` 全流程通过，启用原生沙箱，无Node权限暴露、无捕获到的渲染异常 |
| 桌面操作 | 日线指标、有效风险表单、当前应用图表截图→日记、图片文件上传/粘贴/放大、CtrlS、规则智能体、经验采纳与撤回、原始输入快照、跨窗口版本冲突保留本地草稿、已有事件/CSV/SSE/设置流程通过 |
| 重启与窗口 | 通过正常关窗退出后，以同一临时用户目录重启实际Electron；新的本地端口下恢复未保存草稿，已保存日记及3张图片保持；1080×800渲染视口无横向溢出 |
| 视觉核验 | 人工查看真实运行截图中的盘面、截图预览、日记、智能体、检验结果及紧凑视口；来源标记、长正文、指标图、操作区均可读，可滚动 |

截图：`artifacts/electron-research.png`、`electron-journal.png`、`electron-journal-image.png`、`electron-journal-agent.png`、`electron-journal-restart.png`、`electron-journal-compact.png`、`electron-backtest.png`。

本轮审查修正：原生数值表单step基准导致默认参数无法提交；历史K线选中与悬停数值不一致；SVG缩放坐标误差；跨月横轴合约标签被截断；未平仓与压力场景成交次数披露不足；空字符串时间戳使新建日记失败；图片按钮默认提交表单；随机端口使localStorage草稿跨启动不可见；IPC同步返回过早；HTTP409状态跨IPC丢失导致无法显示冲突处理入口；截图等待动画帧在遮挡窗口时停滞；截图异步与换页竞态；退出取消前过早杀后端；其他未落盘草稿的退出保护。

测试只使用独立合成样本、临时数据库和临时Electron目录，结束后清理。未测试真实交易执行、真实策略盈利能力、付费模型账户、授权行情/产业数据、云部署和Windows/macOS打包。时间切分固定参数，不是完整walk-forward；日终成交量过滤、收盘估值、缺少真实结算/动态保证金/换月/盘口撮合等边界仍存在，详见 [研究方案](trading-research.md)。

---

# 第一阶段验收记录

日期：2026-10-28，Asia/Shanghai。环境：Linux amd64，Intel Core i5-13400，Node 26.9.0，npm 11.19.1，Go 1.27.3，Electron 45.0.0。这里记录已执行的结果，不将未接入的数据源或未来交易能力算作完成。

| 项目 | 执行与结果 |
| --- | --- |
| Go 单元与回归测试 | `go test ./...` 通过，JSON日志统计63项测试/子测试通过；覆盖量化、主力、存储、API、采集解析及模型校验 |
| 并发检查 | `go test -race ./...` 通过 |
| 静态检查 | `go vet ./...` 通过 |
| 前端类型和生产构建 | `npm run typecheck`、`npm run build` 通过，Vite输出静态界面，Go二进制构建成功 |
| npm 依赖审计 | 本次 `npm audit` 返回0个已知漏洞；这不代表不存在尚未披露的问题 |
| 独立后端集成 | `python3 scripts/smoke.py` 通过：未授权拒绝、演示隔离、CSV原子导入与幂等、冲突拒绝、事件和观测首次可用时间、规则分析、未配置LLM错误、成本/指纹/版本输出、重启恢复 |
| 实际 Electron | `node scripts/electron-smoke.mjs` 通过，未使用 `--no-sandbox`；检查渲染端没有 Node `require` / `process`，通过私有IPC读取真实后端 |
| Linux 应用打包 | electron-builder成功生成 `release/linux-unpacked/cornwatch`，独立Go在 `resources/bin`，界面在 `resources/ui`，不依赖Go读取asar |
| 打包后桌面验收 | `node scripts/electron-smoke.mjs release/linux-unpacked/cornwatch` 通过：总览、回测、事件规则分析、事件表单、实际CSV文件选择与导入、SSE即时刷新、设置页；无捕获到的渲染异常 |
| 视觉检查 | 查看实际Electron总览、回测、事件详情和设置四张截图，文字/图表/表单显示完整，长内容可滚动；截图见 `artifacts/electron-*.png` |
| 真实天气网络请求 | 在独立临时数据库调用 `/api/collect/weather` 返回HTTP200、45条预报记录（5区域×3天×3指标）；测试库已删除 |
| 微基准 | 240根日K回测（含输入指纹）约0.362ms/次；7200根主力历史重建约8.50ms/次；仅本机函数基准，详见 [quant-model.md](quant-model.md) |

## 审查发现与修复

- 同一合约/交易日的唯一性原本只在HTTP处理器的事务外检查，并发写入可能绕过；已新增数据库事务内 `bar_days` 索引及回归测试。
- 重复观测原本返回新构造的可用时间，与数据库首次记录不一致；已返回原始持久记录及HTTP200。
- Feed同一发布时间的等价时区写法曾产生不同事件ID；已统一UTC后去重。
- 天气目标日期需要按Asia/Shanghai解析；已修复并验证。
- 带静态界面的路由注册曾触发Go ServeMux模式冲突；已修复并用实际临时index.html验证。
- Electron打包后的Go进程无法读取asar内静态文件；已将界面复制到独立resources/ui并通过打包后桌面测试。
- 长曲线采用参数展开求极值可能越过JS调用栈，数据切换后悬停索引可能越界；改为reduce、索引限制和保留局部极值的绘图抽样。
- 玉米合约验证限制为1、3、5、7、9、11月，拒绝c2702等非支持月份。

## 验收范围之外

本次没有使用真实LLM付费凭据；模型协议、错误和证据校验使用HTTP测试服务验证。没有接入真实CTP/TqSdk行情、付费产业数据或授权Feed实例，没有在云上部署或构建Docker镜像，没有验证Windows/macOS安装与签名。

回测样本是明确标识的合成数据和临时测试数据，未进行真实策略有效性检验。日末量的事后过滤、按收盘估值而非逐日结算、缺少完整交易日历与历史数据版本可得时间，均为已知模型边界。事件因子回测、真实换月、多品种组合、walk-forward、模拟交易和实盘订单尚未实现。

测试数据库和临时Electron用户目录在结束后清理；没有把集成测试价格、测试事件或天气结果写入用户日常数据库。用户首次正常启动看到固定演示数据，各项来源均有状态标识。
