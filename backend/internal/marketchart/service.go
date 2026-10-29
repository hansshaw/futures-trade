package marketchart

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"
	"time"

	"cornwatch/internal/domain"
)

type persistence interface {
	GetMeta(string, any) error
	SetMeta(string, any) error
	Bars(string, bool) ([]domain.Bar, error)
}
type cacheRecord struct {
	Report      Report    `json:"report"`
	NextAttempt time.Time `json:"next_attempt"`
}
type entry struct {
	record  cacheRecord
	loading chan struct{}
}
type Service struct {
	mu      sync.Mutex
	store   persistence
	entries map[string]*entry
	daily   DailyFetcher
	minute  func(context.Context, string, string, time.Time) (Report, error)
	now     func() time.Time
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup
	closed  bool
}

func New(store persistence, daily DailyFetcher) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{store: store, daily: daily, minute: newMinuteCollector().fetch, entries: map[string]*entry{}, now: func() time.Time { return time.Now().UTC() }, ctx: ctx, cancel: cancel}
}
func (s *Service) Close() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock(); s.workers.Wait() }

func (s *Service) History(ctx context.Context, req Request) (Report, error) {
	req, e := req.normalized()
	if e != nil {
		return Report{}, e
	}
	if e = ctx.Err(); e != nil {
		return Report{}, e
	}
	if req.Source == "imported" {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return Report{}, errors.New("历史服务正在退出")
		}
		s.workers.Add(1)
		s.mu.Unlock()
		defer s.workers.Done()
		return s.imported(req)
	}
	key := req.Symbol + "_" + req.Interval
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Report{}, errors.New("历史服务正在退出")
	}
	item := s.entries[key]
	if item == nil {
		item = &entry{}
		err := s.store.GetMeta("market_chart_v1_"+key, &item.record)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			s.mu.Unlock()
			return Report{}, errors.New("历史缓存读取失败，未请求上游")
		}
		if err == nil && len(item.record.Report.Bars) > 0 {
			if err = validateReport(item.record.Report, req, s.now()); err != nil {
				s.mu.Unlock()
				return Report{}, errors.New("历史缓存校验失败，未使用损坏记录")
			}
		}
		// Bound transient memory; persisted history remains queryable on demand.
		if len(s.entries) >= 64 {
			for k, v := range s.entries {
				if v.loading == nil {
					delete(s.entries, k)
					break
				}
			}
		}
		s.entries[key] = item
	}
	now := s.now()
	if item.loading == nil && now.Before(item.record.NextAttempt) {
		report := clone(item.record.Report)
		next := item.record.NextAttempt
		s.mu.Unlock()
		report.Cached = true
		if len(report.Bars) == 0 {
			return report, errors.New(report.LastError)
		}
		return selectRange(report, req, now, next), nil
	}
	if item.loading == nil {
		item.loading = make(chan struct{})
		s.workers.Add(1)
		go s.refresh(key, req, item)
	}
	done := item.loading
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return Report{}, ctx.Err()
	case <-done:
	}
	if e = ctx.Err(); e != nil {
		return Report{}, e
	}
	s.mu.Lock()
	out := clone(item.record.Report)
	next := item.record.NextAttempt
	s.mu.Unlock()
	if len(out.Bars) == 0 {
		return out, errors.New(out.LastError)
	}
	return selectRange(out, req, s.now(), next), nil
}
func (s *Service) refresh(key string, req Request, item *entry) {
	defer s.workers.Done()
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	started := s.now()
	var fresh Report
	var err error
	if req.Interval == "day" {
		if s.daily == nil {
			err = errors.New("公开日线采集器未配置")
		} else {
			var dErr error
			d, dErr := s.daily(ctx, req.Symbol, started)
			err = dErr
			if err == nil {
				fresh = Report{Symbol: req.Symbol, Interval: "day", Bars: []Bar{}, Source: d.Source, SourceURL: d.SourceURL, AvailableAt: d.AvailableAt, Warnings: append([]string{}, d.Warnings...), HasOpenInterest: d.HasOpenInterest, HasSettlement: d.HasSettlement}
				for _, b := range d.Bars {
					fresh.Bars = append(fresh.Bars, Bar{Bar: b, AvailableAt: d.AvailableAt})
				}
			}
		}
	} else {
		fresh, err = s.minute(ctx, req.Symbol, req.Interval, started)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		err = validateReport(fresh, req, s.now())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := clone(item.record.Report)
	now := s.now()
	next := now.Add(RefreshInterval)
	if err == nil {
		fresh.Bars = merge(out.Bars, fresh.Bars)
		if len(out.Bars) > 0 {
			// Archive coverage must not become "complete" merely because a
			// smaller, newer window happens to contain every optional field.
			fresh.HasOpenInterest = fresh.HasOpenInterest && out.HasOpenInterest
			fresh.HasSettlement = fresh.HasSettlement && out.HasSettlement
		}
		fresh.Cached = false
		fresh.Stale = false
		fresh.LastError = ""
		fresh.Warnings = append(fresh.Warnings, "历史按实际合约和周期独立保存，最多保留最近6000根；不是完整历史订阅。每根 available_at 为本机首次取得该版本的时刻，未回填为历史发布时刻。")
		candidate := cacheRecord{Report: fresh, NextAttempt: next}
		if e := s.store.SetMeta("market_chart_v1_"+key, candidate); e != nil {
			err = errors.New("历史缓存保存失败，已保留上次成功数据")
		} else {
			out = fresh
		}
	}
	if err != nil {
		out.Symbol = req.Symbol
		out.Interval = req.Interval
		out.Cached = len(out.Bars) > 0
		out.Stale = true
		out.LastError = "历史更新失败：" + err.Error()
		// Keep the last good cache durable. Failed attempts are also persisted so
		// restarting does not bypass the public endpoint's one-minute cooldown.
		_ = s.store.SetMeta("market_chart_v1_"+key, cacheRecord{Report: out, NextAttempt: next})
	}
	item.record = cacheRecord{Report: out, NextAttempt: next}
	close(item.loading)
	item.loading = nil
}
func validateReport(r Report, req Request, now time.Time) error {
	if r.Symbol != req.Symbol || r.Interval != req.Interval || len(r.Bars) == 0 || len(r.Bars) > MaxBars || r.AvailableAt.IsZero() || r.AvailableAt.After(now.Add(time.Second)) {
		return errors.New("历史元信息或可用时刻无效")
	}
	var last time.Time
	for _, b := range r.Bars {
		if !validBar(b.Bar, req.Symbol) || !mature(b.Bar, req.Interval, now) || (!last.IsZero() && !b.Timestamp.After(last)) || b.AvailableAt.IsZero() || b.AvailableAt.After(now.Add(time.Second)) {
			return errors.New("历史记录无效、重复、未完成或混入演示数据")
		}
		last = b.Timestamp
	}
	return nil
}
func merge(old, fresh []Bar) []Bar {
	byTime := make(map[int64]Bar, len(old)+len(fresh))
	for _, b := range old {
		byTime[b.Timestamp.UnixNano()] = b
	}
	for _, b := range fresh {
		key := b.Timestamp.UnixNano()
		if previous, ok := byTime[key]; ok && sameBar(previous.Bar, b.Bar) {
			b.AvailableAt = previous.AvailableAt
		}
		byTime[key] = b
	}
	out := make([]Bar, 0, len(byTime))
	for _, b := range byTime {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	if len(out) > MaxBars {
		out = out[len(out)-MaxBars:]
	}
	return out
}
func sameBar(a, b domain.Bar) bool {
	if !a.Timestamp.Equal(b.Timestamp) {
		return false
	}
	// JSON reload can use a different Location pointer for the same instant.
	a.Timestamp = b.Timestamp
	return a == b
}
func clone(r Report) Report {
	r.Bars = append([]Bar{}, r.Bars...)
	r.Warnings = append([]string{}, r.Warnings...)
	return r
}
func selectRange(r Report, req Request, now, next time.Time) Report {
	r = clone(r)
	eligible := make([]Bar, 0, len(r.Bars))
	for _, b := range r.Bars {
		if mature(b.Bar, req.Interval, now) && (b.AvailableAt.IsZero() || !b.AvailableAt.After(now)) {
			eligible = append(eligible, b)
		}
	}
	if len(eligible) != len(r.Bars) {
		r.Warnings = append(r.Warnings, "因系统时钟或资料时点变化，已隐藏当前时刻尚未完成或尚不可得的缓存记录。")
	}
	r.Bars = eligible
	r.TotalAvailable = len(r.Bars)
	if len(r.Bars) > 0 {
		r.RangeStart = r.Bars[0].Timestamp.Format(time.RFC3339)
		r.RangeEnd = r.Bars[len(r.Bars)-1].Timestamp.Format(time.RFC3339)
		v := r.Bars[len(r.Bars)-1].Timestamp
		r.LastBarAt = &v
	}
	r.RefreshAfterSeconds = max(0, int(math.Ceil(next.Sub(now).Seconds())))
	if r.AvailableAt.IsZero() || now.Sub(r.AvailableAt) > 2*RefreshInterval {
		r.Stale = true
	}
	filtered := make([]Bar, 0, len(r.Bars))
	for _, b := range r.Bars {
		day := b.Timestamp.In(china).Format("2006-01-02")
		if req.From != "" && day < req.From || req.To != "" && day > req.To {
			continue
		}
		filtered = append(filtered, b)
	}
	if len(filtered) > req.Limit {
		filtered = filtered[len(filtered)-req.Limit:]
	}
	r.Bars = filtered
	if len(filtered) == 0 {
		r.Warnings = append(r.Warnings, "所选日期内没有已保存的历史记录；没有补零或补造K线。")
	}
	return r
}
func (s *Service) imported(req Request) (Report, error) {
	bars, e := s.store.Bars(req.Symbol, false)
	if e != nil {
		return Report{}, errors.New("读取本地真实日线失败")
	}
	now := s.now()
	r := Report{Symbol: req.Symbol, Interval: "day", Source: "本地导入真实日线", Bars: []Bar{}, Cached: true, HasOpenInterest: true, HasSettlement: true, Warnings: []string{"仅展示本地导入的真实日线，不混入演示数据或公开日线；原CSV需自行核验。当前存储没有逐根文件来源及首次可用时间，不能据此证明历史当时可得。"}}
	for _, b := range bars {
		if !validBar(b, req.Symbol) {
			return r, errors.New("本地日线校验失败")
		}
		if !mature(b, "day", now) {
			continue
		}
		r.Bars = append(r.Bars, Bar{Bar: b})
		if b.Settlement == 0 {
			r.HasSettlement = false
		}
	}
	if len(r.Bars) > MaxBars {
		r.Bars = r.Bars[len(r.Bars)-MaxBars:]
		r.Warnings = append(r.Warnings, fmt.Sprintf("本次最多展示最近%d根本地日线。", MaxBars))
	}
	out := selectRange(r, req, now, now)
	out.Stale = false
	return out, nil
}
