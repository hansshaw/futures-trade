package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cornwatch/internal/analysis"
	"cornwatch/internal/collector"
	"cornwatch/internal/datahub"
	"cornwatch/internal/domain"
	"cornwatch/internal/journal"
	"cornwatch/internal/marketchart"
	"cornwatch/internal/marketdaily"
	"cornwatch/internal/quant"
	"cornwatch/internal/store"
)

type Config struct {
	Token, UI, AllowedOrigin string
	FeedURLs                 []string
	WeatherEnabled           bool
	PollInterval             time.Duration
	Journal                  *journal.Service
	Hub                      *datahub.Hub
}
type Server struct {
	reportMu        sync.Mutex
	reportJournalMu sync.Mutex
	reportBusy      bool
	reportClosing   bool
	reportContext   context.Context
	reportCancel    context.CancelFunc
	reportWorkers   sync.WaitGroup
	reportInitErr   error
	DailyFetcher    DailyFetcher
	Chart           *marketchart.Service
	current         chan struct{}
	Store           *store.Store
	Analyzer        *analysis.Analyzer
	Collector       *collector.Collector
	Config          Config
	mu              sync.Mutex
	subs            map[chan string]struct{}
	collecting      map[string]bool
	backtests       chan struct{}
}

func New(s *store.Store, a *analysis.Analyzer, c Config) *Server {
	server := &Server{DailyFetcher: marketdaily.Cached(marketdaily.New().Fetch), current: make(chan struct{}, 1), Store: s, Analyzer: a, Collector: collector.New(), Config: c, subs: map[chan string]struct{}{}, collecting: map[string]bool{}, backtests: make(chan struct{}, 2)}
	server.Chart = marketchart.New(s, func(ctx context.Context, symbol string, at time.Time) (marketdaily.Report, error) {
		return server.DailyFetcher(ctx, symbol, at)
	})
	server.reportContext, server.reportCancel = context.WithCancel(context.Background())
	server.reportInitErr = server.recoverReportJobs()
	if c.Hub != nil {
		c.Hub.SetNotify(server.changed)
	}
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.Config.Hub != nil {
		s.Config.Hub.Register(mux)
	}
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"status": "ok", "version": "0.6.0", "mode": "research", "live_trading": false})
	})
	mux.HandleFunc("GET /api/report-settings", s.getReportSettings)
	mux.HandleFunc("POST /api/report-settings", s.saveReportSettings)
	mux.HandleFunc("POST /api/report-settings/test", s.testReportSettings)
	mux.HandleFunc("GET /api/daily-reports", s.dailyReportList)
	mux.HandleFunc("GET /api/daily-reports/{id}", s.dailyReportDetail)
	mux.HandleFunc("POST /api/daily-reports/generate", s.generateDailyReport)
	mux.HandleFunc("POST /api/daily-reports/{id}/journal", s.dailyReportJournal)
	mux.HandleFunc("GET /api/research/market", s.marketResearch)
	mux.HandleFunc("GET /api/market/history", s.marketHistory)
	mux.HandleFunc("GET /api/research/current", s.getCurrentAnalysis)
	mux.HandleFunc("POST /api/research/current", s.refreshCurrentAnalysis)
	mux.HandleFunc("POST /api/research/risk", s.riskResearch)
	mux.HandleFunc("POST /api/research/validate", s.validateResearch)
	if s.Config.Journal != nil {
		s.Config.Journal.Register(mux, s.changed)
	}
	mux.HandleFunc("GET /api/dashboard", s.dashboard)
	mux.HandleFunc("GET /api/bars", s.bars)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/events", s.addEvent)
	mux.HandleFunc("POST /api/events/{id}/analyze", s.analyzeEvent)
	mux.HandleFunc("GET /api/observations", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.Store.Observations()
		respond(w, items, err)
	})
	mux.HandleFunc("POST /api/observations", s.addObservation)
	mux.HandleFunc("POST /api/import/bars", s.importBars)
	mux.HandleFunc("GET /api/backtests", func(w http.ResponseWriter, r *http.Request) { items, err := s.Store.Runs(); respond(w, items, err) })
	mux.HandleFunc("POST /api/backtests", s.backtest)
	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, r *http.Request) { write(w, 200, s.sources()) })
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		vision := s.Config.Journal != nil && s.Config.Journal.VisionEnabled()
		weatherEnabled := s.Config.WeatherEnabled
		if s.Config.Hub != nil {
			for _, source := range s.Config.Hub.Sources() {
				if source.ID == "weather" {
					weatherEnabled = source.Config.Enabled
				}
			}
		}
		write(w, 200, map[string]any{"llm": map[string]any{"configured": s.Analyzer.Configured(), "base_url": s.Analyzer.Config.BaseURL, "model": s.Analyzer.Config.Model}, "journal": map[string]any{"available": s.Config.Journal != nil, "vision_enabled": vision}, "data_sources_available": s.Config.Hub != nil, "feed_count": len(s.Config.FeedURLs), "weather_enabled": weatherEnabled, "storage": "bbolt / transaction / local", "live_trading": false})
	})
	mux.HandleFunc("POST /api/collect/weather", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.collect(r.Context(), "weather")
		if err != nil {
			fail(w, 400, err)
			return
		}
		write(w, 200, map[string]int{"imported": n})
	})
	mux.HandleFunc("POST /api/collect/feeds", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.collect(r.Context(), "feeds")
		if err != nil {
			fail(w, 400, err)
			return
		}
		write(w, 200, map[string]int{"imported": n})
	})
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, errors.New("API endpoint not found")) })
	if s.Config.UI != "" {
		files := http.FileServer(http.Dir(s.Config.UI))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Path != "/" {
				path := filepath.Join(s.Config.UI, filepath.Clean(r.URL.Path))
				info, e := os.Stat(path)
				if e != nil || info.IsDir() {
					http.NotFound(w, r)
					return
				}
			}
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'")
			files.ServeHTTP(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		// Loopback-only without a token. Validate Host as well as Origin to
		// prevent another website from rebinding DNS to the local API.
		if s.Config.Token == "" {
			host, _, e := net.SplitHostPort(r.Host)
			if e != nil {
				host = r.Host
			}
			if host != "127.0.0.1" && host != "localhost" && host != "::1" {
				fail(w, 403, errors.New("invalid local Host"))
				return
			}
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			same := e == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
			if !same && origin != s.Config.AllowedOrigin && origin != "http://127.0.0.1:5173" {
				fail(w, 403, errors.New("origin not allowed"))
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" && s.Config.Token != "" {
			auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(auth), []byte(s.Config.Token)) != 1 {
				fail(w, 401, errors.New("unauthorized"))
				return
			}
		}
		if r.Method == "POST" && r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			fail(w, 415, errors.New("Content-Type must be application/json"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("response encoding: %v", err)
	}
}
func fail(w http.ResponseWriter, status int, err error) {
	write(w, status, map[string]string{"error": err.Error()})
}
func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		log.Printf("storage error: %v", err)
		fail(w, 500, errors.New("本地数据读取失败"))
		return
	}
	write(w, 200, value)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("JSON 请求无效: %w", err)
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return errors.New("请求只能包含一个 JSON 对象")
	}
	return nil
}
func validURL(s string) bool {
	if s == "" {
		return true
	}
	u, e := url.Parse(s)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && len(s) < 4000
}
func (s *Server) changed(kind string) {
	message, _ := json.Marshal(map[string]any{"kind": kind, "at": time.Now().UTC()})
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- string(message):
		default:
		}
	}
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	bars, err := s.Store.Bars("", false)
	if err != nil {
		respond(w, nil, err)
		return
	}
	mode := "imported"
	if len(bars) == 0 {
		bars, err = s.Store.Bars("", true)
		mode = "demo"
	}
	if err != nil {
		respond(w, nil, err)
		return
	}
	if len(bars) == 0 {
		mode = "empty"
	}
	events, err := s.Store.Events()
	if err != nil {
		respond(w, nil, err)
		return
	}
	observations, err := s.Store.Observations()
	if err != nil {
		respond(w, nil, err)
		return
	}
	quoteMap := map[string][]domain.Bar{}
	cornBars := []domain.Bar{}
	for _, b := range bars {
		quoteMap[b.Symbol] = append(quoteMap[b.Symbol], b)
		if isCorn.MatchString(b.Symbol) {
			cornBars = append(cornBars, b)
		}
	}
	quotes := []domain.Quote{}
	for symbol, items := range quoteMap {
		last := items[len(items)-1]
		prev := last.Close
		if len(items) > 1 {
			prev = items[len(items)-2].Close
		}
		name := "玉米"
		if strings.HasPrefix(symbol, "cs") {
			name = "玉米淀粉"
		} else if strings.HasPrefix(symbol, "lh") {
			name = "生猪"
		} else if strings.HasPrefix(symbol, "m") {
			name = "豆粕"
		}
		source := "导入日K · 非实时"
		if last.IsDemo {
			source = "合成演示日K"
		}
		quotes = append(quotes, domain.Quote{Symbol: symbol, Name: name, Last: last.Close, PreviousClose: prev, ChangePct: (last.Close - prev) / prev * 100, Volume: last.Volume, OpenInterest: last.OpenInterest, ObservedAt: last.Timestamp, Source: source, IsDemo: last.IsDemo})
	}
	sort.Slice(quotes, func(i, j int) bool { return quotes[i].Symbol < quotes[j].Symbol })
	decision := quant.ResolveMain(cornBars, time.Now().UTC())
	var live *datahub.Live
	if s.Config.Hub != nil {
		current := s.Config.Hub.Live()
		live = &current
		if len(current.Quotes) > 0 {
			quotes = liveCards(current)
			decision = current.Main.Decision
			mode = "public_quotes"
			if current.SourceID == "market_bridge" {
				mode = "authorized_quotes"
			}
		}
	}
	eventCount := len(events)
	if len(events) > 100 {
		events = events[:100]
	}
	if len(observations) > 100 {
		observations = observations[:100]
	}
	write(w, 200, map[string]any{"as_of": time.Now().UTC(), "mode": mode, "quotes": quotes, "events": events, "observations": observations, "main_contract": decision, "market_live": live, "sources": s.sources(), "bar_count": len(bars), "event_count": eventCount, "llm_configured": s.Analyzer.Configured()})
}

var isCorn = regexp.MustCompile(`^c[0-9]{4}$`)
var symbolPattern = regexp.MustCompile(`^[a-z]{1,3}[0-9]{4}$`)

func (s *Server) bars(w http.ResponseWriter, r *http.Request) {
	symbol := strings.ToLower(r.URL.Query().Get("symbol"))
	if !symbolPattern.MatchString(symbol) {
		fail(w, 400, errors.New("symbol 应为实际合约，如 c2701"))
		return
	}
	demo := r.URL.Query().Get("demo") == "true"
	bars, err := s.Store.Bars(symbol, demo)
	respond(w, bars, err)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	events, err := s.Store.Events()
	if err != nil {
		respond(w, nil, err)
		return
	}
	q := strings.ToLower(r.URL.Query().Get("q"))
	category := r.URL.Query().Get("category")
	out := []domain.Event{}
	for _, e := range events {
		if category != "" && category != e.Category {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Title+e.Body+e.Region), q) {
			continue
		}
		out = append(out, e)
		if len(out) >= 500 {
			break
		}
	}
	write(w, 200, out)
}
func (s *Server) addEvent(w http.ResponseWriter, r *http.Request) {
	var e domain.Event
	if err := decode(w, r, &e); err != nil {
		fail(w, 400, err)
		return
	}
	e.Title = strings.TrimSpace(e.Title)
	e.Source = strings.TrimSpace(e.Source)
	if e.Title == "" || e.Body == "" || e.Source == "" || e.PublishedAt.IsZero() || e.PublishedAt.After(time.Now().Add(5*time.Minute)) || len(e.Title) > 1000 || len(e.Body) > 64000 || !validURL(e.SourceURL) {
		fail(w, 400, errors.New("需填写标题、正文、来源、有效发布时间和合法链接（标题≤1000字节、正文≤64000字节）"))
		return
	}
	if e.Severity != "high" && e.Severity != "medium" && e.Severity != "low" {
		e.Severity = "medium"
	}
	if e.Category == "" {
		e.Category = "news"
	}
	if e.Products == nil {
		e.Products = []string{"c"}
	}
	e.ID = store.ID(e.Source, e.SourceURL, e.Title, e.Body, e.PublishedAt.UTC().Format(time.RFC3339Nano))
	e.IngestedAt = time.Now().UTC()
	e.AvailableAt = e.IngestedAt
	if e.PublishedAt.After(e.AvailableAt) {
		e.AvailableAt = e.PublishedAt
	}
	e.IsDemo = false
	e.Analysis = nil
	e, created, err := s.Store.AddEvent(e)
	if err != nil {
		respond(w, nil, err)
		return
	}
	s.changed("events")
	status := 200
	if created {
		status = 201
	}
	write(w, status, e)
}
func (s *Server) analyzeEvent(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UseLLM bool `json:"use_llm"`
	}
	if err := decode(w, r, &request); err != nil {
		fail(w, 400, err)
		return
	}
	e, err := s.Store.Event(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	a := analysis.Rule(e)
	if request.UseLLM {
		a, err = s.Analyzer.Analyze(r.Context(), e)
		if err != nil {
			fail(w, 400, err)
			return
		}
	}
	e, err = s.Store.AnalyzeEvent(e.ID, a)
	if err != nil {
		respond(w, nil, err)
		return
	}
	s.changed("analysis")
	write(w, 200, e)
}
func (s *Server) addObservation(w http.ResponseWriter, r *http.Request) {
	var o domain.Observation
	if err := decode(w, r, &o); err != nil {
		fail(w, 400, err)
		return
	}
	validKind := o.Kind == "purchase_price" || o.Kind == "truck_arrivals" || o.Kind == "weather" || o.Kind == "related_price" || o.Kind == "import_auction_rate"
	if !validKind || o.Region == "" || o.Entity == "" || o.Unit == "" || o.Source == "" || o.ObservedAt.IsZero() || o.ObservedAt.After(time.Now().Add(5*time.Minute)) || math.IsNaN(o.Value) || math.IsInf(o.Value, 0) || !validURL(o.SourceURL) || len(o.Note) > 12000 {
		fail(w, 400, errors.New("观测记录需要类型、地区、主体、数值、单位、来源和有效观察时间"))
		return
	}
	if o.Kind != "weather" && o.Value < 0 {
		fail(w, 400, errors.New("价格与车辆数不能为负数"))
		return
	}
	if o.Kind == "import_auction_rate" && (o.Unit != "%" || o.Value > 100 || o.Value < 0) {
		fail(w, 400, errors.New("拍卖成交率需为0至100，单位为%"))
		return
	}
	if o.Kind == "truck_arrivals" && math.Trunc(o.Value) != o.Value {
		fail(w, 400, errors.New("到车量必须为整数"))
		return
	}
	o.AvailableAt = time.Now().UTC()
	o.IsDemo = false
	o.ID = store.ID(o.Kind, o.Entity, o.Region, o.ObservedAt.UTC().Format(time.RFC3339Nano), fmt.Sprint(o.Value), o.Unit, o.Source, o.Note)
	added, err := s.Store.AddObservations([]domain.Observation{o})
	if err != nil {
		respond(w, nil, err)
		return
	}
	status := 201
	if added == 0 {
		o, err = s.Store.Observation(o.ID)
		if err != nil {
			respond(w, nil, err)
			return
		}
		status = 200
	}
	s.changed("observations")
	write(w, status, o)
}
func (s *Server) importBars(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CSV    string `json:"csv"`
		Source string `json:"source"`
	}
	if err := decode(w, r, &request); err != nil {
		fail(w, 400, err)
		return
	}
	if strings.TrimSpace(request.Source) == "" {
		fail(w, 400, errors.New("请注明数据来源和授权口径"))
		return
	}
	bars, err := ParseCSV(request.CSV)
	if err != nil {
		fail(w, 400, err)
		return
	}
	// Check new daily timestamps against earlier imports before committing.
	existing, err := s.Store.Bars("", false)
	if err != nil {
		respond(w, nil, err)
		return
	}
	days := map[string]time.Time{}
	for _, b := range existing {
		days[b.Symbol+"|"+b.TradeDate] = b.Timestamp
	}
	for _, b := range bars {
		if t, ok := days[b.Symbol+"|"+b.TradeDate]; ok && !t.Equal(b.Timestamp) {
			fail(w, 400, fmt.Errorf("%s %s 已有不同结束时间的日K", b.Symbol, b.TradeDate))
			return
		}
	}
	added, duplicates, err := s.Store.ImportBars(bars, request.Source)
	if err != nil {
		fail(w, 400, err)
		return
	}
	_ = s.Store.SetMeta("source_csv", domain.SourceStatus{ID: "csv", Name: "授权历史数据 / CSV", Kind: "market", Status: "ok", LastSuccess: timePtr(time.Now().UTC()), Message: fmt.Sprintf("最近导入%d条，重复%d条；来源：%s", added, duplicates, request.Source)})
	s.changed("bars")
	write(w, 200, map[string]int{"imported": added, "duplicates": duplicates})
}
func (s *Server) backtest(w http.ResponseWriter, r *http.Request) {
	select {
	case s.backtests <- struct{}{}:
		defer func() { <-s.backtests }()
	default:
		fail(w, 429, errors.New("回测任务繁忙，请稍后重试"))
		return
	}
	var config quant.Config
	if err := decode(w, r, &config); err != nil {
		fail(w, 400, err)
		return
	}
	bars, err := s.Store.Bars(config.Symbol, config.Demo)
	if err != nil {
		respond(w, nil, err)
		return
	}
	result, err := quant.Backtest(config, bars)
	if err != nil {
		fail(w, 400, err)
		return
	}
	// Audit input hash beside the engine result; input bars are immutable.
	if err = s.Store.SetMeta("run_input_"+result.ID, map[string]any{"input_sha256": result.InputSHA256, "engine": result.EngineVersion, "bar_count": result.BarCount, "at": time.Now().UTC()}); err != nil {
		respond(w, nil, err)
		return
	}
	if err = s.Store.PutRun(time.Now().UTC().Format(time.RFC3339Nano)+"|"+result.ID, result); err != nil {
		respond(w, nil, err)
		return
	}
	s.changed("backtests")
	write(w, 200, result)
}
func timePtr(t time.Time) *time.Time { return &t }

func (s *Server) sources() []domain.SourceStatus {
	sources := []domain.SourceStatus{
		{ID: "market", Name: "实时行情 / CTP·授权服务", Kind: "market", Status: "not_configured", Message: "未接入实时行情；CSV日K不是实时行情。CTP / TqSdk适配计划见方案"},
		{ID: "csv", Name: "授权历史数据 / CSV", Kind: "market", Status: "not_configured", Message: "支持实际玉米合约日K；导入后校验、去重、事务入库"},
		{ID: "industry", Name: "收购价 / 到车量", Kind: "fundamental", Status: "not_configured", Message: "使用手动观测记录或授权Feed；水分、企业样本与计量口径需注明"},
		{ID: "weather", Name: "Open-Meteo 天气预报", Kind: "weather", Status: "configured", Message: "可手动采集5个区域、3日预报；自动采集默认关闭；免费端点适用于非商业评估，商业部署需调整"},
		{ID: "feeds", Name: "授权 RSS / Atom", Kind: "events", Status: "not_configured", Message: "配置 CORNWATCH_FEED_URLS 后启用；按发布时间保留原文"},
		{ID: "llm", Name: "可配置大模型", Kind: "analysis", Status: "not_configured", Message: "配置 LLM_BASE_URL、LLM_MODEL、LLM_API_KEY；未配置时可使用标记明确的规则摘要"},
	}
	if len(s.Config.FeedURLs) > 0 {
		sources[4].Status = "configured"
		sources[4].Message = fmt.Sprintf("已配置%d个Feed，定时采集间隔%s", len(s.Config.FeedURLs), s.Config.PollInterval)
	}
	if s.Analyzer.Configured() {
		sources[5].Status = "configured"
		sources[5].Message = "服务端配置完成，尚需调用验证"
	}
	for i := range sources {
		var status domain.SourceStatus
		if s.Store.GetMeta("source_"+sources[i].ID, &status) == nil {
			sources[i] = status
		}
	}
	if s.Config.Hub != nil {
		base := []domain.SourceStatus{sources[1], sources[4], sources[5]}
		for _, item := range s.Config.Hub.Sources() {
			base = append(base, domain.SourceStatus{ID: item.ID, Name: item.Name, Kind: item.Kind, Status: item.Status, LastSuccess: item.LastSuccess, Message: item.FreshnessNote + " " + item.LastError})
		}
		return base
	}
	return sources
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, errors.New("streaming unsupported"))
		return
	}
	ch := make(chan string, 8)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.subs, ch); s.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			if _, err := fmt.Fprintf(w, "event: update\ndata: %s\n\n", msg); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) collect(ctx context.Context, kind string) (int, error) {
	if kind == "weather" && s.Config.Hub != nil {
		out, err := s.Config.Hub.Run(ctx, "weather", false)
		return out.Added, err
	}
	s.mu.Lock()
	if s.collecting[kind] {
		s.mu.Unlock()
		return 0, errors.New("该采集任务正在运行")
	}
	s.collecting[kind] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.collecting, kind); s.mu.Unlock() }()
	count := 0
	var err error
	if kind == "weather" {
		var items []domain.Observation
		items, err = s.Collector.Weather(ctx)
		if err == nil {
			count, err = s.Store.AddObservations(items)
		}
		if err == nil {
			for _, o := range items {
				if o.Unit == "mm" && o.Value >= 25 {
					event := domain.Event{ID: store.ID("weather-alert", o.ID), Title: o.Region + " 降水预报达到关注阈值", Body: fmt.Sprintf("%s 预报 %.1f mm。阈值25mm为研究规则，不是官方气象预警；需结合收获期和区域覆盖核验。", o.Entity, o.Value), Category: "weather", Region: o.Region, Products: []string{"c"}, Source: o.Source, SourceURL: o.SourceURL, PublishedAt: o.AvailableAt, IngestedAt: o.AvailableAt, AvailableAt: o.AvailableAt, Severity: "high"}
					if _, _, err = s.Store.AddEvent(event); err != nil {
						break
					}
				}
			}
		}
	} else {
		if len(s.Config.FeedURLs) == 0 {
			err = errors.New("尚未配置 CORNWATCH_FEED_URLS")
		}
		for _, address := range s.Config.FeedURLs {
			var items []domain.Event
			items, err = s.Collector.Feed(ctx, address)
			if err != nil {
				break
			}
			for _, e := range items {
				var added bool
				_, added, err = s.Store.AddEvent(e)
				if err != nil {
					break
				}
				if added {
					count++
				}
			}
			if err != nil {
				break
			}
		}
	}
	status := domain.SourceStatus{ID: kind, Name: map[string]string{"weather": "Open-Meteo 天气预报", "feeds": "授权 RSS / Atom"}[kind], Kind: kind, Status: "ok", LastSuccess: timePtr(time.Now().UTC()), Message: fmt.Sprintf("最近采集新增%d条", count)}
	if err != nil {
		status.Status = "error"
		status.Message = err.Error()
		status.LastSuccess = nil
		var old domain.SourceStatus
		if s.Store.GetMeta("source_"+kind, &old) == nil {
			status.LastSuccess = old.LastSuccess
		}
	}
	if e := s.Store.SetMeta("source_"+kind, status); e != nil && err == nil {
		err = e
	}
	s.changed(kind)
	return count, err
}
func (s *Server) Poll(ctx context.Context) {
	// Wait for schedulers to cancel their workers and flush pending quote
	// checkpoints before main closes the shared databases.
	var schedulers sync.WaitGroup
	schedulers.Add(1)
	go func() { defer schedulers.Done(); s.pollReports(ctx) }()
	if s.Config.Hub != nil {
		schedulers.Add(1)
		go func() { defer schedulers.Done(); s.Config.Hub.Poll(ctx) }()
	}
	defer schedulers.Wait()
	interval := s.Config.PollInterval
	if interval < time.Minute {
		interval = 5 * time.Minute
	}
	run := func() {
		if len(s.Config.FeedURLs) > 0 {
			if _, err := s.collect(ctx, "feeds"); err != nil {
				log.Printf("feed collector: %v", err)
			}
		}
		if s.Config.WeatherEnabled && s.Config.Hub == nil {
			var last domain.SourceStatus
			_ = s.Store.GetMeta("source_weather", &last)
			if last.LastSuccess == nil || time.Since(*last.LastSuccess) >= time.Hour {
				if _, err := s.collect(ctx, "weather"); err != nil {
					log.Printf("weather collector: %v", err)
				}
			}
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
