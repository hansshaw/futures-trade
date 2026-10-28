# v0.2 补充契约（实现协作）

沿用认证、JSON/GET/POST、错误 `{error}` 和 SSE update。既有接口不破坏。以下字段以 snake_case 命名；缺失指标返回 null，不能伪造0。

## 盘面研究

`GET /api/research/market?symbol=c2701&demo=true` 返回：

```
{symbol,is_demo,as_of,bar_count,points:[{timestamp,trade_date,open,high,low,close,volume,open_interest,ma5,ma20,ma60,atr14,rsi14,macd,macd_signal,macd_hist,prior_high20,prior_low20}],latest:{close,change_pct,oi_change,oi_change_pct,volume_ratio20,atr14,atr_pct,rsi14,ma20,ma60,prior_high20,prior_low20,trend,price_oi_label},insights:[{title,body,tone}],warnings:[],term_structure:[{symbol,trade_date,close,volume,open_interest}],spread:{near_symbol,far_symbol,near_minus_far,trade_date}|null}
```

`research.Analyze(bars []domain.Bar, allBars []domain.Bar) (MarketReport,error)`；仅选定玉米合约，allBars用于同期同demo的玉米期限结构。latest trend 文本中文，price_oi_label 必须解释为假设而非识别多/空资金。MA/ATR/RSI标准口径在文档披露，突破阈值必须排除当前bar。

`POST /api/research/risk` body `{equity,entry,stop,direction:"long"|"short",risk_pct,margin_rate,max_margin_pct,fee_per_lot,slippage_ticks}`，返回 `{risk_budget,loss_per_lot,margin_per_lot,risk_lots,margin_lots,max_lots,planned_loss,margin_used,notional,leverage,warnings:[]}`。玉米乘数10，tick1；损失包括来回手续费/滑点；仓位取风险预算和保证金占用预算较小者，资金为全账户权益。函数 `research.SizeRisk(RiskConfig)(RiskResult,error)`。

`POST /api/research/validate` body 同quant.Config（现有回测参数），root传bars。`research.ValidateStrategy(config quant.Config,bars []domain.Bar)(ValidationReport,error)`。返回 `{symbol,is_demo,split_date,train_bars,test_bars,segments:[{name,start,end,bar_count,metrics:quant.Metrics}],cost_scenarios:[{name,fee_per_lot,slippage_ticks,metrics:quant.Metrics}],warnings:[]}`。推荐最少80bar，前70%/后30%时间切分，不优化参数。独立平仓分段、测试集warmup来自前序训练数据，测试集不在warmup成交/计费；不冒称已完成walk-forward自动优化。根代理会提供UI展示。必要时可说明测试段独立slow_window warmup损失样本，但不要隐瞒。额外参数易兼容先同步。

## 日记数据

独立包 `journal`，根代理通过 `j,err:=journal.Open(path,a.Config)` 开启；`j.Close()`；`j.Register(mux, onChange func(string))` 注册路由；所有鉴权在外层server统一做。包不要编辑main/server/analysis已有文件。

Journal:
`{id,date,title,symbol,tags:[],plan,thesis,execution,review,lessons,emotion,discipline:number,pnl:number|null,attachment_ids:[],created_at,updated_at,revision:number,archived:boolean}`。date为上海复盘日期 YYYY-MM-DD。保存时校验字段长度与日期、有限数值。正文支持纯文本/Markdown存储但渲染不能执行HTML。

`GET /api/journals?q=&month=YYYY-MM` → Journal[]，默认排除archived。`GET /api/journals/{id}` → Journal。
`POST /api/journals` → Journal(201)；无id/revision的新建。
`POST /api/journals/{id}` → Journal，须revision与现有一致，否则409（编辑期间AI整理不能影响日记revision）。
`POST /api/journals/{id}/archive` body `{archived:boolean,revision:number}` → Journal，可恢复；GET列表 `archived=true`只看归档。

`POST /api/journal-assets` body `{name,data_url}` → `{id,name,mime,size,width,height,created_at}`；仅PNG/JPEG/WebP，magic与真实decode校验，单张4MiB、像素≤2400万，拒绝SVG/GIF，服务端随机文件名，文件0600；最多每篇6张，上传单独请求。可存bbolt bytes防路径风险，或sidecar安全目录。上传后替换为asset id而不是data URL存日记。
`GET /api/journal-assets/{id}` → `{id,name,mime,size,width,height,data_url}`，由IPC带认证获取；不能通过不鉴权静态路由取附件。

## 复盘学习智能体

`POST /api/journal-reviews` body `{journal_ids:string[],use_llm:boolean,include_images:boolean}`，最多20篇；快照读取、可选引用最多30条已采纳经验、单次分析、验证并存独立历史。响应 Review：
`{id,created_at,model,journal_ids:[],input_revisions:{id:number},summary,patterns:[{title,observation,journal_ids:[]}],actions:[],questions:[],risk_flags:[],image_count:number,lessons:[{id,text,evidence_ids:[],status:"candidate"|"accepted"}],input_sha256}`。
规则整理 use_llm=false 不冒充AI，按真实字段缺项/纪律/反复标签形成可核验待办，不伪造交易绩效；若无配置use_llm=true返回400。
截图默认不发模型；用户界面勾选include_images时带图（如未开启视觉模型支持，返回明确错误）。LLM使用现有LLM_*配置、JSON模式；不改变权重，不称模型训练。原始记录/截图不修改。仅允许引用所选Journal IDs；输出校验、限长度、并发/超时；模型输出是候选，经验采纳单独用户操作。分析结果保留输入版本和指纹，后续编辑不会使旧结论被误称最新。
`GET /api/journal-reviews` → 最新20个Review[]。
`POST /api/journal-reviews/{id}/lessons/{lesson_id}/accept` body `{}` → Lesson `{id,text,evidence_ids,status:"accepted",review_id,created_at}`；不可由模型自己采纳。
`GET /api/journal-lessons` → 已采纳Lesson[]，会在下一次分析中作为可追溯个人经验上下文，最多30个最近的。

GET /api/settings root增加 `journal:{available:true,vision_enabled:boolean}`（环境 LLM_VISION_ENABLED=true 才允许include_images，防文字模型误传图；UI同时声明所选文字/截图将发送到用户配置的模型服务）。

## 完成实现补充

- 时间切分报告额外包含 `config`、`input_sha256`、`engine_version`；当前版本为 `sma-daily-v2`。全样本回测也返回 `evaluation_start=0`；训练预热接口内部使用非零起点。
- `GET /api/journal-reviews/{id}/snapshot` 返回分析当时的 `journals`、`accepted_lessons`、`include_images` 和 `image_sha256`。快照不会随着当前日记或经验撤回而改变。
- `POST /api/journal-reviews` 可传 `use_accepted_lessons`，默认true；上下文最多包含最近30条采纳经验。响应包含 `context_lesson_ids`。
- `POST /api/journal-lessons/{id}/revoke {}` 幂等撤回采纳，返回candidate Lesson；同事务更新原报告候选状态并从后续上下文剔除，可以重新采纳，历史快照保留。
- 日记discipline为0..5整数，0表示未评分；PNL为空表示未记录。创建请求不应发送空字符串时间戳，创建/更新时间由后端赋值。每个正文字段上限12000 UTF-8字节。
- 图片单张4MiB/2400万像素，每篇6张；视觉任务最多8张不重复图片、合计8MiB原始数据；完整模型载荷12MiB。默认不读取或传输图像。
- Electron草稿通过窄IPC持久化到userData/journal-drafts.json，最大2MiB，0600文件权限、原子替换；Web开发模式使用localStorage。前端隐藏复盘页时保留组件，避免任务完成时覆盖新挂载草稿。
- Electron截图接口只截本应用当前可见区域，验证调用页面和矩形，拒绝4MiB以上图片并设置10秒超时。它不读取桌面、其他应用或屏幕。

所有journal路由均置于现有主服务认证中间件之内；截图只通过已认证JSON返回。图片移除和撤回经验不物理删除历史证据。
