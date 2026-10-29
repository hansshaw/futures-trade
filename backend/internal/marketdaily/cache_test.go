package marketdaily

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cornwatch/internal/domain"
)

func cacheReport(at time.Time) Report {
	return Report{Bars: []domain.Bar{{Symbol: "c2701", Timestamp: at, Close: 2224}}, Source: Source,
		SourceURL: endpoint + "C2701", AvailableAt: at, LastTradingDate: "2026-10-28", SHA256: "original-hash",
		MissingOpenInterestDates: []string{"2026-10-20"}, MissingSettlementDates: []string{"2026-10-21"},
		ExcludedIncomplete: []string{"2026-10-29"}, Warnings: []string{"original warning"}}
}

func TestCachedSuccessExpiresFromCompletionAndPreservesIndependentCopies(t *testing.T) {
	start := at(t, "2026-10-28T15:30:00+08:00")
	var nano atomic.Int64
	nano.Store(start.UnixNano())
	clock := func() time.Time { return time.Unix(0, nano.Load()).UTC() }
	original := cacheReport(start.Add(-time.Minute))
	want := cloneReport(original)
	calls := 0
	fetch := cached(func(context.Context, string, time.Time) (Report, error) {
		calls++
		nano.Add(int64(4 * time.Second)) // Four seconds of network/parse work.
		return original, nil
	}, clock, time.Second)
	one, err := fetch(context.Background(), " C2701 ", start)
	if err != nil || calls != 1 || !reflect.DeepEqual(one, want) {
		t.Fatalf("initial fetch: %+v %v calls=%d", one, err, calls)
	}
	one.Bars[0].Close = -1
	one.Warnings[0] = "mutated caller"
	one.MissingOpenInterestDates[0] = "mutated caller"
	one.MissingSettlementDates[0] = "mutated caller"
	one.ExcludedIncomplete[0] = "mutated caller"
	original.Bars[0].Close = -2
	original.Warnings[0] = "mutated fetcher"
	original.MissingOpenInterestDates[0] = "mutated fetcher"
	original.MissingSettlementDates[0] = "mutated fetcher"
	original.ExcludedIncomplete[0] = "mutated fetcher"
	nano.Add(int64(4*time.Second + 999*time.Millisecond))
	two, err := fetch(context.Background(), "c2701", start)
	if err != nil || calls != 1 || !reflect.DeepEqual(two, want) {
		t.Fatalf("cache reset time or leaked mutable fields: %+v %v calls=%d", two, err, calls)
	}
	nano.Add(int64(time.Millisecond))
	if _, err = fetch(context.Background(), "c2701", start); err != nil || calls != 2 {
		t.Fatalf("five-second completion boundary did not expire: %v calls=%d", err, calls)
	}
}

func TestCachedKeySeparatesShanghaiDateMaturityAndActualSymbol(t *testing.T) {
	var calls atomic.Int32
	fetch := cached(func(_ context.Context, symbol string, asOf time.Time) (Report, error) {
		calls.Add(1)
		r := cacheReport(asOf)
		r.Bars[0].Symbol = symbol
		return r, nil
	}, func() time.Time { return time.Unix(1000, 0) }, time.Second)
	cases := []struct {
		symbol, instant string
		wantCalls       int32
	}{
		{"c2701", "2026-10-28T15:14:59+08:00", 1},
		{"C2701", "2026-10-28T07:14:59Z", 1}, // Same Shanghai date/maturity.
		{"c2701", "2026-10-28T15:15:00+08:00", 2},
		{"c2701", "2026-10-28T23:59:59+08:00", 2},
		{"c2701", "2026-10-29T00:00:00+08:00", 3},
		{"c2701", "2026-10-28T16:00:01Z", 3},
		{"c2705", "2026-10-29T00:00:00+08:00", 4},
	}
	for _, tc := range cases {
		if _, err := fetch(context.Background(), tc.symbol, at(t, tc.instant)); err != nil || calls.Load() != tc.wantCalls {
			t.Fatalf("key %s %s: calls=%d want=%d err=%v", tc.symbol, tc.instant, calls.Load(), tc.wantCalls, err)
		}
	}
}

func TestCachedCombinesConcurrentWaiters(t *testing.T) {
	stamp := at(t, "2026-10-28T15:30:00+08:00")
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	fetch := Cached(func(context.Context, string, time.Time) (Report, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return cacheReport(stamp), nil
	})
	var wg sync.WaitGroup
	results := make(chan Report, 16)
	errorsSeen := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := fetch(context.Background(), "c2701", stamp)
			results <- r
			errorsSeen <- err
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("shared fetch did not start")
	}
	close(release)
	wg.Wait()
	close(results)
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	for r := range results {
		if !r.AvailableAt.Equal(stamp) || r.Bars[0].Close != 2224 {
			t.Fatal("waiter lost shared result")
		}
		r.Bars[0].Close = -1
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent requests duplicated upstream: %d", calls.Load())
	}
}

func TestCachedCallerCancellationDoesNotCancelSharedRequest(t *testing.T) {
	stamp := at(t, "2026-10-28T15:30:00+08:00")
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	fetch := Cached(func(ctx context.Context, _ string, _ time.Time) (Report, error) {
		calls.Add(1)
		entered <- ctx
		select {
		case <-release:
			return cacheReport(stamp), nil
		case <-ctx.Done():
			return Report{}, ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := fetch(ctx, "c2701", stamp); first <- err }()
	var shared context.Context
	select {
	case shared = <-entered:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start")
	}
	second := make(chan error, 1)
	go func() { _, err := fetch(context.Background(), "c2701", stamp); second <- err }()
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled caller waited for shared network request")
	}
	if shared.Err() != nil {
		t.Fatal("first caller canceled another caller's request")
	}
	if deadline, ok := shared.Deadline(); !ok || time.Until(deadline) > 20*time.Second {
		t.Fatal("shared request lost its bounded deadline")
	}
	close(release)
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("remaining waiter: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("remaining waiter did not finish")
	}
	if _, err := fetch(context.Background(), "c2701", stamp); err != nil || calls.Load() != 1 {
		t.Fatalf("successful shared request not reusable: calls=%d err=%v", calls.Load(), err)
	}
}

func TestCachedFailureCooldownAndRecovery(t *testing.T) {
	stamp := at(t, "2026-10-28T15:30:00+08:00")
	var nano atomic.Int64
	nano.Store(stamp.UnixNano())
	clock := func() time.Time { return time.Unix(0, nano.Load()).UTC() }
	wantError := errors.New("upstream offline")
	calls := 0
	fetch := cached(func(context.Context, string, time.Time) (Report, error) {
		calls++
		if calls == 1 {
			return Report{}, wantError
		}
		return cacheReport(stamp), nil
	}, clock, time.Second)
	if _, err := fetch(context.Background(), "c2701", stamp); !errors.Is(err, wantError) {
		t.Fatalf("original fetch error lost: %v", err)
	}
	nano.Add(int64(1100 * time.Millisecond))
	for i := 0; i < 3; i++ {
		_, err := fetch(context.Background(), "c2701", stamp)
		var cooldown *CooldownError
		if !errors.Is(err, ErrCooldown) || !errors.As(err, &cooldown) || cooldown.RetrySeconds() != 4 || calls != 1 {
			t.Fatalf("failure cooldown missed: calls=%d err=%v", calls, err)
		}
	}
	nano.Add(int64(3900 * time.Millisecond))
	if r, err := fetch(context.Background(), "c2701", stamp); err != nil || calls != 2 || !r.AvailableAt.Equal(stamp) {
		t.Fatalf("recovery: calls=%d report=%+v err=%v", calls, r, err)
	}
}

func TestCachedSharedFetchDeadlineAndInvalidCalls(t *testing.T) {
	stamp := at(t, "2026-10-28T15:30:00+08:00")
	var calls atomic.Int32
	fetch := cached(func(ctx context.Context, _ string, _ time.Time) (Report, error) {
		calls.Add(1)
		<-ctx.Done()
		return Report{}, ctx.Err()
	}, time.Now, 20*time.Millisecond)
	started := time.Now()
	if _, err := fetch(context.Background(), "c2701", stamp); !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("shared timeout did not release waiter: %v", err)
	}
	if _, err := fetch(context.Background(), "c2701", stamp); !errors.Is(err, ErrCooldown) || calls.Load() != 1 {
		t.Fatalf("timeout did not cool down: %v calls=%d", err, calls.Load())
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetch(canceled, "c2705", stamp); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled request started: %v", err)
	}
	if _, err := fetch(context.Background(), "C0", stamp); err == nil {
		t.Fatal("continuous symbol accepted")
	}
	if _, err := fetch(context.Background(), "c2705", time.Time{}); err == nil {
		t.Fatal("missing maturity timestamp accepted")
	}
	if _, err := Cached(nil)(context.Background(), "c2705", stamp); err == nil {
		t.Fatal("nil fetcher accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("invalid call reached upstream")
	}
}
