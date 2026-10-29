package marketchart

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cornwatch/internal/domain"
	"cornwatch/internal/marketdaily"
)

type fakeStore struct {
	mu     sync.Mutex
	meta   map[string][]byte
	bars   []domain.Bar
	fail   bool
	writes int
}

func newStore() *fakeStore { return &fakeStore{meta: map[string][]byte{}} }
func (s *fakeStore) GetMeta(k string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.meta[k]
	if !ok {
		return os.ErrNotExist
	}
	return json.Unmarshal(b, v)
}
func (s *fakeStore) SetMeta(k string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("disk error")
	}
	b, e := json.Marshal(v)
	if e == nil {
		s.meta[k] = b
		s.writes++
	}
	return e
}
func (s *fakeStore) Bars(symbol string, demo bool) ([]domain.Bar, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Bar
	for _, b := range s.bars {
		if b.Symbol == symbol && b.IsDemo == demo {
			out = append(out, b)
		}
	}
	return out, nil
}

type testClock struct{ ns atomic.Int64 }

func clockAt(t *testing.T, s string) *testClock {
	c := &testClock{}
	c.ns.Store(at(t, s).UnixNano())
	return c
}
func (c *testClock) Now() time.Time      { return time.Unix(0, c.ns.Load()).UTC() }
func (c *testClock) Add(d time.Duration) { c.ns.Add(int64(d)) }
func daily(t *testing.T, now time.Time, days ...string) marketdaily.Report {
	t.Helper()
	r := marketdaily.Report{Source: "synthetic local test only", SourceURL: "https://example.test", AvailableAt: now, HasOpenInterest: true, HasSettlement: true}
	for _, d := range days {
		stamp := at(t, d+"T15:00:00+08:00")
		r.Bars = append(r.Bars, domain.Bar{Symbol: "c2701", Timestamp: stamp, TradeDate: d, Open: 2200, High: 2202, Low: 2199, Close: 2201, Volume: 100, OpenInterest: 200, Settlement: 2200})
	}
	return r
}
func TestCacheArchiveRevisionsFailureAndRestart(t *testing.T) {
	clock := clockAt(t, "2026-10-29T16:00:00+08:00")
	db := newStore()
	calls := 0
	bad := false
	s := New(db, func(ctx context.Context, symbol string, at time.Time) (marketdaily.Report, error) {
		calls++
		if bad {
			return marketdaily.Report{}, errors.New("source busy")
		}
		d := daily(t, clock.Now(), "2026-10-27", "2026-10-28")
		if calls > 1 {
			d.Bars[1].Close = 2202
		}
		return d, nil
	})
	s.now = clock.Now
	defer s.Close()
	req := Request{Symbol: "c2701"}
	r, e := s.History(context.Background(), req)
	if e != nil || len(r.Bars) != 2 || calls != 1 || r.Cached {
		t.Fatalf("%+v %v", r, e)
	}
	original := r.Bars[0].AvailableAt
	r.Bars[0].Close = 999
	r.Warnings[0] = "mutation"
	r, e = s.History(context.Background(), req)
	if e != nil || calls != 1 || !r.Cached || r.Bars[0].Close != 2201 {
		t.Fatal("cache mutation or extra request")
	}
	clock.Add(61 * time.Second)
	r, e = s.History(context.Background(), req)
	if e != nil || calls != 2 || r.Bars[1].Close != 2202 || !r.Bars[0].AvailableAt.Equal(original) || r.Bars[1].AvailableAt.Equal(original) {
		t.Fatalf("revision availability: %+v %v", r, e)
	}
	bad = true
	clock.Add(61 * time.Second)
	r, e = s.History(context.Background(), req)
	if e != nil || calls != 3 || !r.Stale || r.LastError == "" || len(r.Bars) != 2 {
		t.Fatalf("fallback %+v %v", r, e)
	}
	restarted := New(db, func(context.Context, string, time.Time) (marketdaily.Report, error) {
		t.Error("restart bypassed cooldown")
		return marketdaily.Report{}, nil
	})
	restarted.now = clock.Now
	defer restarted.Close()
	r, e = restarted.History(context.Background(), req)
	if e != nil || !r.Cached || !r.Stale || r.LastError == "" {
		t.Fatalf("restart %+v %v", r, e)
	}
	if len(db.bars) != 0 {
		t.Fatal("chart wrote backtest bars")
	}
}
func TestConcurrentFetchCallerCancellationAndShutdown(t *testing.T) {
	clock := clockAt(t, "2026-10-29T16:00:00+08:00")
	db := newStore()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s := New(db, func(ctx context.Context, _ string, _ time.Time) (marketdaily.Report, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return daily(t, clock.Now(), "2026-10-28"), nil
		case <-ctx.Done():
			return marketdaily.Report{}, ctx.Err()
		}
	})
	s.now = clock.Now
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, e := s.History(ctx, Request{Symbol: "c2701"}); first <- e }()
	<-started
	second := make(chan error, 1)
	go func() { _, e := s.History(context.Background(), Request{Symbol: "c2701"}); second <- e }()
	cancel()
	if e := <-first; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	close(release)
	if e := <-second; e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate fetch")
	}
	s.Close()
	if _, e := s.History(context.Background(), Request{Symbol: "c2701"}); e == nil {
		t.Fatal("accepted after close")
	}
	blocked := make(chan struct{})
	s2 := New(db, func(ctx context.Context, _ string, _ time.Time) (marketdaily.Report, error) {
		close(blocked)
		<-ctx.Done()
		return marketdaily.Report{}, ctx.Err()
	})
	s2.now = func() time.Time { return clock.Now().Add(2 * time.Minute) }
	done := make(chan struct{})
	go func() { s2.History(context.Background(), Request{Symbol: "c2701"}); close(done) }()
	<-blocked
	s2.Close()
	<-done
}
func TestBadSourceAndFailedPersistenceKeepOld(t *testing.T) {
	clock := clockAt(t, "2026-10-29T16:00:00+08:00")
	db := newStore()
	invalid := false
	s := New(db, func(context.Context, string, time.Time) (marketdaily.Report, error) {
		d := daily(t, clock.Now(), "2026-10-28")
		if invalid {
			d.Bars[0].IsDemo = true
		}
		return d, nil
	})
	s.now = clock.Now
	defer s.Close()
	req := Request{Symbol: "c2701"}
	if _, e := s.History(context.Background(), req); e != nil {
		t.Fatal(e)
	}
	invalid = true
	clock.Add(61 * time.Second)
	r, e := s.History(context.Background(), req)
	if e != nil || !r.Stale || r.Bars[0].IsDemo {
		t.Fatal("invalid source replaced cache")
	}
	invalid = false
	db.fail = true
	clock.Add(61 * time.Second)
	r, e = s.History(context.Background(), req)
	if e != nil || !r.Stale || r.LastError == "" {
		t.Fatal("storage error hidden")
	}
}
func TestRangeImportedIsolationAndFutureExclusion(t *testing.T) {
	clock := clockAt(t, "2026-10-29T10:00:00+08:00")
	db := newStore()
	d := daily(t, clock.Now(), "2026-10-27", "2026-10-28", "2026-10-29")
	db.bars = append(db.bars, d.Bars...)
	demo := d.Bars[0]
	demo.IsDemo = true
	demo.Close = 2222
	db.bars = append(db.bars, demo)
	s := New(db, func(context.Context, string, time.Time) (marketdaily.Report, error) {
		t.Fatal("imported should not request public")
		return marketdaily.Report{}, nil
	})
	s.now = clock.Now
	defer s.Close()
	r, e := s.History(context.Background(), Request{Symbol: "c2701", Source: "imported", From: "2026-10-28", To: "2026-10-29", Limit: 1})
	if e != nil || len(r.Bars) != 1 || r.Bars[0].TradeDate != "2026-10-28" || r.TotalAvailable != 2 || !r.AvailableAt.IsZero() {
		t.Fatalf("%+v %v", r, e)
	}
	r, e = s.History(context.Background(), Request{Symbol: "c2701", Source: "imported", From: "2026-01-01", To: "2026-01-02"})
	if e != nil || len(r.Bars) != 0 || r.TotalAvailable != 2 {
		t.Fatalf("empty range %+v %v", r, e)
	}
}
func TestMinuteServiceArchiveAndLimit(t *testing.T) {
	clock := clockAt(t, "2026-10-29T10:03:00+08:00")
	s := New(newStore(), nil)
	s.now = clock.Now
	defer s.Close()
	calls := 0
	s.minute = func(context.Context, string, string, time.Time) (Report, error) {
		calls++
		return parseMinute([]byte(minuteFixture), "c2701", "1m", clock.Now())
	}
	r, e := s.History(context.Background(), Request{Symbol: "c2701", Interval: "1m", Limit: 2})
	if e != nil || len(r.Bars) != 2 || r.TotalAvailable != 3 || calls != 1 {
		t.Fatalf("%+v %v", r, e)
	}
	clock.Add(30 * time.Second)
	if _, e = s.History(context.Background(), Request{Symbol: "c2701", Interval: "1m"}); e != nil || calls != 1 {
		t.Fatal("minute refresh bypassed")
	}
}

func TestRestartUnchangedRowsRetainAvailabilityAndClockRollbackHides(t *testing.T) {
	clock := clockAt(t, "2026-10-29T16:00:00+08:00")
	db := newStore()
	fetch := func(context.Context, string, time.Time) (marketdaily.Report, error) {
		return daily(t, clock.Now(), "2026-10-28"), nil
	}
	s := New(db, fetch)
	s.now = clock.Now
	first, e := s.History(context.Background(), Request{Symbol: "c2701"})
	if e != nil {
		t.Fatal(e)
	}
	original := first.Bars[0].AvailableAt
	s.Close()
	clock.Add(61 * time.Second)
	restored := New(db, fetch)
	restored.now = clock.Now
	defer restored.Close()
	second, e := restored.History(context.Background(), Request{Symbol: "c2701"})
	if e != nil || !second.Bars[0].AvailableAt.Equal(original) {
		t.Fatalf("unchanged JSON-restored row became a new revision: %+v %v", second, e)
	}
	clock.Add(-2 * time.Minute)
	third, e := restored.History(context.Background(), Request{Symbol: "c2701"})
	if e != nil || len(third.Bars) != 0 || third.TotalAvailable != 0 {
		t.Fatalf("clock rollback exposed future availability: %+v %v", third, e)
	}
}

func TestArchiveOptionalCoverageRemainsConservative(t *testing.T) {
	clock := clockAt(t, "2026-10-29T16:00:00+08:00")
	calls := 0
	s := New(newStore(), func(context.Context, string, time.Time) (marketdaily.Report, error) {
		calls++
		var r marketdaily.Report
		if calls == 1 {
			r = daily(t, clock.Now(), "2026-10-27")
			r.HasOpenInterest = false
			r.HasSettlement = false
			r.Bars[0].OpenInterest = 0
			r.Bars[0].Settlement = 0
		} else {
			r = daily(t, clock.Now(), "2026-10-28")
		}
		return r, nil
	})
	s.now = clock.Now
	if _, e := s.History(context.Background(), Request{Symbol: "c2701"}); e != nil {
		t.Fatal(e)
	}
	clock.Add(61 * time.Second)
	r, e := s.History(context.Background(), Request{Symbol: "c2701"})
	if e != nil || len(r.Bars) != 2 || r.HasOpenInterest || r.HasSettlement {
		t.Fatalf("old missing fields declared complete %+v %v", r, e)
	}
	s.Close()
	if _, e = s.History(context.Background(), Request{Symbol: "c2701", Source: "imported"}); e == nil {
		t.Fatal("imported read accepted after close")
	}
}
