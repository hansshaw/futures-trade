package datahub

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"cornwatch/internal/store"
)

func TestHighFrequencyDefaultsAndIntervalValidation(t *testing.T) {
	h, _, _ := testHub(t)
	for _, id := range []string{"sina", "market_bridge"} {
		source := sourceByID(t, h, id)
		if source.Config.IntervalSeconds != .5 || source.MinIntervalSeconds != .5 || source.Polling.TargetIntervalMS != 500 || source.Polling.TargetRequestsPerMinute != 120 {
			t.Fatalf("wrong fast defaults: %+v", source)
		}
		for _, bad := range []float64{.499, .5001, math.NaN(), math.Inf(1), 3601} {
			c := h.configs[id]
			if _, err := h.Save(id, ConfigRequest{Revision: c.Revision, Enabled: true, IntervalSeconds: bad}); err == nil {
				t.Fatalf("accepted interval %v", bad)
			}
		}
	}
	if h.Live().Polling.TargetIntervalMS != 500 {
		t.Fatal("pending UI lacks target cadence")
	}
	if sourceByID(t, h, "weather").Config.IntervalSeconds != 3600 || sourceByID(t, h, "boyar_prices").Config.IntervalSeconds != 43200 {
		t.Fatal("market cadence changed low-frequency sources")
	}
}

func TestLegacyCadenceMigrationOnlyOncePreservesCustomAndCredentials(t *testing.T) {
	for _, tc := range []struct {
		id        string
		old, want float64
		changed   bool
	}{
		{"sina", 60, .5, true}, {"market_bridge", 10, .5, true}, {"sina", 120, 120, false}, {"market_bridge", 20, 20, false}, {"weather", 7200, 7200, false},
	} {
		t.Run(tc.id+time.Duration(tc.old*float64(time.Second)).String(), func(t *testing.T) {
			_, s, _ := testHub(t)
			c := Config{Revision: 7, Enabled: false, IntervalSeconds: tc.old, Endpoint: "", Token: "saved-secret"}
			if tc.id == "market_bridge" {
				c.Endpoint = "https://example.com/quotes"
			}
			if err := s.SetMeta("datahub_config_"+tc.id, c); err != nil {
				t.Fatal(err)
			}
			next := testNow.Add(time.Hour)
			if err := s.SetMeta("datahub_record_"+tc.id, Record{Binding: binding(c), ConfigRevision: 7, NextRun: &next}); err != nil {
				t.Fatal(err)
			}
			h, err := New(s)
			if err != nil {
				t.Fatal(err)
			}
			got := h.configs[tc.id]
			rev := uint64(7)
			if tc.changed {
				rev++
			}
			if got.IntervalSeconds != tc.want || got.Revision != rev || got.Enabled || got.Token != c.Token || got.Endpoint != c.Endpoint || got.CadenceVersion != 1 {
				t.Fatalf("unsafe migration: %+v", got)
			}
			if tc.changed && h.records[tc.id].NextRun != nil {
				t.Fatal("old schedule survived migrated cadence")
			}
			// Returning deliberately to the previous default is a user choice;
			// subsequent launches must not migrate that setting again.
			if _, err = h.Save(tc.id, ConfigRequest{Revision: got.Revision, Enabled: false, IntervalSeconds: tc.old, Endpoint: got.Endpoint}); err != nil {
				t.Fatal(err)
			}
			again, err := New(s)
			if err != nil {
				t.Fatal(err)
			}
			if again.configs[tc.id].IntervalSeconds != tc.old {
				t.Fatal("migration repeatedly overwrote user setting")
			}
		})
	}
}

func TestFastCadenceCounts120StartsNotCacheHitsAndKeepsSourceTime(t *testing.T) {
	h, _, _ := testHub(t)
	calls := 0
	events := []string{}
	h.SetNotify(func(kind string) { events = append(events, kind) })
	h.fetchers["sina"] = func(context.Context, Config) (Batch, error) {
		calls++
		quotes := exampleQuotes("public", testNow)
		quotes[0].Last += float64(calls)
		// Simulate 100ms of upstream latency. Next start remains 500ms from
		// start rather than 500ms from the slower completion time.
		advanceHubClock(h, 100*time.Millisecond)
		return quoteBatch(quotes), nil
	}
	for i := 0; i < 120; i++ {
		at := testNow.Add(time.Duration(i) * 500 * time.Millisecond)
		h.now = func() time.Time { return at }
		out, err := h.RunScheduled(context.Background(), "sina")
		if err != nil || out.Skipped || out.Cached {
			t.Fatalf("cadence %d: %+v %v", i, out, err)
		}
		record := h.records["sina"]
		if !record.NextRun.Equal(at.Add(500 * time.Millisecond)) {
			t.Fatal("latency shifted target cadence")
		}
		cached, err := h.Run(context.Background(), "sina", true)
		if err != nil || !cached.Cached {
			t.Fatalf("manual burst not cached: %+v %v", cached, err)
		}
	}
	stats := h.Live().Polling
	if calls != 120 || stats.RequestsLastMinute != 120 || stats.LastLatencyMS != 100 {
		t.Fatalf("not 120 actual fetches: calls=%d %+v", calls, stats)
	}
	if !stats.StatsSince.Equal(testNow) || !stats.LastRequestAt.Equal(testNow.Add(59500*time.Millisecond)) {
		t.Fatal("wrong telemetry times")
	}
	points := h.Live().PriceHistory["c2701"]
	if len(points) != 1 || points[0].Last != 2420 || !points[0].ObservedAt.Equal(testNow) {
		t.Fatalf("same-second update made duplicate or missing point: %+v", points)
	}
	// Explicit connection tests emit data-sources; quote syncs emit only market.
	for i, event := range events {
		want := "market"
		if i%2 == 1 {
			want = "data-sources"
		}
		if event != want {
			t.Fatalf("event %d=%s want %s", i, event, want)
		}
	}
	advanceHubClock(h, time.Minute)
	if h.Live().Polling.RequestsLastMinute != 0 {
		t.Fatal("rolling minute retained expired attempts")
	}
}

func TestFastFailureCooldownAndNoCatchupBurst(t *testing.T) {
	h, _, _ := testHub(t)
	calls := 0
	h.fetchers["sina"] = func(context.Context, Config) (Batch, error) {
		calls++
		advanceHubClock(h, 800*time.Millisecond)
		return quoteBatch(exampleQuotes("public", testNow)), nil
	}
	if _, err := h.RunScheduled(context.Background(), "sina"); err != nil {
		t.Fatal(err)
	}
	if h.records["sina"].NextRun.Before(h.now()) {
		t.Fatal("slow response accumulated catchup debt")
	}
	h.fetchers["sina"] = func(context.Context, Config) (Batch, error) {
		calls++
		return Batch{}, errors.New("429 too many requests")
	}
	if _, err := h.RunScheduled(context.Background(), "sina"); err == nil {
		t.Fatal("missing source failure")
	}
	deadline := *h.records["sina"].NextRun
	if deadline.Sub(h.now()) != 5*time.Second {
		t.Fatal("fast source failed without 5s backoff")
	}
	advanceHubClock(h, time.Second)
	if out, err := h.RunScheduled(context.Background(), "sina"); err != nil || !out.Skipped {
		t.Fatal("auto source bypassed backoff")
	}
	if _, err := h.Run(context.Background(), "sina", false); !errors.Is(err, ErrCooldown) {
		t.Fatal("manual source bypassed cooldown")
	}
	if calls != 2 || h.Live().Polling.RequestsLastMinute != 2 {
		t.Fatal("cooldown hit upstream")
	}
}

type countingPersistence struct {
	*store.Store
	writes int
	fail   bool
}

func (p *countingPersistence) SetMeta(key string, value any) error {
	if key == "datahub_record_sina" {
		p.writes++
		if p.fail {
			return errors.New("disk failure")
		}
	}
	return p.Store.SetMeta(key, value)
}
func TestAutomaticQuoteCheckpointsAndFlushPreserveDurabilityBoundary(t *testing.T) {
	h, s, _ := testHub(t)
	disk := &countingPersistence{Store: s}
	h.store = disk
	h.fetchers["sina"] = func(context.Context, Config) (Batch, error) { return quoteBatch(exampleQuotes("public", h.now())), nil }
	for i := 0; i < 10; i++ {
		at := testNow.Add(time.Duration(i) * 500 * time.Millisecond)
		h.now = func() time.Time { return at }
		if _, err := h.RunScheduled(context.Background(), "sina"); err != nil {
			t.Fatal(err)
		}
	}
	if disk.writes != 1 || !h.dirty["sina"] {
		t.Fatalf("excessive writes: %d", disk.writes)
	}
	var saved Record
	if err := s.GetMeta("datahub_record_sina", &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.LastSuccess.Equal(testNow) || !h.Live().LastSuccess.Equal(testNow.Add(4500*time.Millisecond)) || !h.Live().Polling.PersistedAt.Equal(testNow) {
		t.Fatal("live quote presented as durable")
	}
	advanceHubClock(h, 500*time.Millisecond)
	if _, err := h.RunScheduled(context.Background(), "sina"); err != nil {
		t.Fatal(err)
	}
	if disk.writes != 2 || h.dirty["sina"] {
		t.Fatal("5s checkpoint missing")
	}
	advanceHubClock(h, 500*time.Millisecond)
	if _, err := h.RunScheduled(context.Background(), "sina"); err != nil {
		t.Fatal(err)
	}
	before := h.Live().Polling.PersistedAt
	disk.fail = true
	if err := h.Flush(); err == nil {
		t.Fatal("flush failure hidden")
	}
	if !h.Live().Polling.PersistedAt.Equal(*before) || !h.dirty["sina"] {
		t.Fatal("failed flush advanced durability")
	}
	disk.fail = false
	if err := h.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.GetMeta("datahub_record_sina", &saved); err != nil {
		t.Fatal(err)
	}
	if h.dirty["sina"] || !reflect.DeepEqual(saved.Quotes, h.Live().Quotes) || !saved.LastSuccess.Equal(*h.Live().LastSuccess) {
		t.Fatal("flush did not save latest snapshot")
	}
}

func TestPollWaitUsesExactNearDeadline(t *testing.T) {
	h, _, _ := testHub(t)
	for id, c := range h.configs {
		c.Enabled = id == "sina"
		h.configs[id] = c
	}
	next := testNow.Add(1700 * time.Microsecond)
	h.records["sina"] = Record{NextRun: &next}
	if got := h.pollWait(); got != 1700*time.Microsecond {
		t.Fatalf("near deadline quantized: %s", got)
	}
	h.busy["sina"] = true
	if got := h.pollWait(); got != 50*time.Millisecond {
		t.Fatalf("busy deadline spun: %s", got)
	}
	delete(h.busy, "sina")
	next = testNow.Add(-time.Second)
	if got := h.pollWait(); got != time.Millisecond {
		t.Fatalf("overdue deadline spun: %s", got)
	}
}

func TestFastPollRealTimerStaysNear500ms(t *testing.T) {
	h, _, _ := testHub(t)
	h.now = func() time.Time { return time.Now().UTC() }
	for id, c := range h.configs {
		c.Enabled = id == "sina"
		h.configs[id] = c
	}
	starts := make(chan time.Time, 8)
	h.fetchers["sina"] = func(ctx context.Context, _ Config) (Batch, error) {
		at := time.Now()
		starts <- at
		timer := time.NewTimer(35 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return Batch{}, ctx.Err()
		case <-timer.C:
		}
		return quoteBatch(exampleQuotes("public", at.UTC())), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.Poll(ctx) }()
	defer func() { cancel(); <-done }()
	captured := []time.Time{}
	deadline := time.After(4 * time.Second)
	for len(captured) < 6 {
		select {
		case at := <-starts:
			captured = append(captured, at)
		case <-deadline:
			t.Fatal("local polling fell behind")
		}
	}
	elapsed := captured[5].Sub(captured[0])
	// Five 500ms gaps should total about2.5s. The old fixed50ms ticker often
	// accumulated a full extra50ms at each gap (~2.75s). Allow scheduling jitter.
	if elapsed < 2500*time.Millisecond || elapsed > 2640*time.Millisecond {
		t.Fatalf("500ms deadline drift: five gaps=%s", elapsed)
	}
	for i := 1; i < len(captured); i++ {
		if captured[i].Sub(captured[i-1]) < 499*time.Millisecond {
			t.Fatal("catchup burst compressed source interval")
		}
	}
	t.Logf("five local intervals %s (target2.5s)", elapsed)
}
