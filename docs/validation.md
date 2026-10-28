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
