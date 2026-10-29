package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cornwatch/internal/datahub"
	"cornwatch/internal/domain"
	"cornwatch/internal/market"
)

const v3APIToken = "v3-api-private-test-token"
const v3BridgeToken = "v3-authorized-bridge-private-token"

func v3Server(t *testing.T) (*Server, *datahub.Hub) {
	t.Helper()
	base := reviewServer(t, v3APIToken)
	hub, err := datahub.New(base.Store)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately never start Poll: all network traffic in these tests is one
	// explicit manual request to an authenticated httptest loopback bridge.
	return New(base.Store, base.Analyzer, Config{Token: v3APIToken, Hub: hub}), hub
}

func v3Get(t *testing.T, s *Server, path, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func v3ConfigureBridge(t *testing.T, hub *datahub.Hub, endpoint string) {
	t.Helper()
	var revision uint64
	for _, source := range hub.Sources() {
		if source.ID == "market_bridge" {
			revision = source.Config.Revision
		}
	}
	_, err := hub.Save("market_bridge", datahub.ConfigRequest{Revision: revision, Enabled: true, IntervalSeconds: 10, Endpoint: endpoint, Token: v3BridgeToken})
	if err != nil {
		t.Fatal(err)
	}
}

func v3Quotes(now time.Time) []market.Quote {
	china := time.FixedZone("Asia/Shanghai", 8*3600)
	result := make([]market.Quote, 0, 5)
	for i, item := range []struct{ symbol, product string }{{"c2701", "c"}, {"c2703", "c"}, {"cs2701", "cs"}, {"lh2701", "lh"}, {"m2612", "m"}} {
		last := float64(2200 + i*100)
		result = append(result, market.Quote{
			Symbol: item.symbol, Product: item.product, Name: "授权测试 " + item.symbol, Exchange: "DCE",
			TradeDate: now.In(china).Format("2006-01-02"), ObservedAt: now.Add(-2 * time.Second), ReceivedAt: now.Add(-time.Second),
			Source: "test licensed SDK", SourceURL: "https://example.invalid/authorized-source",
			Last: last, Open: last - 2, High: last + 3, Low: last - 3, PreviousSettlement: last - 20,
			ChangePct: 999, ChangeBasis: "untrusted_input_basis", Volume: int64(1000 - i*100), OpenInterest: int64(2000 - i*100),
		})
	}
	return result
}

func v3Bridge(t *testing.T, quotes []market.Quote) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/quotes" {
			http.Error(w, "unknown bridge route", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+v3BridgeToken {
			http.Error(w, "unauthorized bridge client", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"generated_at": time.Now().UTC(), "quotes": quotes})
	}))
	t.Cleanup(bridge.Close)
	return bridge, calls
}

func v3SeedCSV(t *testing.T, s *Server) {
	t.Helper()
	bars := []domain.Bar{}
	date := time.Date(2026, 9, 1, 7, 0, 0, 0, time.UTC)
	for _, symbol := range []string{"c2701", "cs2701", "lh2701", "m2701"} {
		bars = append(bars, domain.Bar{Symbol: symbol, Timestamp: date, TradeDate: "2026-09-01", Open: 7770, High: 7780, Low: 7760, Close: 7777, Settlement: 7770, Volume: 100, OpenInterest: 200})
	}
	if _, _, err := s.Store.ImportBars(bars, "licensed-test-csv"); err != nil {
		t.Fatal(err)
	}
}

type v3Dashboard struct {
	Mode       string              `json:"mode"`
	Quotes     []domain.Quote      `json:"quotes"`
	Main       domain.MainDecision `json:"main_contract"`
	MarketLive *datahub.Live       `json:"market_live"`
}

func v3DashboardResponse(t *testing.T, s *Server) v3Dashboard {
	t.Helper()
	w := v3Get(t, s, "/api/dashboard", "Bearer "+v3APIToken)
	if w.Code != 200 {
		t.Fatalf("dashboard status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), v3BridgeToken) || strings.Contains(w.Body.String(), v3APIToken) {
		t.Fatal("credential disclosed in dashboard")
	}
	var result v3Dashboard
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDataHubEndpointsRequireAPIAuthentication(t *testing.T) {
	s, hub := v3Server(t)
	// Saving a loopback endpoint does not connect to it.
	v3ConfigureBridge(t, hub, "http://127.0.0.1:8787/quotes")
	for _, path := range []string{"/api/data-sources", "/api/market/live"} {
		t.Run(path, func(t *testing.T) {
			for _, authorization := range []string{"", "Bearer wrong-token"} {
				w := v3Get(t, s, path, authorization)
				if w.Code != 401 {
					t.Fatalf("unauthorized %s returned %d: %s", path, w.Code, w.Body.String())
				}
			}
			w := v3Get(t, s, path, "Bearer "+v3APIToken)
			if w.Code != 200 || strings.Contains(w.Body.String(), v3APIToken) || strings.Contains(w.Body.String(), v3BridgeToken) {
				t.Fatalf("authenticated response leaked secret or failed: %d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, path := range []string{"/api/data-sources/market_bridge/config", "/api/data-sources/market_bridge/test", "/api/data-sources/market_bridge/sync"} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path, strings.NewReader("{}"))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("mutation bypassed auth %s: %d", path, w.Code)
		}
	}
}

func TestAuthorizedBridgeSyncReplacesDashboardWithFourRealCards(t *testing.T) {
	s, hub := v3Server(t)
	if err := s.Store.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	v3SeedCSV(t, s)
	if before := v3DashboardResponse(t, s); before.Mode != "imported" {
		t.Fatalf("test precondition: expected imported baseline, got %s", before.Mode)
	}
	input := v3Quotes(time.Now().UTC())
	bridge, calls := v3Bridge(t, input)
	v3ConfigureBridge(t, hub, bridge.URL+"/quotes")
	response := reviewRequest(t, s, "/api/data-sources/market_bridge/sync", map[string]any{})
	if response.Code != 200 {
		t.Fatalf("manual bridge sync: %d %s", response.Code, response.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly one manual loopback fetch, got %d", calls.Load())
	}
	result := v3DashboardResponse(t, s)
	if result.Mode != "authorized_quotes" || len(result.Quotes) != 4 || result.MarketLive == nil || result.MarketLive.SourceID != "market_bridge" || len(result.MarketLive.Quotes) != 5 {
		t.Fatalf("authorized feed not selected: %+v", result)
	}
	products := map[string]bool{}
	for _, q := range result.Quotes {
		products[q.Product] = true
		want := (q.Last/q.PreviousSettlement - 1) * 100
		if q.IsDemo || q.Last == 7777 || q.PreviousClose != 0 || q.ChangeBasis != "previous_settlement" || math.Abs(q.ChangePct-want) > 1e-9 || q.ChangePct == 999 {
			t.Fatalf("mixed historical/demo or incorrect change basis: %+v", q)
		}
		if !q.ReceivedAt.Equal(input[0].ReceivedAt) || !q.ObservedAt.Equal(input[0].ObservedAt) || q.SourceURL != bridge.URL+"/quotes" {
			t.Fatalf("original quote timing/provenance lost: %+v", q)
		}
	}
	for _, p := range []string{"c", "cs", "lh", "m"} {
		if !products[p] {
			t.Fatalf("missing product %s", p)
		}
	}
	var live datahub.Live
	w := v3Get(t, s, "/api/market/live", "Bearer "+v3APIToken)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &live) != nil || len(live.Quotes) != 5 || live.SourceID != "market_bridge" {
		t.Fatalf("live endpoint disagrees with dashboard: %d %s", w.Code, w.Body.String())
	}
	if len(live.PriceHistory["m2612"]) != 1 {
		t.Fatal("real December meal contract was lost")
	}
	bars, err := s.Store.Bars("", false)
	if err != nil || len(bars) != 4 {
		t.Fatalf("quote sync changed historical bars: %d %v", len(bars), err)
	}
}

func TestMissingAuthorizedProductsNeverFilledFromDemoOrCSV(t *testing.T) {
	for _, withCSV := range []bool{false, true} {
		name := "demo-baseline"
		if withCSV {
			name = "csv-and-demo-baseline"
		}
		t.Run(name, func(t *testing.T) {
			s, hub := v3Server(t)
			if err := s.Store.SeedDemo(); err != nil {
				t.Fatal(err)
			}
			if withCSV {
				v3SeedCSV(t, s)
			}
			quotes := v3Quotes(time.Now().UTC())[:1]
			bridge, calls := v3Bridge(t, quotes)
			v3ConfigureBridge(t, hub, bridge.URL+"/quotes")
			if _, err := hub.Run(context.Background(), "market_bridge", false); err != nil {
				t.Fatal(err)
			}
			result := v3DashboardResponse(t, s)
			if calls.Load() != 1 || result.Mode != "authorized_quotes" || len(result.Quotes) != 1 || result.Quotes[0].Product != "c" || result.Quotes[0].IsDemo || result.Quotes[0].Last != quotes[0].Last {
				t.Fatalf("missing products were filled with unrelated data: %+v", result)
			}
			if result.MarketLive == nil || len(result.MarketLive.Quotes) != 1 || result.Main.Symbol != "" {
				t.Fatal("one contract fabricated a confirmed main or merged a second source")
			}
		})
	}
}

func TestLiveCardsPreferConfirmedCornAndMarkIndividualStaleQuote(t *testing.T) {
	now := time.Date(2026, 10, 28, 6, 0, 0, 0, time.UTC)
	quotes := v3Quotes(now)
	quotes[0].Volume, quotes[0].OpenInterest = 10, 20
	quotes[0].ObservedAt = now.Add(-10 * time.Minute)
	quotes[1].Volume, quotes[1].OpenInterest = 10000, 20000
	live := datahub.Live{Quotes: quotes, LatestDataAt: &now, Freshness: "fresh", FreshnessNote: "batch recently updated", Main: market.State{Decision: domain.MainDecision{Symbol: "c2701"}, ProvisionalSymbol: "c2703"}}
	cards := liveCards(live)
	if len(cards) != 4 || cards[0].Symbol != "c2701" || cards[0].Freshness != "stale" || cards[0].FreshnessNote == live.FreshnessNote {
		t.Fatalf("confirmed corn priority/stale guard missing: %+v", cards)
	}
	for _, q := range cards[1:] {
		if q.Freshness != "fresh" {
			t.Fatalf("one stale contract contaminated another: %+v", q)
		}
	}
	// Without a confirmed main, the explicitly supplied provisional corn is
	// selected. With no actual quote for a product, no synthetic card appears.
	live.Main.Decision.Symbol = ""
	if provisional := liveCards(live); provisional[0].Symbol != "c2703" {
		t.Fatalf("provisional reference ignored: %+v", provisional)
	}
	live.Quotes = nil
	if empty := liveCards(live); len(empty) != 0 {
		t.Fatalf("no quotes should mean no cards: %+v", empty)
	}
}
