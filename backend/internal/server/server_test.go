package server

import (
	"bufio"
	"bytes"
	"context"
	"cornwatch/internal/analysis"
	"cornwatch/internal/domain"
	"cornwatch/internal/store"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reviewServer(t *testing.T, token string) *Server {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a, err := analysis.New(analysis.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return New(s, a, Config{Token: token})
}

func reviewRequest(t *testing.T, s *Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	if s.Config.Token != "" {
		r.Header.Set("Authorization", "Bearer "+s.Config.Token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

const reviewCSVHeader = "symbol,timestamp,trade_date,open,high,low,close,settlement,volume,open_interest,limit_up,limit_down\n"

func TestInvalidCSVNeverPartiallyImports(t *testing.T) {
	s := reviewServer(t, "")
	csv := reviewCSVHeader + "c2701,2026-09-01T15:00:00+08:00,2026-09-01,2200,2220,2190,2210,2205,1000,2000,0,0\n" +
		"c2701,2026-09-02T15:00:00+08:00,2026-09-02,2200,2220,2190,not-a-price,2205,1000,2000,0,0\n"
	w := reviewRequest(t, s, "/api/import/bars", map[string]string{"csv": csv, "source": "test licensed data"})
	if w.Code != 400 {
		t.Fatalf("want 400, got %d %s", w.Code, w.Body.String())
	}
	bars, err := s.Store.Bars("", false)
	if err != nil || len(bars) != 0 {
		t.Fatalf("invalid file partially committed: %+v %v", bars, err)
	}
}

func TestCSVImportIsImmutableAndIdempotent(t *testing.T) {
	s := reviewServer(t, "")
	csv := reviewCSVHeader + "c2701,2026-09-01T15:00:00+08:00,2026-09-01,2200,2220,2190,2210,2205,1000,2000,0,0\n"
	for i := 0; i < 2; i++ {
		w := reviewRequest(t, s, "/api/import/bars", map[string]string{"csv": csv, "source": "licensed test"})
		if w.Code != 200 {
			t.Fatalf("valid import failed: %d %s", w.Code, w.Body.String())
		}
	}
	w := reviewRequest(t, s, "/api/import/bars", map[string]string{"csv": strings.Replace(csv, ",2210,", ",2211,", 1), "source": "correction"})
	if w.Code != 400 {
		t.Fatalf("conflict accepted: %d %s", w.Code, w.Body.String())
	}
	bars, _ := s.Store.Bars("c2701", false)
	if len(bars) != 1 || bars[0].Close != 2210 {
		t.Fatalf("immutable input replaced: %+v", bars)
	}
}

func TestDuplicateEventAPIKeepsOriginalAvailability(t *testing.T) {
	s := reviewServer(t, "")
	payload := domain.Event{ID: "client-chosen", Title: "玉米报价", Body: "报价2200元/吨", Source: "授权来源", PublishedAt: time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC), AvailableAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), IsDemo: true}
	w1 := reviewRequest(t, s, "/api/events", payload)
	if w1.Code != 201 {
		t.Fatalf("initial event: %d %s", w1.Code, w1.Body.String())
	}
	var first domain.Event
	if err := json.Unmarshal(w1.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.ID == payload.ID || first.IsDemo || !first.AvailableAt.After(payload.PublishedAt) {
		t.Fatalf("client forged provenance: %+v", first)
	}
	w2 := reviewRequest(t, s, "/api/events", payload)
	if w2.Code != 200 {
		t.Fatalf("duplicate event: %d %s", w2.Code, w2.Body.String())
	}
	var second domain.Event
	if err := json.Unmarshal(w2.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || !first.AvailableAt.Equal(second.AvailableAt) {
		t.Fatalf("duplicate changed availability: %+v %+v", first, second)
	}
}

func TestDuplicateObservationReturnsPersistedFirstAvailability(t *testing.T) {
	s := reviewServer(t, "")
	payload := domain.Observation{Kind: "truck_arrivals", Region: "山东", Entity: "同口径样本", Value: 100, Unit: "辆", Source: "授权来源", ObservedAt: time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)}
	w1 := reviewRequest(t, s, "/api/observations", payload)
	if w1.Code != 201 {
		t.Fatalf("initial observation: %d %s", w1.Code, w1.Body.String())
	}
	var first domain.Observation
	if err := json.Unmarshal(w1.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	w2 := reviewRequest(t, s, "/api/observations", payload)
	if w2.Code != 200 {
		t.Fatalf("duplicate should be 200, got %d %s", w2.Code, w2.Body.String())
	}
	var second domain.Observation
	if err := json.Unmarshal(w2.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	persisted, err := s.Store.Observations()
	if err != nil || len(persisted) != 1 || !second.AvailableAt.Equal(first.AvailableAt) || !second.AvailableAt.Equal(persisted[0].AvailableAt) {
		t.Fatalf("response differs from durable fact: first=%+v second=%+v stored=%+v err=%v", first, second, persisted, err)
	}
}

func TestAuthenticationAlsoProtectsSSEAndSettings(t *testing.T) {
	s := reviewServer(t, "private-test-token")
	for _, path := range []string{"/api/events", "/api/settings", "/api/stream"} {
		for _, authorization := range []string{"", "Bearer incorrect-token"} {
			r := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
			r.Header.Set("Authorization", authorization)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 401 || strings.Contains(w.Body.String(), s.Config.Token) {
				t.Errorf("unauthenticated access %s: %d %s", path, w.Code, w.Body.String())
			}
		}
	}
}

func TestLoopbackRejectsRebindingHostAndCrossSiteOrigin(t *testing.T) {
	s := reviewServer(t, "")
	for _, config := range []struct{ url, origin string }{{"http://attacker.example/api/events", ""}, {"http://127.0.0.1/api/events", "https://attacker.example"}} {
		r := httptest.NewRequest("GET", config.url, nil)
		r.Header.Set("Origin", config.origin)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("cross-site request accepted: %d", w.Code)
		}
	}
}

func TestAuthorizedSSEPublishesOnlyInvalidationMetadata(t *testing.T) {
	s := reviewServer(t, "private-sse-token")
	endpoint := httptest.NewServer(s.Handler())
	defer endpoint.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", endpoint.URL+"/api/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+s.Config.Token)
	response, err := endpoint.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE handshake: %d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != ": connected\n" {
		t.Fatalf("initial frame %q %v", line, err)
	}
	_, _ = reader.ReadString('\n')
	s.changed("events")
	var frame strings.Builder
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		frame.WriteString(line)
	}
	if !strings.Contains(frame.String(), `"kind":"events"`) || strings.Contains(frame.String(), s.Config.Token) || strings.Contains(frame.String(), "body") {
		t.Fatalf("unexpected stream data: %s", frame.String())
	}
	cancel()
	_, _ = io.Copy(io.Discard, response.Body)
}

func TestStaticUIRoutingDoesNotConflictWithAPICatchall(t *testing.T) {
	s := reviewServer(t, "")
	s.Config.UI = t.TempDir()
	if err := os.WriteFile(filepath.Join(s.Config.UI, "index.html"), []byte("<!doctype html><title>CornWatch</title>"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := s.Handler() // Regression: registering GET / with /api/ used to panic.
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{{"GET", "/", 200}, {"GET", "/api/unknown", 404}, {"POST", "/", 405}} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s %s: want %d got %d %s", tc.method, tc.path, tc.status, w.Code, w.Body.String())
		}
		if tc.method == "GET" && tc.path == "/" {
			if !strings.Contains(w.Body.String(), "CornWatch") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'self'") {
				t.Errorf("UI content or CSP missing: headers=%v body=%s", w.Header(), w.Body.String())
			}
		}
	}
}
