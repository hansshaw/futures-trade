package server

import (
	"cornwatch/internal/quant"
	"cornwatch/internal/research"
	"errors"
	"net/http"
	"strings"
)

func (s *Server) marketResearch(w http.ResponseWriter, r *http.Request) {
	symbol := strings.ToLower(r.URL.Query().Get("symbol"))
	if !isCorn.MatchString(symbol) {
		fail(w, 400, errors.New("请选择实际玉米合约，如 c2701"))
		return
	}
	demo := r.URL.Query().Get("demo") == "true"
	bars, err := s.Store.Bars(symbol, demo)
	if err != nil {
		respond(w, nil, err)
		return
	}
	all, err := s.Store.Bars("", demo)
	if err != nil {
		respond(w, nil, err)
		return
	}
	report, err := research.Analyze(bars, all)
	if err != nil {
		fail(w, 400, err)
		return
	}
	write(w, 200, report)
}
func (s *Server) riskResearch(w http.ResponseWriter, r *http.Request) {
	var config research.RiskConfig
	if err := decode(w, r, &config); err != nil {
		fail(w, 400, err)
		return
	}
	result, err := research.SizeRisk(config)
	if err != nil {
		fail(w, 400, err)
		return
	}
	write(w, 200, result)
}
func (s *Server) validateResearch(w http.ResponseWriter, r *http.Request) {
	select {
	case s.backtests <- struct{}{}:
		defer func() { <-s.backtests }()
	default:
		fail(w, 429, errors.New("研究任务繁忙，请稍后重试"))
		return
	}
	var config quant.Config
	if err := decode(w, r, &config); err != nil {
		fail(w, 400, err)
		return
	}
	bars, err := s.Store.Bars(config.Symbol, config.Demo)
	if err != nil {
		respond(w, nil, err)
		return
	}
	report, err := research.ValidateStrategy(config, bars)
	if err != nil {
		fail(w, 400, err)
		return
	}
	write(w, 200, report)
}
