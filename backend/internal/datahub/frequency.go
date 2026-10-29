package datahub

import (
	"errors"
	"fmt"
	"time"
)

// This is a local burst guard, not a quota granted by any data provider.
const burstWindow = 5 * time.Second
const quoteCheckpointWindow = 5 * time.Second

type requestRuntime struct {
	starts  []time.Time
	since   time.Time
	last    time.Time
	latency time.Duration
}

func interval(c Config) time.Duration { return time.Duration(c.IntervalSeconds * float64(time.Second)) }

// Quote calls use start-to-start pacing. A slow call cannot be overlapped and
// there is no accumulated credit to issue a burst after it completes.
func successCacheUntil(d Definition, c Config, memo fetchMemo) time.Time {
	if d.Kind == "market" {
		return memo.started.Add(min(interval(c), burstWindow))
	}
	return memo.completed.Add(burstWindow)
}

func (h *Hub) requestStartedLocked(id string, now time.Time) {
	r := h.runtime[id]
	if r == nil {
		r = &requestRuntime{since: now}
		h.runtime[id] = r
	}
	r.prune(now)
	r.starts = append(r.starts, now)
	r.last = now
}
func (r *requestRuntime) prune(now time.Time) {
	cutoff := now.Add(-time.Minute)
	i := 0
	for i < len(r.starts) && !r.starts[i].After(cutoff) {
		i++
	}
	if i > 0 {
		r.starts = append([]time.Time(nil), r.starts[i:]...)
	}
}
func (h *Hub) pollingLocked(id string, now time.Time) Polling {
	c := h.configs[id]
	p := Polling{TargetIntervalMS: c.IntervalSeconds * 1000, PersistedAt: copyTime(h.records[id].PersistedAt), PersistenceNote: "每次同步保存到本地数据库"}
	if c.IntervalSeconds > 0 {
		p.TargetRequestsPerMinute = 60 / c.IntervalSeconds
	}
	if d, ok := h.definition(id); ok && d.Kind == "market" && interval(c) < quoteCheckpointWindow {
		p.PersistenceNote = "内存报价即时更新；自动采集最多每5秒保存快照，正常退出刷新。异常退出可能丢失末尾未保存快照。"
	}
	if r := h.runtime[id]; r != nil {
		r.prune(now)
		p.RequestsLastMinute = len(r.starts)
		p.LastLatencyMS = float64(r.latency) / float64(time.Millisecond)
		p.LastRequestAt, p.StatsSince = copyTime(&r.last), copyTime(&r.since)
	}
	return p
}

// Flush persists the last in-memory quote checkpoint. Poll invokes this after
// its workers stop, and callers may invoke it before closing the backing store.
func (h *Hub) Flush() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var first error
	for id := range h.dirty {
		r := cloneRecord(h.records[id])
		now := h.now()
		r.PersistedAt = &now
		if err := h.store.SetMeta("datahub_record_"+id, r); err != nil {
			if first == nil {
				first = errors.New("行情快照退出保存失败，请检查磁盘")
			}
			r.LastError = "行情快照保存失败；当前内存数据尚未全部持久化"
			r.PersistedAt = copyTime(h.records[id].PersistedAt)
			h.records[id] = r
			continue
		}
		h.records[id] = r
		delete(h.dirty, id)
	}
	return first
}

var ErrCooldown = errors.New("数据源本地请求冷却中")

type CooldownError struct{ RetryAfter time.Duration }

func (e *CooldownError) Error() string {
	return fmt.Sprintf("%s；上次请求失败，请%d秒后重试，原快照未改变", ErrCooldown, e.RetrySeconds())
}
func (e *CooldownError) Unwrap() error { return ErrCooldown }
func (e *CooldownError) RetrySeconds() int {
	return max(1, int((e.RetryAfter+time.Second-1)/time.Second))
}

type fetchMemo struct {
	binding            string
	started, completed time.Time
	success            bool
	batch              Batch
}

func (h *Hub) scheduledSkipLocked(d Definition, cfg Config) (Outcome, bool) {
	r := h.records[d.ID]
	reason := ""
	switch {
	case h.paused:
		reason = "自动采集已暂停"
	case !cfg.Enabled:
		reason = "数据源已停用"
	case d.ConfigurableEndpoint && cfg.Endpoint == "":
		reason = "桥接地址尚未配置"
	case r.NextRun != nil && r.NextRun.After(h.now()):
		reason = "尚未到配置的采集/退避时间"
	default:
		return Outcome{}, false
	}
	return Outcome{OK: r.LastSuccess != nil && r.LastError == "" && r.Binding == binding(cfg), Skipped: true,
		Message: reason + "；未请求上游，继续读取已有数据及其时间/健康状态", RecordCount: r.TotalRecords,
		FetchedAt: copyTime(r.LastSuccess), LatestDataAt: copyTime(r.LatestDataAt), Coverage: append([]string{}, r.Coverage...)}, true
}

func cloneBatch(b Batch) Batch {
	b.Quotes = append(b.Quotes[:0:0], b.Quotes...)
	b.Observations = append(b.Observations[:0:0], b.Observations...)
	b.Events = append(b.Events[:0:0], b.Events...)
	b.Coverage = append(b.Coverage[:0:0], b.Coverage...)
	for i := range b.Events {
		b.Events[i].Products = append(b.Events[i].Products[:0:0], b.Events[i].Products...)
		if original := b.Events[i].Analysis; original != nil {
			copied := *original
			copied.Reasoning = append([]string(nil), original.Reasoning...)
			copied.Evidence = append([]string(nil), original.Evidence...)
			copied.RiskFlags = append([]string(nil), original.RiskFlags...)
			b.Events[i].Analysis = &copied
		}
	}
	return b
}
