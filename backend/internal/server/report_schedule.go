package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"
)

type plannedReport struct {
	Kind        string    `json:"kind"`
	ScheduledAt time.Time `json:"scheduled_at"`
}

func reportDayAllowed(c reportSettings, day time.Time) bool {
	if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		return false
	}
	date := day.In(reportZone).Format("2006-01-02")
	for _, d := range c.SkipDates {
		if d == date {
			return false
		}
	}
	return true
}
func reportDue(c reportSettings, day time.Time, kind string) time.Time {
	clock := c.PreTime
	if kind == "post" {
		clock = c.PostTime
	}
	t, _ := time.Parse("15:04", clock)
	local := day.In(reportZone)
	return time.Date(local.Year(), local.Month(), local.Day(), t.Hour(), t.Minute(), 0, 0, reportZone)
}
func reportDeadline(c reportSettings, day time.Time, kind string) time.Time {
	due := reportDue(c, day, kind)
	end := due.Add(time.Hour)
	if kind == "pre" {
		local := day.In(reportZone)
		open := time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, reportZone)
		if open.Before(end) {
			end = open
		}
	}
	return end
}
func nextReportRuns(c reportSettings, now time.Time) []plannedReport {
	out := []plannedReport{}
	if !c.Enabled || !c.configured() || os.Getenv("CORNWATCH_REPORT_AUTO") == "false" {
		return out
	}
	for _, kind := range []string{"pre", "post"} {
		for i := 0; i < 380; i++ {
			day := now.In(reportZone).AddDate(0, 0, i)
			due := reportDue(c, day, kind)
			if reportDayAllowed(c, day) && due.After(now) {
				out = append(out, plannedReport{kind, due})
				break
			}
		}
	}
	return out
}
func (s *Server) recoverReportJobs() error {
	raws, err := s.Store.MetaPrefix("daily_report_job_", 100)
	if err != nil {
		return err
	}
	values := map[string]any{}
	for _, raw := range raws {
		var j reportJob
		if err = json.Unmarshal(raw, &j); err != nil {
			return err
		}
		if j.Status == "queued" || j.Status == "running" {
			j.Status = "interrupted"
			j.Stage = "上次退出时未完成"
			j.Error = "程序上次退出时任务未完成，未自动重复模型请求；可核对后手动重新生成。"
			j.UpdatedAt = time.Now().UTC()
			values["daily_report_job_"+j.ID] = j
		}
	}
	if len(values) == 0 {
		return nil
	}
	return s.Store.SetMetaBatch(values)
}
func (s *Server) tickReports(now time.Time) {
	if os.Getenv("CORNWATCH_REPORT_AUTO") == "false" {
		return
	}
	s.reportMu.Lock()
	c, _, err := s.loadReportSettings()
	busy := s.reportBusy
	s.reportMu.Unlock()
	if err != nil || busy || !c.Enabled || !c.configured() || !reportDayAllowed(c, now.In(reportZone)) {
		return
	}
	for _, kind := range []string{"pre", "post"} {
		due := reportDue(c, now, kind)
		if now.Before(due) {
			continue
		}
		day := now.In(reportZone).Format("2006-01-02")
		slot := "daily_report_slot_" + day + "_" + kind
		var old string
		err = s.Store.GetMeta(slot, &old)
		if err == nil {
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return
		}
		if !now.Before(reportDeadline(c, now, kind)) {
			s.reportMu.Lock()
			// Recheck the reservation under the same lock as manual/automatic starts.
			latest, _, configErr := s.loadReportSettings()
			valid := configErr == nil && !s.reportClosing && latest.Enabled && latest.configured() && reportDayAllowed(latest, now.In(reportZone)) && !now.Before(reportDeadline(latest, now, kind))
			if e := s.Store.GetMeta(slot, &old); valid && errors.Is(e, os.ErrNotExist) {
				id, e := reportID(now)
				if e == nil {
					j := reportJob{ID: id, Kind: kind, Date: day, Mode: "llm", Trigger: "schedule", Status: "missed", Stage: "已错过自动生成窗口", CreatedAt: now.UTC(), UpdatedAt: now.UTC(), Error: "未在自动窗口内运行（最多60分钟，盘前最晚09:00前）；不补造历史信息，可按当前时点手动生成。"}
					_ = s.Store.SetMetaBatch(map[string]any{slot: id, "daily_report_job_" + id: j})
				}
			}
			s.reportMu.Unlock()
			continue
		}
		_, _ = s.startReportJob(kind, "llm", "schedule", now)
	}
}
func (s *Server) pollReports(ctx context.Context) {
	defer s.reportCancel()
	s.tickReports(time.Now())
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.tickReports(now)
		}
	}
}

// WaitReports cancels jobs and shared history fetches that may have started
// before Poll began, then drains them before the caller closes databases.
func (s *Server) WaitReports() {
	s.reportMu.Lock()
	s.reportClosing = true
	if s.reportCancel != nil {
		s.reportCancel()
	}
	s.reportMu.Unlock()
	s.reportWorkers.Wait()
	if s.Chart != nil {
		s.Chart.Close()
	}
}
