package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cornwatch/internal/currentanalysis"
	"cornwatch/internal/domain"
	"cornwatch/internal/marketdaily"
)

const currentPath = "/api/research/current"

// All fixtures remain in reviewServer's temporary database and local mock
// bridge; these tests never fetch public prices or mutate a user's history.
func currentAPIServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, hub := v3Server(t)
	now := time.Now().UTC()
	month := now.AddDate(0, 2, 0)
	if int(month.Month())%2 == 0 {
		month = month.AddDate(0, 1, 0)
	}
	near := month.Format("0601")
	far := month.AddDate(0, 4, 0).Format("0601")
	quotes := v3Quotes(now)
	for i, code := range []string{"c" + near, "c" + far, "cs" + near, "lh" + near, "m" + near} {
		quotes[i].Symbol = code
	}
	bridge, _ := v3Bridge(t, quotes)
	v3ConfigureBridge(t, hub, bridge.URL+"/quotes")
	response := reviewRequest(t, s, "/api/data-sources/market_bridge/sync", map[string]any{})
	if response.Code != 200 {
		t.Fatalf("mock quote setup: %d %s", response.Code, response.Body.String())
	}
	if bars, err := s.Store.Bars("", false); err != nil || len(bars) != 0 {
		t.Fatalf("quote sync wrote research snapshots into historical bars: %d %v", len(bars), err)
	}
	return s, "c" + near
}

func currentDailyFixture(symbol string, now time.Time) marketdaily.Report {
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	local := now.In(zone)
	end := time.Date(local.Year(), local.Month(), local.Day(), 15, 0, 0, 0, zone).AddDate(0, 0, -1)
	report := marketdaily.Report{Source: "isolated daily fixture", SourceURL: "https://example.invalid/daily", AvailableAt: now.Add(-time.Second), HasOpenInterest: true, HasSettlement: true, SHA256: strings.Repeat("b", 64), LastTradingDate: end.Format("2006-01-02")}
	for i := 0; i < 65; i++ {
		stamp := end.AddDate(0, 0, i-64)
		price := float64(2100 + i)
		report.Bars = append(report.Bars, domain.Bar{Symbol: symbol, Timestamp: stamp, TradeDate: stamp.Format("2006-01-02"), Open: price, High: price + 5, Low: price - 5, Close: price, Settlement: price - 1, Volume: int64(1000 + i), OpenInterest: int64(5000 + i)})
	}
	return report
}

func currentPOST(s *Server, authorization string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+currentPath, strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func currentRead(t *testing.T, s *Server) *currentanalysis.Report {
	t.Helper()
	w := v3Get(t, s, currentPath, "Bearer "+v3APIToken)
	if w.Code != 200 {
		t.Fatalf("read current analysis: %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Report *currentanalysis.Report `json:"report"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Report
}

func TestCurrentAnalysisRequiresAuthForReadAndRefresh(t *testing.T) {
	s, _ := v3Server(t)
	var calls atomic.Int32
	s.DailyFetcher = func(context.Context, string, time.Time) (marketdaily.Report, error) {
		calls.Add(1)
		return marketdaily.Report{}, errors.New("must not run")
	}
	for _, auth := range []string{"", "Bearer wrong-token"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			var w *httptest.ResponseRecorder
			if method == http.MethodGet {
				w = v3Get(t, s, currentPath, auth)
			} else {
				w = currentPOST(s, auth)
			}
			if w.Code != 401 {
				t.Fatalf("unauthenticated %s returned%d: %s", method, w.Code, w.Body.String())
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized request started data fetch")
	}
	if currentRead(t, s) != nil {
		t.Fatal("empty cache must return report:null")
	}
}

func TestCurrentAnalysisNeverSubstitutesDemoOrImportedBars(t *testing.T) {
	s, _ := v3Server(t)
	if err := s.Store.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	v3SeedCSV(t, s)
	var calls atomic.Int32
	s.DailyFetcher = func(context.Context, string, time.Time) (marketdaily.Report, error) {
		calls.Add(1)
		return marketdaily.Report{}, errors.New("must not run")
	}
	w := currentPOST(s, "Bearer "+v3APIToken)
	if w.Code != 400 || calls.Load() != 0 || currentRead(t, s) != nil {
		t.Fatalf("used historical/demo prices without a live source: %d %s", w.Code, w.Body.String())
	}
}

func TestCurrentAnalysisPersistsSnapshotWithoutMutatingBarsAndKeepsCacheOnFailure(t *testing.T) {
	s, symbol := currentAPIServer(t)
	if err := s.Store.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	v3SeedCSV(t, s)
	beforeReal, err := s.Store.Bars("", false)
	if err != nil {
		t.Fatal(err)
	}
	beforeDemo, err := s.Store.Bars("", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, future := range []bool{false, true} {
		id := "known-observation"
		available := now.Add(-time.Minute)
		if future {
			id = "not-yet-available"
			available = now.Add(time.Hour)
		}
		_, err = s.Store.AddObservations([]domain.Observation{{ID: id, Kind: "purchase_price", Entity: id, Region: "isolated-region", Unit: "元/吨", Value: 2100, Source: "test-official", ObservedAt: now.Add(-24 * time.Hour), AvailableAt: available}})
		if err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	var raw marketdaily.Report
	s.DailyFetcher = func(_ context.Context, got string, cutoff time.Time) (marketdaily.Report, error) {
		calls.Add(1)
		if got != symbol {
			return marketdaily.Report{}, fmt.Errorf("selected wrong contract %s", got)
		}
		raw = currentDailyFixture(got, cutoff)
		return raw, nil
	}
	w := currentPOST(s, "Bearer "+v3APIToken)
	if w.Code != 200 {
		t.Fatalf("refresh failed: %d %s", w.Code, w.Body.String())
	}
	first := currentRead(t, s)
	if first == nil || first.Symbol != symbol || first.Technical.IsDemo || first.Technical.BarCount != 65 || first.DailySHA256 != raw.SHA256 {
		t.Fatalf("wrong persisted report: %+v", first)
	}
	if len(first.Evidence) != 1 || first.Evidence[0].ID != "known-observation" {
		t.Fatalf("future/demo observation entered cache: %+v", first.Evidence)
	}
	if calls.Load() != 1 {
		t.Fatalf("GET refetched source, calls=%d", calls.Load())
	}
	beforeCache, _ := json.Marshal(first)
	for _, kind := range []string{"fetch_failure", "invalid_daily", "future_daily"} {
		s.DailyFetcher = func(_ context.Context, got string, cutoff time.Time) (marketdaily.Report, error) {
			if kind == "fetch_failure" {
				return marketdaily.Report{}, errors.New("isolated source unavailable")
			}
			d := currentDailyFixture(got, cutoff)
			if kind == "invalid_daily" {
				d.Bars[0].IsDemo = true
			} else {
				d.Bars[len(d.Bars)-1].Timestamp = cutoff.Add(24 * time.Hour)
			}
			return d, nil
		}
		w = currentPOST(s, "Bearer "+v3APIToken)
		if w.Code != 400 {
			t.Fatalf("%s should fail: %d %s", kind, w.Code, w.Body.String())
		}
		afterCache, _ := json.Marshal(currentRead(t, s))
		if string(beforeCache) != string(afterCache) {
			t.Fatalf("%s replaced last successful report", kind)
		}
	}
	afterReal, err := s.Store.Bars("", false)
	if err != nil {
		t.Fatal(err)
	}
	afterDemo, err := s.Store.Bars("", true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeReal, afterReal) || !reflect.DeepEqual(beforeDemo, afterDemo) {
		t.Fatal("quote/current-analysis path wrote into backtest bars")
	}
	if raw.Bars[len(raw.Bars)-1].Close == first.Snapshot.Last {
		t.Fatal("fixture needs distinct daily/snapshot values")
	}
	if first.Technical.Latest.Close != raw.Bars[len(raw.Bars)-1].Close {
		t.Fatal("snapshot replaced completed daily close")
	}
}

func TestCurrentAnalysisAllowsOnlyOneRefreshAndReleasesSlot(t *testing.T) {
	s, _ := currentAPIServer(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var calls atomic.Int32
	s.DailyFetcher = func(ctx context.Context, symbol string, now time.Time) (marketdaily.Report, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return currentDailyFixture(symbol, now), nil
		case <-ctx.Done():
			return marketdaily.Report{}, ctx.Err()
		}
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- currentPOST(s, "Bearer "+v3APIToken) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not enter fetcher")
	}
	w := currentPOST(s, "Bearer "+v3APIToken)
	if w.Code != 429 || calls.Load() != 1 {
		t.Fatalf("second refresh ran concurrently: %d calls=%d", w.Code, calls.Load())
	}
	if currentRead(t, s) != nil {
		t.Fatal("in-flight report was exposed as complete")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case w = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("first request failed to finish")
	}
	if w.Code != 200 {
		t.Fatalf("first request: %d %s", w.Code, w.Body.String())
	}
	w = currentPOST(s, "Bearer "+v3APIToken)
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("slot not released: %d calls=%d %s", w.Code, calls.Load(), w.Body.String())
	}
}
