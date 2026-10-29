# v0.5 每日报告接口增量

所有接口沿用本地Bearer鉴权与Electron私有IPC白名单。报告不写回测K线库，不下单。

## 设置

GET `/api/report-settings` → `{revision,base_url,model,key_configured,configured,enabled,pre_time,post_time,auto_save,skip_dates,timezone,window_minutes,source}`。

POST 同地址 → 完整上述可编辑配置，`api_key`只写、`clear_api_key`可选。不可提交`key_configured/configured/timezone/window_minutes/source`等服务端字段。成功返回脱敏设置；旧revision返回409。相同地址空key保留原密钥，改变基础地址则清除旧密钥；明确填写新密钥除外。

POST `/api/report-settings/test` `{}` → `{ok,message}`。只测试已保存配置，调用最小非市场示例、验证结构化JSON与原文引用。不保存报告；并发繁忙429。

## 后台任务与报告

- POST `/api/daily-reports/generate` `{kind:"pre"|"post",mode:"llm"|"rules"}` → 202 `{job}`；无模型配置/无数据管理400，并发忙429。
- GET `/api/daily-reports` → `{reports,jobs,busy,scheduler_paused,next_runs,scheduler_note}`。最近100份报告摘要、最近30项任务；没有外网请求。
- GET `/api/daily-reports/{id}` → 完整报告，含summary字段、`bundle`、`output`、`settings_revision`与AI时的`model_endpoint`；不含密钥。
- POST `/api/daily-reports/{id}/journal` `{}` → `{journal_id,already_saved}`；同报告重复调用返回已有日记，不覆盖其修改。

Job字段：`id,kind,date,mode,trigger,status,stage,created_at,updated_at,report_id?,error?`。status为queued/running/succeeded/failed/interrupted/missed；trigger为manual/schedule。`succeeded`可附日记保存失败的error，原报告仍有效，用户可单独重试保存日记。

ReportSummary字段：`id,kind,date,symbol,mode,summary,created_at,model,journal_id?,warnings`。完整Report的Bundle包含`as_of,kind,date,symbol,evidence,warnings,current,input_sha256`；Evidence包含`id,category,title,text,source,source_url,observed_at,available_at,is_forecast,unit?,note?,original_ids?`。Output结构见 `backend/internal/dailyreport/types.go`，身份与时间字段由服务端赋值。

报告及任务状态通过SSE `daily-reports`更新，设置通过`report-settings`、日记通过`journals`更新。后台任务独立于单次HTTP连接，150秒总超时；进程正常退出取消任务并等其完成状态写入后关闭库。崩溃恢复不自动重新发起已预留的模型请求。

## 调度与数据源增量

默认08:30/15:30北京时间、工作日加排除日期。时间窗最多60分钟且盘前截至09:00；每研究日/时段一次持久化预留，不自动重试失败模型请求。未接交易所假日日历。设置在预留任务锁内再次核验，避免刚停用/改时间后旧调度仍调用模型。

DataSource新增 `request_policy` 文本。同步结果新增 `cached`、`fetched_at`；缓存保留原数据可得时间。失败短时冷却返回429与Retry-After。内部 `Hub.RunScheduled` 结果可含`skipped`，报告和定时采集共用原配置间隔/退避。当前分析和日报共享日线请求短缓存，按实际合约、上海日期和15:15成熟边界区分。
