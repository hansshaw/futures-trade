package marketdaily

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const cacheWindow = 5 * time.Second
const sharedFetchTimeout = 20 * time.Second

var ErrCooldown = errors.New("公开日线请求本地冷却中")

type CooldownError struct{ RetryAfter time.Duration }

func (e *CooldownError) Error() string {
	return fmt.Sprintf("%s；上次获取失败，请%d秒后重试", ErrCooldown, e.RetrySeconds())
}
func (e *CooldownError) Unwrap() error { return ErrCooldown }
func (e *CooldownError) RetrySeconds() int {
	return max(1, int((e.RetryAfter+time.Second-1)/time.Second))
}

type dailyFetch func(context.Context, string, time.Time) (Report, error)
type cacheKey struct {
	symbol, date string
	mature       bool
}
type cacheEntry struct {
	done      chan struct{}
	report    Report
	err       error
	completed time.Time
}

// Cached lets the current-analysis and daily-report paths share one read-only
// collector. Successful batches retain the source's AvailableAt; failures block
// new requests for five seconds. This is a local guard, not a provider quota.
//
// The shared fetch is independent of any individual caller's cancellation, but
// has a 20-second deadline. fetch must honor its context (Collector.Fetch does).
// Each waiter may cancel immediately without interrupting the other waiters.
func Cached(fetch func(context.Context, string, time.Time) (Report, error)) func(context.Context, string, time.Time) (Report, error) {
	return cached(fetch, time.Now, sharedFetchTimeout)
}

func cached(fetch dailyFetch, clock func() time.Time, timeout time.Duration) dailyFetch {
	var mu sync.Mutex
	entries := map[cacheKey]*cacheEntry{}
	return func(ctx context.Context, symbol string, asOf time.Time) (Report, error) {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		symbol = strings.ToLower(strings.TrimSpace(symbol))
		if !validSymbol(symbol) || asOf.IsZero() {
			return Report{}, errors.New("日线缓存需要实际合约与非零成熟度核验时刻")
		}
		if fetch == nil {
			return Report{}, errors.New("日线采集器未配置")
		}
		local := asOf.In(china)
		key := cacheKey{symbol, local.Format("2006-01-02"), local.Hour()*60+local.Minute() >= 15*60+15}
		mu.Lock()
		now := clock()
		// Completed entries are tiny-lived. Expired symbols/dates must not grow
		// indefinitely as actual contracts or the calendar change.
		for k, entry := range entries {
			if !entry.completed.IsZero() && !now.Before(entry.completed.Add(cacheWindow)) {
				delete(entries, k)
			}
		}
		entry, found := entries[key]
		if found && !entry.completed.IsZero() {
			if entry.err != nil {
				wait := entry.completed.Add(cacheWindow).Sub(now)
				mu.Unlock()
				return Report{}, &CooldownError{RetryAfter: wait}
			}
			out := cloneReport(entry.report)
			mu.Unlock()
			return out, nil
		}
		if !found {
			entry = &cacheEntry{done: make(chan struct{})}
			entries[key] = entry
			// Do not let the first HTTP caller's disconnect cancel an in-flight
			// report's request. The collector still has its own bounded deadline.
			shared, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
			go func() {
				defer cancel()
				report, err := fetch(shared, symbol, asOf)
				if shared.Err() != nil {
					err = shared.Err()
				}
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					entry.report = cloneReport(report)
				}
				entry.err, entry.completed = err, clock()
				close(entry.done)
			}()
		}
		mu.Unlock()
		select {
		case <-ctx.Done():
			return Report{}, ctx.Err()
		case <-entry.done:
			if err := ctx.Err(); err != nil {
				return Report{}, err
			}
			// Closing done publishes the immutable result to every waiter.
			return cloneReport(entry.report), entry.err
		}
	}
}

func cloneReport(r Report) Report {
	r.Bars = append(r.Bars[:0:0], r.Bars...)
	r.MissingOpenInterestDates = append(r.MissingOpenInterestDates[:0:0], r.MissingOpenInterestDates...)
	r.MissingSettlementDates = append(r.MissingSettlementDates[:0:0], r.MissingSettlementDates...)
	r.ExcludedIncomplete = append(r.ExcludedIncomplete[:0:0], r.ExcludedIncomplete...)
	r.Warnings = append(r.Warnings[:0:0], r.Warnings...)
	return r
}
