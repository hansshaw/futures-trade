package datahub

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cornwatch/internal/domain"
	"cornwatch/internal/market"
)

func marketPayload() bridgePayload {
	return bridgePayload{GeneratedAt: testNow, Quotes: exampleQuotes("producer", testNow.Add(-30*time.Minute))}
}
func industryPayload() bridgePayload {
	return bridgePayload{Observations: []domain.Observation{exampleObservation()}}
}
func bridgeServer(t *testing.T, body []byte, token string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Accept") != "application/json" {
			t.Error("wrong bridge request")
		}
		want := ""
		if token != "" {
			want = "Bearer " + token
		}
		if r.Header.Get("Authorization") != want {
			t.Errorf("authorization mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}
func TestBridgeMarketPreservesProducerTimes(t *testing.T) {
	p := marketPayload()
	p.Quotes = append(p.Quotes, market.Quote{Symbol: "M2612", Last: 3000, PreviousSettlement: 3010, ObservedAt: testNow.Add(-time.Minute), ReceivedAt: testNow.Add(-30 * time.Second)})
	p.Quotes = append(p.Quotes, market.Quote{Symbol: "m2708", Last: 3000, PreviousSettlement: 3010, ObservedAt: testNow.Add(-time.Minute)})
	body, _ := json.Marshal(p)
	server := bridgeServer(t, body, "test-secret")
	b, err := fetchBridge(context.Background(), server.Client(), Config{Endpoint: server.URL, Token: "test-secret"}, true, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Quotes) != 4 || b.Quotes[2].Symbol != "m2612" || b.Quotes[2].Product != "m" || b.Quotes[2].Exchange != "DCE" {
		t.Fatalf("quote normalization: %+v", b.Quotes)
	}
	if !b.Quotes[0].ReceivedAt.Equal(p.Quotes[0].ReceivedAt) || !b.Quotes[0].ObservedAt.Equal(p.Quotes[0].ObservedAt) {
		t.Fatal("producer timestamp overwritten")
	}
	if b.Quotes[3].ReceivedAt.Before(testNow) || b.Quotes[3].ReceivedAt.After(testNow.Add(time.Second)) {
		t.Fatal("missing producer time not recorded locally")
	}
	if b.Quotes[0].SourceURL != server.URL || b.Quotes[0].Source != "用户配置的授权行情桥接" || b.Quotes[0].ChangeBasis != "previous_settlement" {
		t.Fatal("source/basis normalization missing")
	}
	if math.Abs(b.Quotes[0].ChangePct-(2300.0/2290.0-1)*100) > 1e-10 {
		t.Fatal("settlement change incorrect")
	}
}
func TestBridgeMarketValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*bridgePayload)
	}{
		{"missing generated", func(p *bridgePayload) { p.GeneratedAt = time.Time{} }},
		{"stale generated", func(p *bridgePayload) { p.GeneratedAt = testNow.Add(-2*time.Minute - time.Nanosecond) }},
		{"future generated", func(p *bridgePayload) { p.GeneratedAt = testNow.Add(31 * time.Second) }},
		{"empty", func(p *bridgePayload) { p.Quotes = nil }},
		{"too many", func(p *bridgePayload) { p.Quotes = make([]market.Quote, 201) }},
		{"mixed record types", func(p *bridgePayload) { p.Observations = industryPayload().Observations }},
		{"duplicate", func(p *bridgePayload) { p.Quotes[1].Symbol = p.Quotes[0].Symbol }},
		{"continuous", func(p *bridgePayload) { p.Quotes[0].Symbol = "c0" }},
		{"corn august", func(p *bridgePayload) { p.Quotes[0].Symbol = "c2708" }},
		{"corn december", func(p *bridgePayload) { p.Quotes[0].Symbol = "c2612" }},
		{"starch december", func(p *bridgePayload) { p.Quotes[0].Symbol = "cs2612" }},
		{"hog august", func(p *bridgePayload) { p.Quotes[0].Symbol = "lh2708" }},
		{"unknown product", func(p *bridgePayload) { p.Quotes[0].Product = "m" }},
		{"exchange mismatch", func(p *bridgePayload) { p.Quotes[0].Exchange = "SHFE" }},
		{"missing observation time", func(p *bridgePayload) { p.Quotes[0].ObservedAt = time.Time{} }},
		{"future observation", func(p *bridgePayload) { p.Quotes[0].ObservedAt = testNow.Add(time.Minute) }},
		{"received before observed", func(p *bridgePayload) { p.Quotes[0].ReceivedAt = p.Quotes[0].ObservedAt.Add(-time.Nanosecond) }},
		{"future received", func(p *bridgePayload) { p.Quotes[0].ReceivedAt = testNow.Add(time.Minute) }},
		{"received after payload", func(p *bridgePayload) { p.GeneratedAt = testNow.Add(-time.Minute); p.Quotes[0].ReceivedAt = testNow }},
		{"zero price", func(p *bridgePayload) { p.Quotes[0].Last = 0 }},
		{"zero settlement", func(p *bridgePayload) { p.Quotes[0].PreviousSettlement = 0 }},
		{"overflow percentage", func(p *bridgePayload) { p.Quotes[0].PreviousSettlement = 1e-300; p.Quotes[0].Last = 1e9 }},
		{"negative volume", func(p *bridgePayload) { p.Quotes[0].Volume = -1 }},
		{"negative oi", func(p *bridgePayload) { p.Quotes[0].OpenInterest = -1 }},
		{"huge price", func(p *bridgePayload) { p.Quotes[0].High = 1e10 }},
		{"bad date", func(p *bridgePayload) { p.Quotes[0].TradeDate = "2026-02-31" }},
		{"long name", func(p *bridgePayload) { p.Quotes[0].Name = strings.Repeat("a", 101) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := marketPayload()
			tc.mutate(&p)
			body, _ := json.Marshal(p)
			s := bridgeServer(t, body, "")
			if _, err := fetchBridge(context.Background(), s.Client(), Config{Endpoint: s.URL}, true, testNow); err == nil {
				t.Fatal("invalid market payload accepted")
			}
		})
	}
	// Receipt fallback is local acquisition time, even if a valid payload was
	// generated a minute earlier. The original quote time is never changed.
	p := marketPayload()
	p.GeneratedAt = testNow.Add(-time.Minute)
	p.Quotes[0].ReceivedAt = time.Time{}
	body, _ := json.Marshal(p)
	s := bridgeServer(t, body, "")
	if _, err := fetchBridge(context.Background(), s.Client(), Config{Endpoint: s.URL}, true, testNow); err != nil {
		t.Fatalf("documented local receipt fallback: %v", err)
	}
}
func TestBridgeStrictJSONAndTransport(t *testing.T) {
	valid, _ := json.Marshal(marketPayload())
	for name, body := range map[string][]byte{
		"unknown top field":    []byte(strings.Replace(string(valid), `"quotes":`, `"extra":true,"quotes":`, 1)),
		"unknown nested field": []byte(strings.Replace(string(valid), `"symbol":`, `"extra":true,"symbol":`, 1)),
		"two objects":          append(append([]byte{}, valid...), []byte(` {}`)...),
		"truncated":            valid[:len(valid)-1],
		"oversized":            []byte(strings.Repeat(" ", 4*1024*1024+1)),
	} {
		t.Run(name, func(t *testing.T) {
			s := bridgeServer(t, body, "")
			if _, err := fetchBridge(context.Background(), s.Client(), Config{Endpoint: s.URL}, true, testNow); err == nil {
				t.Fatal("invalid JSON accepted")
			}
		})
	}
	t.Run("status body and secret redacted", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(401)
			_, _ = fmt.Fprint(w, "private-token detailed provider secret")
		}))
		defer s.Close()
		_, err := fetchBridge(context.Background(), s.Client(), Config{Endpoint: s.URL, Token: "private-token"}, true, testNow)
		if err == nil || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "detailed") {
			t.Fatalf("unsafe error %v", err)
		}
	})
	t.Run("redirect does not forward credentials", func(t *testing.T) {
		h, _, _ := testHub(t)
		seen := make(chan bool, 1)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- true; _, _ = w.Write(valid) }))
		defer target.Close()
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
		defer redirect.Close()
		saveBridge(t, h, "market_bridge", redirect.URL, "redirect-secret")
		if _, err := h.Run(context.Background(), "market_bridge", true); err == nil {
			t.Fatal("redirect accepted")
		}
		select {
		case <-seen:
			t.Fatal("redirect target contacted")
		default:
		}
	})
	for _, endpoint := range []string{"http://remote.example/path", "https://user:pass@example.com/data", "https://example.com/data?token=secret", "https://example.com/data#secret", "javascript:alert(1)", "https:///missing"} {
		if err := validateEndpoint(endpoint); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", endpoint)
		}
	}
}
func TestBridgeIndustryValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*bridgePayload)
	}{
		{"no observations", func(p *bridgePayload) { p.Observations = nil }},
		{"too many", func(p *bridgePayload) { p.Observations = make([]domain.Observation, 1001) }},
		{"mixed", func(p *bridgePayload) { p.Quotes = marketPayload().Quotes }},
		{"stale optional generated", func(p *bridgePayload) { p.GeneratedAt = testNow.Add(-3 * time.Minute) }},
		{"future optional generated", func(p *bridgePayload) { p.GeneratedAt = testNow.Add(time.Minute) }},
		{"unsupported kind", func(p *bridgePayload) { p.Observations[0].Kind = "weather" }},
		{"demo", func(p *bridgePayload) { p.Observations[0].IsDemo = true }},
		{"missing entity", func(p *bridgePayload) { p.Observations[0].Entity = "  " }},
		{"missing region", func(p *bridgePayload) { p.Observations[0].Region = "" }},
		{"missing source", func(p *bridgePayload) { p.Observations[0].Source = "" }},
		{"missing scope", func(p *bridgePayload) { p.Observations[0].Note = "" }},
		{"blank price unit", func(p *bridgePayload) { p.Observations[0].Kind = "purchase_price"; p.Observations[0].Unit = " " }},
		{"tonnes not trucks", func(p *bridgePayload) { p.Observations[0].Unit = "吨" }},
		{"fractional trucks", func(p *bridgePayload) { p.Observations[0].Value = 1.5 }},
		{"negative trucks", func(p *bridgePayload) { p.Observations[0].Value = -1 }},
		{"zero purchase price", func(p *bridgePayload) { p.Observations[0].Kind = "purchase_price"; p.Observations[0].Value = 0 }},
		{"long field", func(p *bridgePayload) { p.Observations[0].Entity = strings.Repeat("a", 201) }},
		{"missing time", func(p *bridgePayload) { p.Observations[0].ObservedAt = time.Time{} }},
		{"future observation", func(p *bridgePayload) { p.Observations[0].ObservedAt = testNow.Add(6 * time.Minute) }},
		{"source url missing host", func(p *bridgePayload) { p.Observations[0].SourceURL = "https:///bad" }},
		{"source url credentials", func(p *bridgePayload) { p.Observations[0].SourceURL = "https://secret@example.com/data" }},
		{"source url active content", func(p *bridgePayload) { p.Observations[0].SourceURL = "javascript:alert(1)" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := industryPayload()
			tc.mutate(&p)
			body, _ := json.Marshal(p)
			s := bridgeServer(t, body, "")
			if _, err := fetchBridge(context.Background(), s.Client(), Config{Endpoint: s.URL}, false, testNow); err == nil {
				t.Fatal("invalid industry payload accepted")
			}
		})
	}
}
func TestIndustryTokenRotationIsIdempotent(t *testing.T) {
	h, store, _ := testHub(t)
	p := industryPayload()
	p.Observations[0].Unit = "车"
	p.Observations[0].ID = "untrusted-provider-id"
	p.Observations[0].AvailableAt = testNow.Add(-24 * time.Hour)
	body, _ := json.Marshal(p)
	token := "one"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("new credential was not used")
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	saveBridge(t, h, "industry_bridge", server.URL, token)
	first, err := h.Run(context.Background(), "industry_bridge", false)
	if err != nil || first.Added != 1 {
		t.Fatalf("first: %+v %v", first, err)
	}
	token = "two"
	saveBridge(t, h, "industry_bridge", server.URL, token)
	h.now = func() time.Time { return testNow.Add(time.Minute) }
	second, err := h.Run(context.Background(), "industry_bridge", false)
	if err != nil || second.Added != 0 || second.Duplicates != 1 {
		t.Fatalf("rotation duplicate: %+v %v", second, err)
	}
	observations, err := store.Observations()
	if err != nil || len(observations) != 1 {
		t.Fatalf("store: %d %v", len(observations), err)
	}
	o := observations[0]
	if o.ID == "untrusted-provider-id" || o.Unit != "辆" || o.Source != "授权桥接 / 授权供应商" || !o.AvailableAt.Equal(testNow) {
		t.Fatalf("identity/first availability: %+v", o)
	}
	// A distinct bridge endpoint gets its own stable namespace.
	server2 := bridgeServer(t, body, "")
	b, err := fetchBridge(context.Background(), server2.Client(), Config{Endpoint: server2.URL}, false, testNow)
	if err != nil || b.Observations[0].ID == o.ID {
		t.Fatalf("provider namespaces merged: %v", err)
	}
}
func TestWeatherBatchUsesAcquisitionTimesAndResearchAlerts(t *testing.T) {
	o := exampleObservation()
	o.ID = "rain"
	o.Kind = "weather"
	o.Entity = "日降水预报 · 2026-10-30"
	o.Unit = "mm"
	o.Value = 25
	o.ObservedAt = testNow.Add(48 * time.Hour)
	o.AvailableAt = testNow
	below := o
	below.ID = "light-rain"
	below.Value = 24.9
	temp := o
	temp.ID = "temperature"
	temp.Unit = "°C"
	temp.Value = 30
	temp.AvailableAt = testNow.Add(time.Second)
	b := weatherBatch([]domain.Observation{o, below, temp})
	if len(b.Observations) != 3 || len(b.Events) != 1 || !b.DataAt.Equal(temp.AvailableAt) {
		t.Fatalf("weather batch: %+v", b)
	}
	e := b.Events[0]
	if !e.AvailableAt.Equal(o.AvailableAt) || !e.PublishedAt.Equal(o.AvailableAt) || !e.IngestedAt.Equal(o.AvailableAt) || e.Category != "weather" || e.Source != o.Source || e.SourceURL != o.SourceURL || !strings.Contains(e.Body, "不是官方气象预警") {
		t.Fatalf("weather event: %+v", e)
	}
	repeated := weatherBatch([]domain.Observation{o})
	if repeated.Events[0].ID != e.ID {
		t.Fatal("weather alert identity is not stable")
	}
}

func TestBridgeMissingReceiptUsesResponseCompletionNotRequestStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(25 * time.Millisecond)
		observed := time.Now().UTC()
		p := bridgePayload{GeneratedAt: observed, Quotes: []market.Quote{{Symbol: "c2701", Last: 2300, PreviousSettlement: 2290, Volume: 100, OpenInterest: 200, ObservedAt: observed}}}
		if err := json.NewEncoder(w).Encode(p); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	requestStart := time.Now().UTC()
	b, err := fetchBridge(context.Background(), server.Client(), Config{Endpoint: server.URL}, true, requestStart)
	completed := time.Now().UTC()
	if err != nil {
		t.Fatalf("fresh quote generated during request rejected: %v", err)
	}
	if len(b.Quotes) != 1 {
		t.Fatal("missing quote")
	}
	q := b.Quotes[0]
	// Compare against the payload's own timestamp without inventing a producer
	// receive time or moving observed_at to make the ordering pass.
	if !q.ObservedAt.After(requestStart) || q.ReceivedAt.Before(q.ObservedAt) || q.ReceivedAt.After(completed) {
		t.Fatalf("bad receipt fallback: request=%s observed=%s received=%s completed=%s", requestStart, q.ObservedAt, q.ReceivedAt, completed)
	}
}
