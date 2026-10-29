package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"cornwatch/internal/currentanalysis"
	"cornwatch/internal/marketdaily"
)

type DailyFetcher func(context.Context, string, time.Time) (marketdaily.Report, error)

func (s *Server) getCurrentAnalysis(w http.ResponseWriter, r *http.Request) {
	var report currentanalysis.Report
	err := s.Store.GetMeta("current_analysis", &report)
	if errors.Is(err, os.ErrNotExist) {
		write(w, 200, map[string]any{"report": nil})
		return
	}
	if err != nil {
		fail(w, 500, errors.New("读取当前分析失败"))
		return
	}
	write(w, 200, map[string]any{"report": report})
}
func (s *Server) refreshCurrentAnalysis(w http.ResponseWriter, r *http.Request) {
	if s.Config.Hub == nil {
		fail(w, 400, errors.New("真实数据管理尚未启用"))
		return
	}
	select {
	case s.current <- struct{}{}:
		defer func() { <-s.current }()
	default:
		fail(w, 429, errors.New("当前盘面分析正在运行"))
		return
	}
	now := time.Now().UTC()
	live := s.Config.Hub.Live()
	q, _, err := currentanalysis.Select(live, now)
	if err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	daily, err := s.DailyFetcher(ctx, q.Symbol, now)
	if err != nil {
		fail(w, 400, err)
		return
	}
	observations, err := s.Store.Observations()
	if err != nil {
		fail(w, 500, errors.New("读取产业记录失败"))
		return
	}
	report, err := currentanalysis.Build(live, daily, observations, time.Now().UTC())
	if err != nil {
		fail(w, 400, err)
		return
	}
	if err = s.Store.SetMeta("current_analysis", report); err != nil {
		fail(w, 500, errors.New("分析保存失败，已保留以前报告"))
		return
	}
	s.changed("current-analysis")
	write(w, 200, map[string]any{"report": report})
}
