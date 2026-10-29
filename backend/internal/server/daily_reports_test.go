package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cornwatch/internal/analysis"
	"cornwatch/internal/dailyreport"
	"cornwatch/internal/journal"
	"cornwatch/internal/marketdaily"
)

func reportServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("CORNWATCH_AUTO_SYNC", "false")
	t.Setenv("CORNWATCH_REPORT_AUTO", "false")
	s, symbol := currentAPIServer(t)
	s.DailyFetcher = func(ctx context.Context, got string, now time.Time) (marketdaily.Report, error) {
		if got != symbol {
			t.Errorf("unexpected contract %s", got)
		}
		return currentDailyFixture(got, now), nil
	}
	j, err := journal.Open(filepath.Join(t.TempDir(), "notes.db"), analysis.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Journal = j
	t.Cleanup(func() { s.WaitReports(); _ = j.Close() })
	return s
}
func readJob(t *testing.T, s *Server, id string) reportJob {
	t.Helper()
	var j reportJob
	if err := s.Store.GetMeta("daily_report_job_"+id, &j); err != nil {
		t.Fatal(err)
	}
	return j
}
func waitReport(t *testing.T, s *Server, id string) reportJob {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		j := readJob(t, s, id)
		s.reportMu.Lock()
		busy := s.reportBusy
		s.reportMu.Unlock()
		if !busy && j.Status != "queued" && j.Status != "running" {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("report did not finish")
	return reportJob{}
}
func testModel(t *testing.T, bad bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer isolated-key" {
			t.Error("missing model auth")
		}
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("bad request")
			return
		}
		var data struct {
			Evidence []dailyreport.Evidence `json:"evidence"`
		}
		if len(request.Messages) < 2 || json.Unmarshal([]byte(request.Messages[1].Content), &data) != nil || len(data.Evidence) == 0 {
			t.Error("missing evidence")
			return
		}
		e := data.Evidence[0]
		id := e.ID
		if bad {
			id = "fabricated-id"
		}
		result := map[string]any{"summary": "隔离模型测试摘要，不是实际市场观点。", "sections": []any{map[string]any{"title": "证据检查", "body": "事实：输入有记录。推断：仍需人工核验。", "evidence_ids": []string{id}, "quotes": []any{map[string]string{"evidence_id": id, "text": e.Title}}}}, "scenarios": []any{}, "risks": []string{"隔离模型，仅验证工作流。"}}
		raw, _ := json.Marshal(result)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(h.Close)
	return h, calls
}
func configureReports(t *testing.T, s *Server, url string) reportSettings {
	t.Helper()
	c, _, err := s.loadReportSettings()
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = url
	c.Model = "isolated-mock"
	c.APIKey = "isolated-key"
	if err = s.Store.SetMeta("report_settings_v1", c); err != nil {
		t.Fatal(err)
	}
	return c
}
func TestReportSettingsCredentialsRevisionAndOrigin(t *testing.T) {
	s := reportServer(t)
	model, calls := testModel(t, false)
	c := configureReports(t, s, model.URL)
	response := v3Get(t, s, "/api/report-settings", "Bearer "+s.Config.Token)
	if response.Code != 200 || strings.Contains(response.Body.String(), "isolated-key") || !strings.Contains(response.Body.String(), `"key_configured":true`) {
		t.Fatal(response.Code, response.Body.String())
	}
	// Same-base blank key preserves it, and stale revisions cannot overwrite.
	c.APIKey = ""
	saved := reviewRequest(t, s, "/api/report-settings", c)
	if saved.Code != 200 {
		t.Fatal(saved.Body.String())
	}
	if strings.Contains(saved.Body.String(), "isolated-key") {
		t.Fatal("key leak")
	}
	if stale := reviewRequest(t, s, "/api/report-settings", c); stale.Code != 409 {
		t.Fatal("stale save accepted", stale.Code)
	}
	stored, _, _ := s.loadReportSettings()
	if stored.APIKey != "isolated-key" {
		t.Fatal("blank removed same-origin key")
	}
	test := reviewRequest(t, s, "/api/report-settings/test", map[string]any{})
	if test.Code != 200 || calls.Load() != 1 {
		t.Fatal(test.Code, test.Body.String())
	}
	docs, _ := s.Store.MetaPrefix("daily_report_doc_", 100)
	if len(docs) != 0 {
		t.Fatal("connection test stored a report")
	}
	stored.BaseURL = "https://new-provider.example/v1"
	stored.APIKey = ""
	response = reviewRequest(t, s, "/api/report-settings", stored)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	after, _, _ := s.loadReportSettings()
	if after.APIKey != "" {
		t.Fatal("old key crossed endpoints")
	}
	if deny := v3Get(t, s, "/api/report-settings", ""); deny.Code != 401 {
		t.Fatal("settings exposed without auth")
	}
}
func TestRulesDailyReportAndIdempotentJournal(t *testing.T) {
	s := reportServer(t)
	r := reviewRequest(t, s, "/api/daily-reports/generate", map[string]string{"kind": "pre", "mode": "rules"})
	if r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	var envelope struct {
		Job reportJob `json:"job"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &envelope)
	job := waitReport(t, s, envelope.Job.ID)
	if job.Status != "succeeded" {
		t.Fatal(job)
	}
	d, err := s.readDailyReport(job.ReportID)
	if err != nil || d.Mode != "rules" || d.Output.Mode != "rules" || d.Bundle.InputSHA256 == "" {
		t.Fatal(d, err)
	}
	notes, _ := s.Config.Journal.Journals("", "", false)
	if len(notes) != 0 {
		t.Fatal("rule preview auto-saved personal diary")
	}
	id, existing, err := s.saveReportJournal(d.ID)
	if err != nil || existing || id == "" {
		t.Fatal(id, existing, err)
	}
	note, _ := s.Config.Journal.Journal(id)
	note.Execution = "用户补充的实际执行"
	note.Title = "用户改写的标题"
	if _, err = s.Config.Journal.Save(note, id); err != nil {
		t.Fatal(err)
	}
	id2, existing, err := s.saveReportJournal(d.ID)
	if err != nil || !existing || id2 != id {
		t.Fatal(id2, existing, err)
	}
	note, _ = s.Config.Journal.Journal(id)
	if note.Title != "用户改写的标题" || note.Execution != "用户补充的实际执行" {
		t.Fatal("repeat save overwrote user edits")
	}
	notes, _ = s.Config.Journal.Journals("", "", false)
	if len(notes) != 1 {
		t.Fatal("duplicate report diary")
	}
	bars, _ := s.Store.Bars("", false)
	if len(bars) != 0 {
		t.Fatal("report contaminated backtest bars")
	}
}
func TestAIDailyReportAndDurableScheduleReservation(t *testing.T) {
	s := reportServer(t)
	t.Setenv("CORNWATCH_REPORT_AUTO", "true")
	model, calls := testModel(t, false)
	configureReports(t, s, model.URL)
	planned := time.Date(2026, 10, 29, 8, 35, 0, 0, reportZone)
	job, err := s.startReportJob("pre", "llm", "schedule", planned)
	if err != nil {
		t.Fatal(err)
	}
	j := waitReport(t, s, job.ID)
	if j.Status != "succeeded" || j.Error != "" {
		t.Fatal(j)
	}
	d, err := s.readDailyReport(j.ReportID)
	if err != nil || d.Mode != "llm" || d.JournalID == "" {
		t.Fatal(d.JournalID, err)
	}
	again, err := s.startReportJob("pre", "llm", "schedule", planned)
	if err != nil || again.ID != "" || calls.Load() != 1 {
		t.Fatal("duplicate paid request", again, err, calls.Load())
	}
	reloaded := New(s.Store, s.Analyzer, s.Config)
	t.Cleanup(reloaded.WaitReports)
	again, err = reloaded.startReportJob("pre", "llm", "schedule", planned)
	if err != nil || again.ID != "" || calls.Load() != 1 {
		t.Fatal("restart duplicated request")
	}
}
func TestInvalidModelReportDoesNotReplaceHistory(t *testing.T) {
	s := reportServer(t)
	job, err := s.startReportJob("post", "rules", "manual", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	previous := waitReport(t, s, job.ID)
	model, calls := testModel(t, true)
	configureReports(t, s, model.URL)
	job, err = s.startReportJob("post", "llm", "manual", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	failed := waitReport(t, s, job.ID)
	if failed.Status != "failed" || failed.ReportID != "" || calls.Load() != 1 {
		t.Fatal(failed)
	}
	docs, _ := s.Store.MetaPrefix("daily_report_doc_", 100)
	if len(docs) != 1 {
		t.Fatal("invalid output stored", len(docs))
	}
	if _, err = s.readDailyReport(previous.ReportID); err != nil {
		t.Fatal("prior report lost")
	}
	notes, _ := s.Config.Journal.Journals("", "", false)
	if len(notes) != 0 {
		t.Fatal("invalid report wrote diary")
	}
}
func TestReportScheduleDatesAndMissedWindow(t *testing.T) {
	s := reportServer(t)
	model, calls := testModel(t, false)
	c := configureReports(t, s, model.URL)
	t.Setenv("CORNWATCH_REPORT_AUTO", "true")
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, reportZone)
	s.tickReports(now)
	s.tickReports(now)
	raws, _ := s.Store.MetaPrefix("daily_report_job_", 100)
	if len(raws) != 1 || calls.Load() != 0 {
		t.Fatal("late task made request or repeated", len(raws), calls.Load())
	}
	var j reportJob
	_ = json.Unmarshal(raws[0], &j)
	if j.Status != "missed" {
		t.Fatal(j)
	}
	c.SkipDates = []string{"2026-10-30"}
	next := nextReportRuns(c, time.Date(2026, 10, 29, 20, 0, 0, 0, reportZone))
	if len(next) != 2 || next[0].ScheduledAt.Format("2006-01-02") != "2026-11-02" {
		t.Fatal(next)
	}
	c.Enabled = false
	if len(nextReportRuns(c, now)) != 0 {
		t.Fatal("disabled schedule had runs")
	}
}
func TestReportRecoveryInterruptsUnfinishedJob(t *testing.T) {
	s := reportServer(t)
	id, _ := reportID(time.Now())
	j := reportJob{ID: id, Status: "running", Mode: "llm", CreatedAt: time.Now()}
	if err := s.Store.SetMeta("daily_report_job_"+id, j); err != nil {
		t.Fatal(err)
	}
	reloaded := New(s.Store, s.Analyzer, s.Config)
	t.Cleanup(reloaded.WaitReports)
	got := readJob(t, reloaded, id)
	if got.Status != "interrupted" || got.Error == "" {
		t.Fatal(got)
	}
}

func TestScheduledStartRechecksLatestConfiguration(t *testing.T) {
	s := reportServer(t)
	t.Setenv("CORNWATCH_REPORT_AUTO", "true")
	model, calls := testModel(t, false)
	base := configureReports(t, s, model.URL)
	planned := time.Date(2026, 10, 29, 8, 35, 0, 0, reportZone)
	for _, scenario := range []struct {
		name   string
		change func(*reportSettings)
	}{
		{"disabled", func(c *reportSettings) { c.Enabled = false }},
		{"excluded", func(c *reportSettings) { c.SkipDates = []string{"2026-10-29"} }},
		{"moved later", func(c *reportSettings) { c.PreTime = "08:55" }},
		{"moved earlier outside grace", func(c *reportSettings) { c.PreTime = "06:00" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			c := base
			scenario.change(&c)
			if err := s.Store.SetMeta("report_settings_v1", c); err != nil {
				t.Fatal(err)
			}
			job, err := s.startReportJob("pre", "llm", "schedule", planned)
			if err != nil || job.ID != "" {
				t.Fatal(job, err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("disabled/rescheduled task billed model")
	}
	jobs, _ := s.Store.MetaPrefix("daily_report_job_", 100)
	if len(jobs) != 0 {
		t.Fatal("skipped schedule consumed slot")
	}
}

func TestReportBusyAndShutdownCancellation(t *testing.T) {
	s := reportServer(t)
	entered := make(chan struct{})
	s.DailyFetcher = func(ctx context.Context, _ string, _ time.Time) (marketdaily.Report, error) {
		close(entered)
		<-ctx.Done()
		return marketdaily.Report{}, ctx.Err()
	}
	job, err := s.startReportJob("pre", "rules", "manual", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	second := reviewRequest(t, s, "/api/daily-reports/generate", map[string]string{"kind": "post", "mode": "rules"})
	if second.Code != 429 {
		t.Fatal("concurrent generation allowed", second.Code)
	}
	s.WaitReports()
	j := readJob(t, s, job.ID)
	if j.Status != "interrupted" {
		t.Fatal(j)
	}
	if _, err = s.startReportJob("post", "rules", "manual", time.Now()); err == nil {
		t.Fatal("new work accepted during shutdown")
	}
}
