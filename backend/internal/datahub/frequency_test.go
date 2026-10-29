package datahub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"cornwatch/internal/domain"
)

func TestTestThenSyncReusesBatchWithoutInventingFreshness(t *testing.T) {
	h, s, _ := testHub(t)
	calls := 0
	b := observationBatch([]domain.Observation{exampleObservation()}, []domain.Event{{
		ID: "cached-event", Title: "source event", PublishedAt: testNow, AvailableAt: testNow,
		Products: []string{"c"}, Analysis: &domain.Analysis{Reasoning: []string{"original"}},
	}})
	h.fetchers["liaoning"] = func(context.Context, Config) (Batch, error) { calls++; return b, nil }
	tested, err := h.Run(context.Background(), "liaoning", true)
	if err != nil || tested.Cached || tested.FetchedAt == nil || !tested.FetchedAt.Equal(testNow) {
		t.Fatalf("initial test: %+v %v", tested, err)
	}
	if got, _ := s.Observations(); len(got) != 0 {
		t.Fatal("test imported data")
	}
	// A caller/fetcher retaining the original slices cannot corrupt the memo.
	b.Observations[0].Value = -1
	b.Events[0].Products[0] = "bad"
	b.Events[0].Analysis.Reasoning[0] = "bad"
	*tested.FetchedAt = time.Time{}
	advanceHubClock(h, 2*time.Second)
	out, err := h.Run(context.Background(), "liaoning", false)
	if err != nil || !out.Cached || calls != 1 || out.Added != 2 || !out.FetchedAt.Equal(testNow) || !strings.Contains(out.Message, "未再次请求上游") {
		t.Fatalf("test/sync duplicated fetch: calls=%d out=%+v err=%v", calls, out, err)
	}
	r := cloneRecord(h.records["liaoning"])
	if !r.LastAttempt.Equal(testNow) || !r.LastSuccess.Equal(testNow) || !r.NextRun.Equal(testNow.Add(12*time.Hour)) {
		t.Fatalf("cached batch shifted collection/schedule timestamps: %+v", r)
	}
	items, _ := s.Observations()
	events, _ := s.Events()
	if len(items) != 1 || items[0].Value != 42 || !items[0].AvailableAt.Equal(testNow) || events[0].Products[0] != "c" || events[0].Analysis.Reasoning[0] != "original" {
		t.Fatal("cache lost immutable source values or timestamps")
	}
	advanceHubClock(h, 2*time.Second)
	out, err = h.Run(context.Background(), "liaoning", false)
	if err != nil || calls != 1 || !out.Cached || out.Duplicates != 2 || !h.records["liaoning"].NextRun.Equal(*r.NextRun) || !h.records["liaoning"].LastSuccess.Equal(*r.LastSuccess) {
		t.Fatalf("repeated cache use extended health/schedule: %+v %v", out, err)
	}
	advanceHubClock(h, time.Second) // Exactly five seconds is no longer cached.
	if out, err = h.Run(context.Background(), "liaoning", true); err != nil || out.Cached || calls != 2 {
		t.Fatalf("cache never expired: calls=%d out=%+v err=%v", calls, out, err)
	}
}

func TestCachedQuoteDoesNotBecomeNewCloseEvidence(t *testing.T) {
	h, _, _ := testHub(t)
	// Cross the mature-close boundary while inside the 500ms quote cache.
	beforeClose := time.Date(2026, 10, 28, 7, 14, 59, 800000000, time.UTC)
	h.now = func() time.Time { return beforeClose }
	quotes := exampleQuotes("public", beforeClose.Add(-10*time.Minute))
	h.fetchers["sina"] = func(context.Context, Config) (Batch, error) { return quoteBatch(quotes), nil }
	if _, err := h.Run(context.Background(), "sina", true); err != nil {
		t.Fatal(err)
	}
	advanceHubClock(h, 300*time.Millisecond)
	out, err := h.Run(context.Background(), "sina", false)
	if err != nil || !out.Cached || len(h.records["sina"].Main.Snapshots) != 0 {
		t.Fatalf("cached quote promoted across close boundary: %+v %v", h.records["sina"].Main, err)
	}
}

func TestFailureCooldownDoesNotExtendBackoffOrHideLastSnapshot(t *testing.T) {
	h, _, _ := testHub(t)
	calls := 0
	h.fetchers["sina"] = func(context.Context, Config) (Batch, error) {
		calls++
		if calls == 1 {
			return quoteBatch(exampleQuotes("public", testNow)), nil
		}
		return Batch{}, errors.New("upstream HTTP 429")
	}
	if _, err := h.Run(context.Background(), "sina", false); err != nil {
		t.Fatal(err)
	}
	advanceHubClock(h, 5*time.Second)
	if _, err := h.Run(context.Background(), "sina", false); err == nil {
		t.Fatal("source failure missing")
	}
	before := cloneRecord(h.records["sina"])
	advanceHubClock(h, 1100*time.Millisecond)
	for _, test := range []bool{true, false} {
		_, err := h.Run(context.Background(), "sina", test)
		var cooldown *CooldownError
		if !errors.Is(err, ErrCooldown) || !errors.As(err, &cooldown) || cooldown.RetrySeconds() != 4 {
			t.Fatalf("missing cooldown: %v", err)
		}
		response := httptest.NewRecorder()
		replyError(response, err)
		if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "4" {
			t.Fatalf("cooldown HTTP contract: %+v", response.Result())
		}
	}
	if calls != 2 || !reflect.DeepEqual(before, h.records["sina"]) || len(h.Live().Quotes) != 2 {
		t.Fatal("blocked request hit upstream, changed failure history, or discarded quotes")
	}
	advanceHubClock(h, 3900*time.Millisecond)
	if _, err := h.Run(context.Background(), "sina", true); err == nil || errors.Is(err, ErrCooldown) || calls != 3 {
		t.Fatalf("failure cooldown did not expire: calls=%d err=%v", calls, err)
	}
}

func TestScheduledRunsRespectConfiguredCadenceAndBackoff(t *testing.T) {
	h, _, _ := testHub(t)
	calls, failing := 0, false
	h.fetchers["liaoning"] = func(context.Context, Config) (Batch, error) {
		calls++
		if failing {
			return Batch{}, errors.New("upstream unavailable")
		}
		return observationBatch([]domain.Observation{exampleObservation()}, nil), nil
	}
	if out, err := h.RunScheduled(context.Background(), "liaoning"); err != nil || out.Skipped || !out.OK {
		t.Fatalf("initial due run: %+v %v", out, err)
	}
	before := cloneRecord(h.records["liaoning"])
	// Reports run every minute; the weekly source is still checked every 12h.
	for i := 0; i < 10; i++ {
		advanceHubClock(h, time.Minute)
		out, err := h.RunScheduled(context.Background(), "liaoning")
		if err != nil || !out.Skipped || !out.OK || !out.FetchedAt.Equal(testNow) {
			t.Fatalf("report bypassed schedule: %+v %v", out, err)
		}
	}
	if calls != 1 || !reflect.DeepEqual(before, h.records["liaoning"]) {
		t.Fatal("skipping changed source state")
	}
	h.now = func() time.Time { return *before.NextRun }
	failing = true
	if _, err := h.RunScheduled(context.Background(), "liaoning"); err == nil || calls != 2 {
		t.Fatalf("due failure not reported: %v calls=%d", err, calls)
	}
	failed := cloneRecord(h.records["liaoning"])
	advanceHubClock(h, 12*time.Hour)
	out, err := h.RunScheduled(context.Background(), "liaoning")
	if err != nil || !out.Skipped || out.OK || calls != 2 || !reflect.DeepEqual(failed, h.records["liaoning"]) {
		t.Fatalf("report bypassed failure backoff: %+v %v", out, err)
	}
	h.now = func() time.Time { return *failed.NextRun }
	if _, err := h.RunScheduled(context.Background(), "liaoning"); err == nil || calls != 3 {
		t.Fatalf("backoff expiry: %v calls=%d", err, calls)
	}
}

func TestScheduledRunsSkipPausedDisabledAndUnconfigured(t *testing.T) {
	for _, mode := range []string{"paused", "disabled", "unconfigured"} {
		t.Run(mode, func(t *testing.T) {
			h, _, _ := testHub(t)
			id := "sina"
			switch mode {
			case "paused":
				h.paused = true
			case "disabled":
				cfg := h.configs[id]
				cfg.Enabled = false
				h.configs[id] = cfg
			case "unconfigured":
				id = "market_bridge"
				cfg := h.configs[id]
				cfg.Enabled = true
				h.configs[id] = cfg
			}
			h.fetchers[id] = func(context.Context, Config) (Batch, error) { t.Fatal("skipped source fetched"); return Batch{}, nil }
			before := cloneRecord(h.records[id])
			out, err := h.RunScheduled(context.Background(), id)
			if err != nil || !out.Skipped || out.OK || !reflect.DeepEqual(before, h.records[id]) {
				t.Fatalf("did not skip %s: %+v %v", mode, out, err)
			}
		})
	}
}

func TestConnectionChangeInvalidatesSuccessfulCache(t *testing.T) {
	h, _, _ := testHub(t)
	saveBridge(t, h, "market_bridge", "https://first.example/quotes", "first-secret")
	calls := 0
	h.fetchers["market_bridge"] = func(_ context.Context, cfg Config) (Batch, error) {
		calls++
		return quoteBatch(exampleQuotes(cfg.Endpoint, testNow)), nil
	}
	for _, change := range []struct{ endpoint, token string }{
		{"https://first.example/quotes", "first-secret"},
		{"https://first.example/quotes", "second-secret"},
		{"https://second.example/quotes", "second-secret"},
	} {
		saveBridge(t, h, "market_bridge", change.endpoint, change.token)
		out, err := h.Run(context.Background(), "market_bridge", true)
		if err != nil || out.Cached {
			t.Fatalf("new connection reused old authorization: %+v %v", out, err)
		}
	}
	if calls != 3 {
		t.Fatalf("connection validation calls=%d", calls)
	}
}

func TestRequestPolicyAvailableForEverySource(t *testing.T) {
	for _, d := range definitions() {
		if d.RequestPolicy == "" || !strings.Contains(d.RequestPolicy, "不代表来源延迟或授权额度") {
			t.Fatalf("missing source request policy: %s", d.ID)
		}
	}
}
