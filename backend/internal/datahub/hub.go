package datahub

import (
	"context"
	"cornwatch/internal/collector"
	"cornwatch/internal/domain"
	"cornwatch/internal/importcorn"
	"cornwatch/internal/industry"
	"cornwatch/internal/industrypublic"
	"cornwatch/internal/market"
	"cornwatch/internal/store"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrBusy = errors.New("该数据源任务正在运行，请稍后重试")
var ErrConflict = errors.New("配置版本冲突，请刷新后合并修改")

// Keeping persistence behind this narrow interface also lets fault tests verify
// that a failed disk write cannot mutate the previously committed snapshot.
type persistence interface {
	SetMeta(string, any) error
	AddObservations([]domain.Observation) (int, error)
	AddEvent(domain.Event) (domain.Event, bool, error)
}

type Hub struct {
	mu       sync.Mutex
	store    persistence
	defs     []Definition
	configs  map[string]Config
	records  map[string]Record
	busy     map[string]bool
	pending  map[string]bool
	fetchers map[string]Fetcher
	fetched  map[string]fetchMemo
	runtime  map[string]*requestRuntime
	dirty    map[string]bool
	notify   func(string)
	paused   bool
	now      func() time.Time
}

func New(s *store.Store) (*Hub, error) {
	h := &Hub{store: s, defs: definitions(), configs: map[string]Config{}, records: map[string]Record{}, busy: map[string]bool{}, pending: map[string]bool{}, fetchers: map[string]Fetcher{}, fetched: map[string]fetchMemo{}, runtime: map[string]*requestRuntime{}, dirty: map[string]bool{}, paused: os.Getenv("CORNWATCH_AUTO_SYNC") == "false", now: func() time.Time { return time.Now().UTC() }}
	for _, d := range h.defs {
		c := Config{CadenceVersion: 1, Revision: 1, Enabled: !d.ConfigurableEndpoint, IntervalSeconds: d.DefaultInterval}
		if d.ID == "weather" && os.Getenv("CORNWATCH_WEATHER_ENABLED") == "false" {
			c.Enabled = false
		}
		// Decode into a zero-valued destination so absent migration fields from
		// existing installs remain distinguishable from a new default config.
		var saved Config
		loadErr := s.GetMeta("datahub_config_"+d.ID, &saved)
		if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
			return nil, loadErr
		}
		if loadErr == nil {
			c = saved
		}
		if c.Revision == 0 || !validInterval(c.IntervalSeconds, d) {
			return nil, fmt.Errorf("数据源%s配置损坏，请从备份恢复", d.ID)
		}
		if loadErr == nil && c.CadenceVersion < 1 {
			if d.ID == "sina" && c.IntervalSeconds == 60 || d.ID == "market_bridge" && c.IntervalSeconds == 10 {
				c.IntervalSeconds = 0.5
				c.Revision++
			}
			c.CadenceVersion = 1
			if err := s.SetMeta("datahub_config_"+d.ID, c); err != nil {
				return nil, fmt.Errorf("数据源%s刷新频率迁移保存失败: %w", d.ID, err)
			}
		}
		h.configs[d.ID] = c
		var record Record
		if err := s.GetMeta("datahub_record_"+d.ID, &record); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		// Saving a config is one atomic metadata write. A revision mismatch
		// invalidates the old schedule after a restart as well, including when
		// only the interval changed and the connection binding stayed the same.
		if record.Binding != binding(c) || record.ConfigRevision != c.Revision {
			record.NextRun = nil
		}
		h.records[d.ID] = record
	}
	public := market.New()
	official := industry.New()
	weather := collector.New()
	enterprise := industrypublic.New()
	imports := importcorn.New()
	h.fetchers["sina"] = func(ctx context.Context, _ Config) (Batch, error) {
		q, e := public.Collect(ctx)
		return quoteBatch(q), e
	}
	h.fetchers["liaoning"] = func(ctx context.Context, _ Config) (Batch, error) {
		o, e, err := official.Liaoning(ctx)
		return observationBatch(o, e), err
	}
	h.fetchers["moa"] = func(ctx context.Context, _ Config) (Batch, error) {
		o, e, err := official.MOAFeed(ctx)
		return observationBatch(o, e), err
	}
	h.fetchers["boyar_prices"] = func(ctx context.Context, _ Config) (Batch, error) {
		o, e, err := enterprise.Prices(ctx)
		return observationBatch(o, e), err
	}
	h.fetchers["boyar_arrivals"] = func(ctx context.Context, _ Config) (Batch, error) {
		o, e, err := enterprise.Arrivals(ctx)
		return observationBatch(o, e), err
	}
	h.fetchers["import_auction"] = func(ctx context.Context, _ Config) (Batch, error) {
		o, e, err := imports.Auction(ctx)
		b := observationBatch(o, e)
		if err == nil && len(o) == 0 && len(e) == 0 {
			b.NoPublication = "本次公开栏目未发现进口玉米成交结果；不以零成交入库，保留以前的场次。"
		}
		return b, err
	}
	h.fetchers["weather"] = func(ctx context.Context, _ Config) (Batch, error) {
		o, err := weather.Weather(ctx)
		return weatherBatch(o), err
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("桥接接口不允许重定向") }}
	h.fetchers["market_bridge"] = func(ctx context.Context, c Config) (Batch, error) {
		return fetchBridgeWithClock(ctx, client, c, true, h.now)
	}
	h.fetchers["industry_bridge"] = func(ctx context.Context, c Config) (Batch, error) {
		return fetchBridgeWithClock(ctx, client, c, false, h.now)
	}
	return h, nil
}
func (h *Hub) SetNotify(f func(string)) { h.mu.Lock(); h.notify = f; h.mu.Unlock() }
func (h *Hub) signal()                  { h.signalKind("data-sources") }
func (h *Hub) signalKind(kind string) {
	h.mu.Lock()
	f := h.notify
	h.mu.Unlock()
	if f != nil {
		f(kind)
	}
}
func quoteBatch(q []market.Quote) Batch {
	b := Batch{Quotes: q}
	set := map[string]bool{}
	for _, v := range q {
		if v.ObservedAt.After(b.DataAt) {
			b.DataAt = v.ObservedAt
		}
		set[v.Product] = true
	}
	for k := range set {
		b.Coverage = append(b.Coverage, k)
	}
	sort.Strings(b.Coverage)
	return b
}
func observationBatch(o []domain.Observation, e []domain.Event) Batch {
	b := Batch{Observations: o, Events: e}
	set := map[string]bool{}
	for _, v := range o {
		if v.ObservedAt.After(b.DataAt) {
			b.DataAt = v.ObservedAt
		}
		set[v.Region] = true
	}
	for k := range set {
		b.Coverage = append(b.Coverage, k)
	}
	sort.Strings(b.Coverage)
	return b
}
func weatherBatch(items []domain.Observation) Batch {
	b := observationBatch(items, nil)
	b.DataAt = time.Time{}
	for _, o := range items {
		if o.AvailableAt.After(b.DataAt) {
			b.DataAt = o.AvailableAt
		}
		if o.Unit == "mm" && o.Value >= 25 {
			b.Events = append(b.Events, domain.Event{ID: store.ID("weather-alert", o.ID), Title: o.Region + " 降水预报达到关注阈值", Body: fmt.Sprintf("%s 预报 %.1f mm。阈值25mm为研究规则，不是官方气象预警；需结合收获期和区域覆盖核验。", o.Entity, o.Value), Category: "weather", Region: o.Region, Products: []string{"c"}, Source: o.Source, SourceURL: o.SourceURL, PublishedAt: o.AvailableAt, IngestedAt: o.AvailableAt, AvailableAt: o.AvailableAt, Severity: "high", IsDemo: o.IsDemo})
		}
	}
	return b
}
func binding(c Config) string {
	sum := sha256.Sum256([]byte(c.Endpoint + "\x00" + c.Token))
	return hex.EncodeToString(sum[:])
}

// Credentials identify a verified connection, but are not part of the data's
// identity: rotating a token must not duplicate an unchanged observation.
func datasetBinding(c Config) string {
	sum := sha256.Sum256([]byte(c.Endpoint))
	return hex.EncodeToString(sum[:])
}
func endpointOrigin(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else if u.Scheme == "http" {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}
func (h *Hub) definition(id string) (Definition, bool) {
	for _, d := range h.defs {
		if d.ID == id {
			return d, true
		}
	}
	return Definition{}, false
}
func cfgView(c Config) ConfigView {
	return ConfigView{c.Revision, c.Enabled, c.IntervalSeconds, c.Endpoint, c.Token != ""}
}
func validateEndpoint(value string) error {
	if len(value) > 2000 {
		return errors.New("接口地址过长")
	}
	u, e := url.Parse(value)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return errors.New("填写完整HTTPS接口地址；本机桥接可用HTTP。令牌使用独立字段，不放在URL中")
	}
	local := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return errors.New("远程接口必须HTTPS；HTTP仅限本机回环地址")
	}
	return nil
}
func (h *Hub) Save(id string, r ConfigRequest) (Source, error) {
	h.mu.Lock()
	d, ok := h.definition(id)
	if !ok {
		h.mu.Unlock()
		return Source{}, os.ErrNotExist
	}
	c := h.configs[id]
	if h.busy[id] {
		h.mu.Unlock()
		return Source{}, ErrBusy
	}
	if r.Revision != c.Revision {
		h.mu.Unlock()
		return Source{}, ErrConflict
	}
	if !validInterval(r.IntervalSeconds, d) {
		h.mu.Unlock()
		return Source{}, fmt.Errorf("采集间隔需在%g至%g秒之间，精确到毫秒", d.MinIntervalSeconds, d.MaxIntervalSeconds)
	}
	if !d.ConfigurableEndpoint && (r.Endpoint != "" || r.Token != "" || r.ClearToken) {
		h.mu.Unlock()
		return Source{}, errors.New("固定公开来源不可修改地址或凭据")
	}
	if r.Endpoint != "" {
		if err := validateEndpoint(r.Endpoint); err != nil {
			h.mu.Unlock()
			return Source{}, err
		}
	}
	if r.ClearToken && r.Token != "" {
		h.mu.Unlock()
		return Source{}, errors.New("不能同时填写和删除令牌")
	}
	if len(r.Token) > 4096 || strings.ContainsAny(r.Token, "\r\n\x00") {
		h.mu.Unlock()
		return Source{}, errors.New("令牌格式无效")
	}
	// An empty token means "keep" only within the same origin. This prevents
	// a saved credential from silently following an endpoint to another host.
	if r.ClearToken || endpointOrigin(c.Endpoint) != endpointOrigin(r.Endpoint) {
		c.Token = ""
	}
	if r.Token != "" {
		c.Token = r.Token
	}
	c.Enabled = r.Enabled
	c.Endpoint = r.Endpoint
	c.IntervalSeconds = r.IntervalSeconds
	c.Revision++
	if err := h.store.SetMeta("datahub_config_"+id, c); err != nil {
		h.mu.Unlock()
		return Source{}, errors.New("配置保存失败")
	}
	h.configs[id] = c
	// The scheduler reevaluates the new interval immediately; old backoff does not
	// pin a corrected endpoint. Existing data still carries the previous binding.
	rec := h.records[id]
	rec.NextRun = nil
	h.records[id] = rec
	result := h.sourceLocked(d, h.now())
	h.mu.Unlock()
	h.signal()
	return result, nil
}
func (h *Hub) Sources() []Source {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []Source{}
	for _, d := range h.defs {
		out = append(out, h.sourceLocked(d, h.now()))
	}
	return out
}
func (h *Hub) Paused() bool { return h.paused }
func (h *Hub) sourceLocked(d Definition, now time.Time) Source {
	c, r := h.configs[d.ID], h.records[d.ID]
	status := "idle"
	if !c.Enabled {
		status = "disabled"
	} else if d.ConfigurableEndpoint && c.Endpoint == "" {
		status = "unconfigured"
	} else if r.LastError != "" {
		status = "error"
	} else if r.LastSuccess != nil {
		status = "ok"
	}
	if h.busy[d.ID] {
		status = "running"
	}
	fresh, note := freshness(d.ID, r, now)
	if r.NoPublication != "" {
		note = r.NoPublication + " " + note
	}
	if r.LastSuccess != nil && r.Binding != binding(c) {
		fresh = "stale"
		note = "配置已变化，保留的是旧连接快照；请重新同步验证"
	}
	if !c.Enabled && r.LastSuccess != nil {
		note = "自动更新已停用；" + note
	}
	cov := append([]string{}, r.Coverage...)
	next := copyTime(r.NextRun)
	if !c.Enabled || h.paused || c.Endpoint == "" && d.ConfigurableEndpoint {
		next = nil
	}
	return Source{Definition: d, Config: cfgView(c), Polling: h.pollingLocked(d.ID, now), Status: status, Freshness: fresh, FreshnessNote: note, LastAttempt: copyTime(r.LastAttempt), LastSuccess: copyTime(r.LastSuccess), LatestDataAt: copyTime(r.LatestDataAt), NextRun: next, LastError: r.LastError, ConsecutiveFailures: r.ConsecutiveFailures, Added: r.Added, Duplicates: r.Duplicates, TotalRecords: r.TotalRecords, Coverage: cov}
}
func validInterval(v float64, d Definition) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= d.MinIntervalSeconds && v <= d.MaxIntervalSeconds && math.Abs(v*1000-math.Round(v*1000)) < 0.000001
}
func freshness(id string, r Record, now time.Time) (string, string) {
	if r.LastSuccess == nil {
		return "never", "尚未成功同步"
	}
	if r.LatestDataAt == nil {
		return "unknown", "来源未提供可核验的数据时间"
	}
	age := now.Sub(*r.LatestDataAt)
	if age < -5*time.Minute {
		return "unknown", "数据时间晚于本机时间，请核验时区/时钟"
	}
	if id == "weather" {
		if now.Sub(*r.LastSuccess) > 3*time.Hour {
			return "stale", "预报采集已超过3小时；目标日期仍可能有效，但批次未更新"
		}
		return "fresh", "显示预报采集时刻；不是天气实况或源模型签发时间"
	}
	if id == "liaoning" || id == "moa" {
		if age > 14*24*time.Hour {
			return "stale", "最新监测日期已超过14天；请查看原始发布页"
		}
		return "fresh", "周度监测数据；统计日与实际抓取时间不同"
	}
	if id == "boyar_prices" || id == "boyar_arrivals" {
		if age > 4*24*time.Hour {
			return "stale", "最新公开统计日已超过4天；可能遇休市/延迟/停更，请查看原文。"
		}
		return "fresh", "延迟公开记录；时效按原始统计日判断，不是当前厂门实时成交或到车。"
	}
	if id == "import_auction" {
		if age > 14*24*time.Hour {
			return "stale", "最近公开拍卖场次超过14天；无新公告不等于成交为零。"
		}
		return "fresh", "不定期拍卖场次，按日检查；不是全国每日新增进口成交统计。"
	}
	if id == "industry_bridge" {
		if age > 24*time.Hour {
			return "stale", "产业观测时间已超过24小时，需按供应商统计频率核验"
		}
		return "fresh", "按供应商观测时间判断；采集成功不代表企业仍按此价收购"
	}
	if age <= 3*time.Minute {
		return "fresh", "报价时间在3分钟以内；不构成延迟或可成交性保证"
	}
	china := time.FixedZone("Asia/Shanghai", 8*3600)
	t := now.In(china)
	q := r.LatestDataAt.In(china)
	minute := t.Hour()*60 + t.Minute()
	expected := t.Weekday() != time.Saturday && t.Weekday() != time.Sunday && ((minute >= 9*60 && minute < 10*60+15) || (minute >= 10*60+30 && minute < 11*60+30) || (minute >= 13*60+30 && minute < 15*60) || (minute >= 21*60 && minute < 23*60))
	if !expected && age <= 96*time.Hour && q.Hour() >= 14 {
		return "market_closed", "当前为常规休市/间歇时段，保留最近报价；未接官方节假日日历，仍需核对数据日期"
	}
	return "stale", "报价时间未跟进，请核验数据源或交易日历；不会把本次抓取时间当作报价时间"
}
func (h *Hub) Run(ctx context.Context, id string, test bool) (Outcome, error) {
	return h.run(ctx, id, test, false)
}

// RunScheduled shares the normal collector and storage path, but cannot bypass
// source enablement, pause, the configured interval or failure backoff. Reports
// should use this method and read the saved snapshot even when a run is skipped.
func (h *Hub) RunScheduled(ctx context.Context, id string) (Outcome, error) {
	return h.run(ctx, id, false, true)
}

func (h *Hub) run(ctx context.Context, id string, test, scheduled bool) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	h.mu.Lock()
	d, ok := h.definition(id)
	if !ok {
		h.mu.Unlock()
		return Outcome{}, os.ErrNotExist
	}
	cfg := h.configs[id]
	if h.pending[id] {
		h.mu.Unlock()
		return Outcome{}, fmt.Errorf("%w：正在保存该源配置", ErrBusy)
	}
	if scheduled {
		if out, skip := h.scheduledSkipLocked(d, cfg); skip {
			h.mu.Unlock()
			return out, nil
		}
	}
	if h.busy[id] {
		h.mu.Unlock()
		return Outcome{}, ErrBusy
	}
	if d.ConfigurableEndpoint && cfg.Endpoint == "" {
		h.mu.Unlock()
		return Outcome{}, errors.New("请先填写并保存授权桥接地址")
	}
	fetch := h.fetchers[id]
	memo, cached := h.fetched[id], false
	started := h.now()
	if memo.binding == binding(cfg) && !memo.completed.IsZero() {
		if !memo.success && started.Before(memo.completed.Add(burstWindow)) {
			wait := memo.completed.Add(burstWindow).Sub(started)
			h.mu.Unlock()
			return Outcome{}, &CooldownError{RetryAfter: wait}
		}
		if memo.success && started.Before(successCacheUntil(d, cfg, memo)) {
			cached = true
			started = memo.started
		}
	}
	if !cached {
		h.requestStartedLocked(id, started)
	}
	h.busy[id] = true
	if !test {
		r := h.records[id]
		r.LastAttempt = &started
		h.records[id] = r
	}
	h.mu.Unlock()
	if d.Kind != "market" {
		h.signal()
	}
	timeout, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	var b Batch
	var err error
	fetchedAt := memo.completed
	if cached {
		b = cloneBatch(memo.batch)
	} else {
		b, err = fetch(timeout, cfg)
		fetchedAt = h.now()
	}
	count := len(b.Quotes) + len(b.Observations) + len(b.Events)
	if err == nil && count == 0 && b.NoPublication == "" {
		err = errors.New("数据源未返回任何有效记录；已保留原快照")
	}
	out := Outcome{OK: err == nil, Cached: cached, RecordCount: count, Coverage: append([]string{}, b.Coverage...)}
	if err == nil {
		out.FetchedAt = copyTime(&fetchedAt)
	}
	if !b.DataAt.IsZero() {
		t := b.DataAt.UTC()
		out.LatestDataAt = &t
	}
	h.mu.Lock()
	if !cached {
		h.fetched[id] = fetchMemo{binding: binding(cfg), started: started, completed: fetchedAt, success: err == nil, batch: cloneBatch(b)}
		h.runtime[id].latency = max(0, fetchedAt.Sub(started))
	}
	if test {
		delete(h.busy, id)
		h.mu.Unlock()
		h.signal()
		if err != nil {
			return Outcome{}, safeError(err, cfg)
		}
		out.Message = fmt.Sprintf("连接与字段校验通过：%d条记录，尚未写入数据库", count) + b.NoPublication
		if cached {
			out.Message += "（复用间隔内已校验批次，未再次请求上游）"
		}
		return out, nil
	}
	r := cloneRecord(h.records[id])
	now := h.now()
	if err == nil {
		// Whole batches are parsed before import. A write error is surfaced; retry
		// remains safe because observations/events are immutable and idempotent.
		if len(b.Observations) > 0 {
			out.Added, err = h.store.AddObservations(b.Observations)
		}
		out.Duplicates = len(b.Observations) - out.Added
		if err == nil {
			for _, e := range b.Events {
				_, added, eerr := h.store.AddEvent(e)
				if eerr != nil {
					err = eerr
					break
				}
				if added {
					out.Added++
				} else {
					out.Duplicates++
				}
			}
		}
		if err == nil && len(b.Quotes) > 0 {
			// Bridge quotes are stamped with the endpoint by fetchBridge. Compare
			// that data identity rather than the credential fingerprint so token
			// rotation preserves accumulated close observations and price trails.
			if d.ConfigurableEndpoint && (len(r.Quotes) == 0 || r.Quotes[0].SourceURL != cfg.Endpoint) {
				r.Main = market.State{}
				r.PriceHistory = nil
				r.Quotes = nil
			}
			old := map[string]market.Quote{}
			for _, q := range r.Quotes {
				old[q.Symbol] = q
			}
			for _, q := range b.Quotes {
				if p, ok := old[q.Symbol]; ok && p.ObservedAt.Equal(q.ObservedAt) && p.Last == q.Last && p.Volume == q.Volume && p.OpenInterest == q.OpenInterest {
					out.Duplicates++
				} else {
					out.Added++
				}
			}
			if r.PriceHistory == nil {
				r.PriceHistory = map[string][]PricePoint{}
			}
			for _, q := range b.Quotes {
				points := r.PriceHistory[q.Symbol]
				if len(points) > 0 && q.ObservedAt.Equal(points[len(points)-1].ObservedAt) {
					// Some sources timestamp to one second while updates arrive more
					// often. Keep one point per source timestamp with its latest price.
					points[len(points)-1].Last = q.Last
					r.PriceHistory[q.Symbol] = points
				}
				if len(points) == 0 || q.ObservedAt.After(points[len(points)-1].ObservedAt) {
					points = append(points, PricePoint{q.ObservedAt, q.Last})
					if len(points) > 120 {
						points = points[len(points)-120:]
					}
					r.PriceHistory[q.Symbol] = points
				}
			}
			// Expired contracts and old providers must not accumulate an unbounded trail.
			for sym, points := range r.PriceHistory {
				if len(points) == 0 || now.Sub(points[len(points)-1].ObservedAt) > 7*24*time.Hour {
					delete(r.PriceHistory, sym)
				}
			}
			r.Quotes = append([]market.Quote{}, b.Quotes...)
			r.Main = market.Update(r.Main, b.Quotes, fetchedAt)
		}
	}
	if err == nil {
		r.LastSuccess = copyTime(&fetchedAt)
		if out.LatestDataAt != nil {
			r.LatestDataAt = copyTime(out.LatestDataAt)
		}
		r.NoPublication = b.NoPublication
		r.LastError = ""
		r.ConsecutiveFailures = 0
		r.Added = out.Added
		r.Duplicates = out.Duplicates
		r.TotalRecords = count
		if len(out.Coverage) > 0 {
			r.Coverage = append([]string{}, out.Coverage...)
		}
		r.Binding = binding(cfg)
		out.Message = fmt.Sprintf("同步完成：新增/更新%d条，重复%d条", out.Added, out.Duplicates) + b.NoPublication
		if cached {
			out.Message += "（复用间隔内已校验批次，未再次请求上游）"
		}
	} else {
		r.LastError = safeError(err, cfg).Error()
		r.ConsecutiveFailures++
		out.OK = false
	}
	next := now.Add(runDelay(cfg, r.ConsecutiveFailures))
	if err == nil {
		next = fetchedAt.Add(runDelay(cfg, 0))
		if d.Kind == "market" {
			next = started.Add(runDelay(cfg, 0))
			if next.Before(now) {
				next = now
			}
		}
	}
	r.ConfigRevision = cfg.Revision
	r.NextRun = &next
	// High-frequency automatic quotes are visible immediately but checkpointed
	// at most every five seconds. Manual syncs, errors and main-state changes
	// remain durable before success is returned.
	previous := h.records[id]
	mainChanged := r.Main.LastEvaluatedDate != previous.Main.LastEvaluatedDate || r.Main.Decision.Symbol != previous.Main.Decision.Symbol || len(r.Main.History) != len(previous.Main.History)
	checkpoint := !scheduled || d.Kind != "market" || interval(cfg) >= quoteCheckpointWindow || err != nil || mainChanged || previous.PersistedAt == nil || !now.Before(previous.PersistedAt.Add(quoteCheckpointWindow)) || previous.Binding != r.Binding
	var persistErr error
	if checkpoint {
		r.PersistedAt = copyTime(&now)
		persistErr = h.store.SetMeta("datahub_record_"+id, r)
	}
	if persistErr == nil {
		h.records[id] = r
		if checkpoint {
			delete(h.dirty, id)
		} else {
			h.dirty[id] = true
		}
	} else {
		err = errors.New("同步状态持久化失败，请检查本地磁盘")
		old := cloneRecord(h.records[id])
		old.LastError = err.Error()
		old.ConsecutiveFailures++
		next = now.Add(runDelay(cfg, old.ConsecutiveFailures))
		old.NextRun = &next
		h.records[id] = old
	}
	delete(h.busy, id)
	h.mu.Unlock()
	if d.Kind == "market" {
		h.signalKind("market")
	} else {
		h.signal()
	}
	if err != nil {
		return Outcome{}, safeError(err, cfg)
	}
	return out, nil
}
func runDelay(c Config, failures int) time.Duration {
	wait := interval(c)
	if failures <= 0 {
		return wait
	}
	// A failure must never make a deliberately low-frequency source run sooner
	// than its configured normal cadence (which can be up to seven days).
	return min(max(wait, 2500*time.Millisecond)*time.Duration(1<<min(failures, 6)), max(wait, 24*time.Hour))
}
func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}
func cloneRecord(r Record) Record {
	r.LastAttempt, r.LastSuccess = copyTime(r.LastAttempt), copyTime(r.LastSuccess)
	r.LatestDataAt, r.NextRun = copyTime(r.LatestDataAt), copyTime(r.NextRun)
	r.PersistedAt = copyTime(r.PersistedAt)
	r.Coverage = append([]string(nil), r.Coverage...)
	r.Quotes = append([]market.Quote(nil), r.Quotes...)
	r.Main.History = append([]market.RollEvent(nil), r.Main.History...)
	r.Main.Snapshots = append([]market.CloseSnapshot(nil), r.Main.Snapshots...)
	for i := range r.Main.Snapshots {
		r.Main.Snapshots[i].Quotes = append([]market.Quote(nil), r.Main.Snapshots[i].Quotes...)
	}
	if r.PriceHistory != nil {
		trails := make(map[string][]PricePoint, len(r.PriceHistory))
		for sym, points := range r.PriceHistory {
			trails[sym] = append([]PricePoint(nil), points...)
		}
		r.PriceHistory = trails
	}
	return r
}
func safeError(err error, c Config) error {
	msg := err.Error()
	if c.Token != "" {
		msg = strings.ReplaceAll(msg, c.Token, "[redacted]")
	}
	if c.Endpoint != "" {
		msg = strings.ReplaceAll(msg, c.Endpoint, "[bridge]")
	}
	if len(msg) > 600 {
		msg = msg[:600]
	}
	return errors.New(msg)
}
func (h *Hub) Poll(ctx context.Context) {
	if h.paused {
		return
	}
	var workers sync.WaitGroup
	defer func() {
		workers.Wait()
		if err := h.Flush(); err != nil {
			log.Printf("datahub shutdown: %v", err)
		}
	}()
	run := func() {
		h.mu.Lock()
		ids := []string{}
		now := h.now()
		for _, d := range h.defs {
			c, r := h.configs[d.ID], h.records[d.ID]
			if c.Enabled && !h.busy[d.ID] && !h.pending[d.ID] && (!d.ConfigurableEndpoint || c.Endpoint != "") && (r.NextRun == nil || !r.NextRun.After(now)) {
				ids = append(ids, d.ID)
			}
		}
		h.mu.Unlock()
		for _, id := range ids {
			workers.Add(1)
			go func(id string) { defer workers.Done(); _, _ = h.RunScheduled(ctx, id) }(id)
		}
	}
	run()
	timer := time.NewTimer(h.pollWait())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			run()
			timer.Reset(h.pollWait())
		}
	}
}

// Wake at the nearest source deadline rather than quantizing every 500ms
// quote start to a 50ms tick. The cap keeps configuration changes responsive;
// 1ms prevents an overdue/busy handoff from spinning while a worker claims it.
func (h *Hub) pollWait() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	wait := 50 * time.Millisecond
	for _, d := range h.defs {
		c, r := h.configs[d.ID], h.records[d.ID]
		if !c.Enabled || h.busy[d.ID] || h.pending[d.ID] || d.ConfigurableEndpoint && c.Endpoint == "" {
			continue
		}
		if r.NextRun == nil {
			return time.Millisecond
		}
		wait = min(wait, r.NextRun.Sub(now))
	}
	return max(time.Millisecond, wait)
}
func (h *Hub) Live() Live {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := ""
	note := "等待真实市场快照；公开行情和授权行情分别留存，不混成历史K线。"
	for _, candidate := range []string{"market_bridge", "sina"} {
		if h.configs[candidate].Enabled && len(h.records[candidate].Quotes) > 0 {
			id = candidate
			break
		}
	}
	if id == "" {
		for _, candidate := range []string{"market_bridge", "sina"} {
			if len(h.records[candidate].Quotes) > 0 {
				id = candidate
				break
			}
		}
	}
	if id == "" {
		return Live{Polling: h.pollingLocked("sina", h.now()), Quotes: []market.Quote{}, StateHistory: []market.RollEvent{}, Freshness: "never", FreshnessNote: "尚无真实报价", SelectionNote: note}
	}
	d, _ := h.definition(id)
	v := h.sourceLocked(d, h.now())
	r := h.records[id]
	note = "当前显示" + d.Name + "，独立保留原始数据时刻。"
	if id == "sina" && h.configs["market_bridge"].Enabled {
		note = "授权桥接尚无可用快照，当前明确展示公开参考报价。"
	}
	if !h.configs[id].Enabled {
		note += " 此源已停用，保留最后一次数据。"
	}
	// Deep copy avoids exposing mutable snapshot slices to concurrent encoders.
	q := append([]market.Quote{}, r.Quotes...)
	history := append([]market.RollEvent{}, r.Main.History...)
	m := r.Main
	m.History = history
	m.Snapshots = nil
	trails := map[string][]PricePoint{}
	for sym, points := range r.PriceHistory {
		trails[sym] = append([]PricePoint{}, points...)
	}
	return Live{Polling: v.Polling, PriceHistory: trails, Status: v.Status, LastError: v.LastError, SourceID: id, SourceName: d.Name, Quotes: q, Main: m, StateHistory: append([]market.RollEvent{}, history...), Freshness: v.Freshness, FreshnessNote: v.FreshnessNote, LastSuccess: copyTime(r.LastSuccess), LatestDataAt: copyTime(r.LatestDataAt), SelectionNote: note}
}
