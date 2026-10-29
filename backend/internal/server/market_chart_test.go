package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"cornwatch/internal/marketchart"
	"cornwatch/internal/marketdaily"
)

func historyGET(s *Server, path, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestHistoryAPIValidatesBeforeNetworkAndKeepsQuantSeparate(t *testing.T) {
	s := reviewServer(t, "history-test")
	t.Cleanup(s.Chart.Close)
	var calls atomic.Int32
	s.DailyFetcher = func(_ context.Context, symbol string, at time.Time) (marketdaily.Report, error) {
		calls.Add(1)
		return currentDailyFixture(symbol, at), nil
	}
	path := "/api/market/history?symbol=c2701&interval=day&limit=20"
	for _, auth := range []string{"", "Bearer wrong"} {
		if w := historyGET(s, path, auth); w.Code != 401 {
			t.Fatalf("auth %d", w.Code)
		}
	}
	for _, query := range []string{"symbol=C0", "symbol=c2701&symbol=cs2701", "symbol=c2701&interval=2m", "symbol=c2701&limit=0", "symbol=c2701&limit=6001", "symbol=c2701&limit=no", "symbol=c2701&unknown=1", "symbol=c2701&from=2026-02-30", "symbol=c2701&source=imported&interval=1m"} {
		w := historyGET(s, "/api/market/history?"+query, "Bearer history-test")
		if w.Code != 400 {
			t.Fatalf("invalid query %s: %d %s", query, w.Code, w.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request fetched")
	}
	w := historyGET(s, path, "Bearer history-test")
	if w.Code != 200 {
		t.Fatalf("valid %d %s", w.Code, w.Body.String())
	}
	var report marketchart.Report
	if e := json.Unmarshal(w.Body.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	if len(report.Bars) != 20 || report.TotalAvailable != 65 || report.Symbol != "c2701" || report.Bars[0].AvailableAt.IsZero() {
		t.Fatalf("wrong response %+v", report)
	}
	w = historyGET(s, path, "Bearer history-test")
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatal("query ignored cache")
	}
	bars, e := s.Store.Bars("", false)
	if e != nil || len(bars) != 0 {
		t.Fatal("history changed immutable backtest input")
	}
}
func TestHistoryImportedEmptyAndUnavailable(t *testing.T) {
	s := reviewServer(t, "")
	t.Cleanup(s.Chart.Close)
	w := historyGET(s, "/api/market/history?symbol=c2701&source=imported", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var report marketchart.Report
	if e := json.Unmarshal(w.Body.Bytes(), &report); e != nil || len(report.Bars) != 0 || report.Source != "本地导入真实日线" {
		t.Fatalf("%+v %v", report, e)
	}
	s.Chart = nil
	w = historyGET(s, "/api/market/history?symbol=c2701", "")
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
