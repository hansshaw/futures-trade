package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"cornwatch/internal/currentanalysis"
	"cornwatch/internal/dailyreport"
	"cornwatch/internal/journal"
)

type reportJob struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Date      string    `json:"date"`
	Mode      string    `json:"mode"`
	Trigger   string    `json:"trigger"`
	Status    string    `json:"status"`
	Stage     string    `json:"stage"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ReportID  string    `json:"report_id,omitempty"`
	Error     string    `json:"error,omitempty"`
}
type reportSummary struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Date      string    `json:"date"`
	Symbol    string    `json:"symbol"`
	Mode      string    `json:"mode"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
	Model     string    `json:"model"`
	JournalID string    `json:"journal_id,omitempty"`
	Warnings  []string  `json:"warnings"`
}
type savedDailyReport struct {
	reportSummary
	ModelEndpoint    string             `json:"model_endpoint,omitempty"`
	SettingsRevision uint64             `json:"settings_revision"`
	Bundle           dailyreport.Bundle `json:"bundle"`
	Output           dailyreport.Output `json:"output"`
}

var reportIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}([0-9]{9})?_[a-f0-9]{16}$`)

func reportID(now time.Time) (string, error) {
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return now.UTC().Format("20060102T150405") + fmt.Sprintf("%09d", now.Nanosecond()) + "_" + hex.EncodeToString(b), nil
}
func safeReportError(err error, key string) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if key != "" {
		s = strings.ReplaceAll(s, key, "[凭据已隐藏]")
	}
	r := []rune(s)
	if len(r) > 500 {
		s = string(r[:500])
	}
	return s
}
func (s *Server) dailyReportList(w http.ResponseWriter, r *http.Request) {
	docs, err := s.Store.MetaPrefix("daily_report_doc_", 100)
	if err != nil {
		fail(w, 500, errors.New("读取报告失败"))
		return
	}
	reports := []reportSummary{}
	for _, raw := range docs {
		var d reportSummary
		if json.Unmarshal(raw, &d) != nil {
			fail(w, 500, errors.New("报告记录损坏"))
			return
		}
		reports = append(reports, d)
	}
	rawJobs, err := s.Store.MetaPrefix("daily_report_job_", 30)
	if err != nil {
		fail(w, 500, errors.New("读取任务失败"))
		return
	}
	jobs := []reportJob{}
	for _, raw := range rawJobs {
		var j reportJob
		if json.Unmarshal(raw, &j) != nil {
			fail(w, 500, errors.New("任务记录损坏"))
			return
		}
		jobs = append(jobs, j)
	}
	s.reportMu.Lock()
	busy := s.reportBusy
	c, _, err := s.loadReportSettings()
	s.reportMu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	write(w, 200, map[string]any{"reports": reports, "jobs": jobs, "busy": busy, "scheduler_paused": os.Getenv("CORNWATCH_REPORT_AUTO") == "false", "next_runs": nextReportRuns(c, time.Now()), "scheduler_note": "北京时间周一至周五，另排除已配置日期；未接交易所节假日日历。程序需运行。每时段最多在计划时刻后60分钟内补执行，盘前最晚09:00前；跨时段不回填历史；自动任务每天每时段最多一次，不自动重试模型费用。历史列表显示最近100份，原报告仍保留。"})
}
func (s *Server) readDailyReport(id string) (savedDailyReport, error) {
	var d savedDailyReport
	if !reportIDPattern.MatchString(id) {
		return d, os.ErrNotExist
	}
	err := s.Store.GetMeta("daily_report_doc_"+id, &d)
	return d, err
}
func (s *Server) dailyReportDetail(w http.ResponseWriter, r *http.Request) {
	d, err := s.readDailyReport(r.PathValue("id"))
	if err != nil {
		fail(w, 404, errors.New("报告不存在或无法读取"))
		return
	}
	write(w, 200, d)
}
func (s *Server) generateDailyReport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
		Mode string `json:"mode"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if req.Kind != "pre" && req.Kind != "post" || req.Mode != "llm" && req.Mode != "rules" {
		fail(w, 400, errors.New("请选择盘前/盘后及AI/规则模式"))
		return
	}
	job, err := s.startReportJob(req.Kind, req.Mode, "manual", time.Now())
	if err != nil {
		status := 400
		if errors.Is(err, errReportBusy) {
			status = 429
		}
		fail(w, status, err)
		return
	}
	write(w, 202, map[string]any{"job": job})
}

// startReportJob reserves before launching. Persisted schedule slots survive
// crashes, and a subsequent startup never blindly repeats a paid request.
func (s *Server) startReportJob(kind, mode, trigger string, now time.Time) (reportJob, error) {
	s.reportMu.Lock()
	defer s.reportMu.Unlock()
	if s.reportClosing {
		return reportJob{}, errors.New("程序正在退出")
	}
	if s.reportInitErr != nil {
		return reportJob{}, errors.New("上次日报任务状态恢复失败，请检查数据库")
	}
	if s.reportBusy {
		return reportJob{}, errReportBusy
	}
	c, _, err := s.loadReportSettings()
	if err != nil {
		return reportJob{}, err
	}
	if trigger == "schedule" {
		due := reportDue(c, now, kind)
		if os.Getenv("CORNWATCH_REPORT_AUTO") == "false" || !c.Enabled || !c.configured() || !reportDayAllowed(c, now.In(reportZone)) || now.Before(due) || !now.Before(reportDeadline(c, now, kind)) {
			return reportJob{}, nil
		}
	}
	if mode == "llm" && !c.configured() {
		return reportJob{}, errors.New("请在每日分析中保存大模型地址与模型名称；无账号可先使用规则预览")
	}
	if s.Config.Hub == nil {
		return reportJob{}, errors.New("真实数据管理未启用")
	}
	day := now.In(reportZone).Format("2006-01-02")
	if trigger == "schedule" {
		var old string
		e := s.Store.GetMeta("daily_report_slot_"+day+"_"+kind, &old)
		if e == nil {
			return reportJob{}, nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return reportJob{}, e
		}
	}
	id, err := reportID(now)
	if err != nil {
		return reportJob{}, errors.New("生成任务标识失败")
	}
	job := reportJob{ID: id, Kind: kind, Date: day, Mode: mode, Trigger: trigger, Status: "queued", Stage: "等待整理证据", CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	values := map[string]any{"daily_report_job_" + id: job}
	if trigger == "schedule" {
		values["daily_report_slot_"+day+"_"+kind] = id
	}
	if err = s.Store.SetMetaBatch(values); err != nil {
		return reportJob{}, errors.New("任务未能保存，没有调用模型")
	}
	s.reportBusy = true
	parent := s.reportContext
	if parent == nil {
		parent = context.Background()
	}
	s.reportWorkers.Add(1)
	go func() { defer s.reportWorkers.Done(); s.runReportJob(parent, job, c) }()
	return job, nil
}
func (s *Server) updateReportJob(j reportJob) error {
	j.UpdatedAt = time.Now().UTC()
	err := s.Store.SetMeta("daily_report_job_"+j.ID, j)
	if err == nil {
		s.changed("daily-reports")
	}
	return err
}
func (s *Server) runReportJob(parent context.Context, job reportJob, c reportSettings) {
	defer func() { s.reportMu.Lock(); s.reportBusy = false; s.reportMu.Unlock(); s.changed("daily-reports") }()
	ctx, cancel := context.WithTimeout(parent, 150*time.Second)
	defer cancel()
	failJob := func(err error) {
		job.Status = "failed"
		job.Stage = "任务未完成"
		job.Error = safeReportError(err, c.APIKey)
		if errors.Is(ctx.Err(), context.Canceled) {
			job.Status = "interrupted"
			job.Stage = "程序关闭，任务中断"
		}
		_ = s.updateReportJob(job)
	}
	job.Status = "running"
	job.Stage = "按采集计划检查数据并整理证据"
	if err := s.updateReportJob(job); err != nil {
		return
	}
	warnings := s.refreshReportSources(ctx)
	live := s.Config.Hub.Live()
	now := time.Now().UTC()
	q, _, err := currentanalysis.Select(live, now)
	if err != nil {
		failJob(err)
		return
	}
	daily, err := s.DailyFetcher(ctx, q.Symbol, now)
	if err != nil {
		failJob(err)
		return
	}
	observations, err := s.Store.Observations()
	if err != nil {
		failJob(errors.New("读取产业证据失败"))
		return
	}
	events, err := s.Store.Events()
	if err != nil {
		failJob(errors.New("读取事件证据失败"))
		return
	}
	cutoff := time.Now().UTC()
	current, err := currentanalysis.Build(live, daily, observations, cutoff)
	if err != nil {
		failJob(err)
		return
	}
	current.Warnings = append(current.Warnings, warnings...)
	local := cutoff.In(reportZone)
	if job.Kind == "pre" && (local.Hour()*60+local.Minute() >= 540) {
		current.Warnings = append(current.Warnings, "本次盘前格式报告在09:00后生成，是当前时点补充研究，不是可回溯为开盘前已知的报告。")
	}
	if job.Kind == "post" && (local.Hour()*60+local.Minute() < 915) {
		current.Warnings = append(current.Warnings, "本次盘后格式报告在15:15前生成，使用此前完成日线，不能当作今日完整收盘复盘。")
	}
	current.Warnings = append(current.Warnings, "日程按北京时间研究日期组织，未用交易所日历确认假日和夜盘交易日。")
	bundle, err := dailyreport.BuildWithQuotes(job.Kind, job.Date, &current, observations, events, live.Quotes, cutoff)
	if err != nil {
		failJob(err)
		return
	}
	job.Stage = "生成规则预览"
	if job.Mode == "llm" {
		job.Stage = "大模型解读与证据引用校验"
	}
	if err = s.updateReportJob(job); err != nil {
		return
	}
	var output dailyreport.Output
	if job.Mode == "rules" {
		output = dailyreport.Rules(bundle)
	} else {
		var g *dailyreport.Generator
		g, err = dailyreport.New(c.llm())
		if err == nil {
			output, err = g.Generate(ctx, bundle)
		}
	}
	if err != nil {
		failJob(err)
		return
	}
	d := savedDailyReport{reportSummary: reportSummary{ID: job.ID, Kind: job.Kind, Date: job.Date, Symbol: bundle.Symbol, Mode: job.Mode, Summary: output.Summary, CreatedAt: time.Now().UTC(), Model: output.Model, Warnings: append([]string{}, bundle.Warnings...)}, Bundle: bundle, Output: output}
	d.SettingsRevision = c.Revision
	if job.Mode == "llm" {
		d.ModelEndpoint = c.BaseURL
	}
	job.Status = "succeeded"
	job.Stage = "报告已保存"
	job.ReportID = d.ID
	job.UpdatedAt = time.Now().UTC()
	if c.AutoSave && job.Mode == "llm" {
		job.Status = "running"
		job.Stage = "报告已保存，正在保存复盘"
	}
	if err = s.Store.SetMetaBatch(map[string]any{"daily_report_doc_" + d.ID: d, "daily_report_job_" + job.ID: job}); err != nil {
		failJob(errors.New("报告持久化失败；模型可能已产生费用，请先核验再重试"))
		return
	}
	if c.AutoSave && job.Mode == "llm" {
		_, _, err = s.saveReportJournal(d.ID)
		job.Status = "succeeded"
		job.Stage = "报告与复盘已保存"
		if err != nil {
			job.Error = "报告已生成，但自动保存日记失败：" + safeReportError(err, c.APIKey)
			job.Stage = "报告已保存，日记需重试"
		}
		_ = s.updateReportJob(job)
	}
	s.changed("daily-reports")
}
func (s *Server) refreshReportSources(ctx context.Context) []string {
	if s.Config.Hub == nil {
		return []string{"数据采集未启用"}
	}
	sub, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	warnings := []string{}
	for _, source := range s.Config.Hub.Sources() {
		if !source.Config.Enabled {
			continue
		}
		source := source
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.Config.Hub.RunScheduled(sub, source.ID)
			if err != nil || (out.Skipped && source.LastError != "") {
				mu.Lock()
				warnings = append(warnings, source.Name+"：本次刷新未成功，按已存证据继续；请核对统计日和来源状态。")
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return warnings
}
func reportTextLimit(s string) string {
	r := []rune(s)
	if len(r) > 2300 {
		return string(r[:2300]) + "\n（文字已摘要，完整依据请在每日分析中打开原报告。）"
	}
	return s
}
func (s *Server) saveReportJournal(id string) (string, bool, error) {
	s.reportJournalMu.Lock()
	defer s.reportJournalMu.Unlock()
	if s.Config.Journal == nil {
		return "", false, errors.New("复盘日记未启用")
	}
	d, err := s.readDailyReport(id)
	if err != nil {
		return "", false, errors.New("报告不存在")
	}
	title := map[string]string{"pre": "盘前分析", "post": "盘后分析"}[d.Kind]
	label := "AI"
	if d.Mode == "rules" {
		label = "规则预览"
	}
	thesis := []string{d.Output.Summary}
	for _, x := range d.Output.Sections {
		thesis = append(thesis, x.Title+"："+x.Body+"\n依据："+strings.Join(x.EvidenceIDs, ", "))
	}
	plan := []string{}
	for _, x := range d.Output.Scenarios {
		plan = append(plan, x.Name+"："+x.Condition+"\n失效/反证："+x.Invalidation)
	}
	audit := fmt.Sprintf("%s · %s · %s\n生成时刻：%s\n证据截止：%s\n模型：%s\n报告ID：%s\n输入指纹：%s\n", d.Date, title, label, d.CreatedAt.In(reportZone).Format(time.RFC3339), d.Bundle.AsOf.In(reportZone).Format(time.RFC3339), d.Model, d.ID, d.Bundle.InputSHA256)
	refs := []string{}
	for _, e := range d.Bundle.Evidence {
		refs = append(refs, fmt.Sprintf("[%s] %s | 观测/目标日 %s | 可用 %s | %s", e.ID, e.Title, e.ObservedAt.In(reportZone).Format(time.RFC3339), e.AvailableAt.In(reportZone).Format(time.RFC3339), e.SourceURL))
	}
	j := journal.Journal{Date: d.Date, Title: d.Symbol + " " + d.Date + " " + title + " · " + label, Symbol: d.Symbol, Tags: []string{"每日分析", title, label}, Plan: reportTextLimit(strings.Join(plan, "\n\n")), Thesis: reportTextLimit(audit + "\n" + strings.Join(thesis, "\n\n")), Review: reportTextLimit("机器整理，待人工复核；未填写实际执行、盈亏或个人经验。\n\n风险与缺口：\n" + strings.Join(append(append([]string{}, d.Output.Risks...), d.Warnings...), "\n")), Lessons: "", Execution: "", Emotion: ""}
	// Preserve a compact evidence manifest in a separate field, keeping execution
	// and learned lessons empty. The immutable full bundle stays with the report.
	j.Review = reportTextLimit(j.Review + "\n\n依据目录：\n" + strings.Join(refs, "\n"))
	saved, existing, err := s.Config.Journal.SaveGenerated(j, d.ID)
	if err != nil {
		return "", false, err
	}
	d.JournalID = saved.ID
	if err = s.Store.SetMeta("daily_report_doc_"+id, d); err != nil {
		return saved.ID, existing, errors.New("日记已保存，报告关联状态未能写入；重试不会重复创建")
	}
	s.changed("journals")
	s.changed("daily-reports")
	return saved.ID, existing, nil
}
func (s *Server) dailyReportJournal(w http.ResponseWriter, r *http.Request) {
	id, existing, err := s.saveReportJournal(r.PathValue("id"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	write(w, 200, map[string]any{"journal_id": id, "already_saved": existing})
}
