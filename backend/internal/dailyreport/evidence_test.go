package dailyreport

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"cornwatch/internal/currentanalysis"
	"cornwatch/internal/domain"
	"cornwatch/internal/market"
	"cornwatch/internal/research"
)

func fixture() (time.Time, *currentanalysis.Report) {
	now := time.Date(2026, 10, 29, 8, 0, 0, 0, china)
	closed := time.Date(2026, 10, 28, 15, 0, 0, 0, china)
	q := market.Quote{Symbol: "c2701", Product: "c", Exchange: "DCE", Name: "玉米", TradeDate: "2026-10-28", Source: "公开行情测试源", SourceURL: "https://example.org/quote", ObservedAt: now.Add(-10 * time.Minute), ReceivedAt: now.Add(-9 * time.Minute), Last: 2220, Volume: 12345, OpenInterest: 23456}
	return now, &currentanalysis.Report{GeneratedAt: now.Add(-time.Minute), InputSHA256: strings.Repeat("a", 64), Symbol: q.Symbol, Selection: "测试研究主力", Snapshot: q, DailySource: "完成日线测试源", DailyURL: "https://example.org/daily", DailyAvailableAt: now.Add(-time.Hour), Technical: research.MarketReport{Symbol: q.Symbol, AsOf: closed, BarCount: 1, Points: []research.Point{{Timestamp: closed, TradeDate: "2026-10-28", Close: 2210, Volume: 100}}, Latest: research.Latest{Close: 2210, Trend: "区间观察", PriceOILabel: "持仓数据不足"}}, Warnings: []string{"本测试无真实交易含义"}}
}
func observation(now time.Time, id, kind string) domain.Observation {
	return domain.Observation{ID: id, Kind: kind, Region: "山东", Entity: "测试实体", Value: 2230, Unit: "元/吨", Source: "公开产业测试源", SourceURL: "https://example.org/industry", ObservedAt: now.Add(-time.Hour), AvailableAt: now.Add(-time.Minute), Note: "原始口径：水分14%，税费未注明"}
}
func mustBuild(t *testing.T, now time.Time, c *currentanalysis.Report, obs []domain.Observation, events []domain.Event) Bundle {
	t.Helper()
	b, err := Build("pre", now.In(china).Format("2006-01-02"), c, obs, events, now)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func evidenceByOriginal(b Bundle, id string) (Evidence, bool) {
	for _, e := range b.Evidence {
		if contains(e.OriginalIDs, id) {
			return e, true
		}
	}
	return Evidence{}, false
}

func TestPointInTimeForecastsAndFutureEffectiveAnnouncements(t *testing.T) {
	now, c := fixture()
	valid := observation(now, "known-price", "purchase_price")
	futureAvailable := valid
	futureAvailable.ID = "future-available"
	futureAvailable.AvailableAt = now.Add(time.Second)
	futureAvailable.Entity = "later"
	futurePrice := valid
	futurePrice.ID = "not-effective"
	futurePrice.ObservedAt = now.Add(time.Hour)
	futurePrice.Entity = "later effective"
	demo := valid
	demo.ID = "demo"
	demo.IsDemo = true
	unknownTime := valid
	unknownTime.ID = "unknown"
	unknownTime.AvailableAt = time.Time{}
	nan := valid
	nan.ID = "nan"
	nan.Value = math.NaN()
	weather := observation(now, "weather-old", "weather")
	weather.Entity = "日降水预报 · 2026-10-30"
	weather.ObservedAt = day(now).AddDate(0, 0, 1)
	weather.Unit = "mm"
	weather.Value = 10
	weather.Source = "Open-Meteo"
	weather.AvailableAt = now.Add(-2 * time.Hour)
	newWeather := weather
	newWeather.ID = "weather-latest"
	newWeather.Value = 25
	newWeather.AvailableAt = now.Add(-time.Hour)
	tooNew := newWeather
	tooNew.ID = "weather-unknown"
	tooNew.Value = 99
	tooNew.AvailableAt = now.Add(time.Minute)
	realized := weather
	realized.ID = "future-realized"
	realized.Entity = "累计实测降雨"
	realized.Note = "实测"
	realized.Source = "实际观测源"
	announcement := domain.Event{ID: "future-effect-announcement", Title: "企业明日收购通知", Body: "2026年10月30日07时起生效，水分14%报价2080元/吨。", Category: "purchase_price", Source: "企业公开通知", SourceURL: "https://example.org/notice", PublishedAt: now.Add(-time.Hour), AvailableAt: now.Add(-30 * time.Minute)}
	unpublished := announcement
	unpublished.ID = "unpublished"
	unpublished.PublishedAt = now.Add(time.Hour)
	notCollected := announcement
	notCollected.ID = "not-collected"
	notCollected.AvailableAt = now.Add(time.Hour)
	b := mustBuild(t, now, c, []domain.Observation{valid, futureAvailable, futurePrice, demo, unknownTime, nan, weather, newWeather, tooNew, realized}, []domain.Event{announcement, unpublished, notCollected})
	for _, id := range []string{"known-price", "weather-latest", "future-effect-announcement"} {
		if _, ok := evidenceByOriginal(b, id); !ok {
			t.Fatalf("missing %s", id)
		}
	}
	for _, id := range []string{"future-available", "not-effective", "demo", "unknown", "nan", "weather-old", "weather-unknown", "future-realized", "unpublished", "not-collected"} {
		if _, ok := evidenceByOriginal(b, id); ok {
			t.Fatalf("unavailable input leaked: %s", id)
		}
	}
	w, _ := evidenceByOriginal(b, "weather-latest")
	if !w.IsForecast || !w.ObservedAt.After(b.AsOf) || !strings.Contains(w.Text, "25") || !strings.Contains(w.Note, "不是已发生事实") {
		t.Fatalf("forecast misrepresented: %+v", w)
	}
	a, _ := evidenceByOriginal(b, announcement.ID)
	if a.IsForecast || !strings.Contains(a.Note, "不等于已生效") || a.ObservedAt != announcement.PublishedAt {
		t.Fatalf("announcement misrepresented: %+v", a)
	}
}

func TestForecastBudgetRetainsFiveRegionsAndThreeTargetDays(t *testing.T) {
	now, c := fixture()
	obs := []domain.Observation{}
	for region := 0; region < 5; region++ {
		for d := 0; d < 3; d++ {
			for metric, name := range []string{"日降水预报", "最高温预报", "最低温预报"} {
				o := observation(now, fmt.Sprintf("w%d-%d-%d", region, d, metric), "weather")
				o.Region = fmt.Sprintf("产区%d", region)
				o.Entity = name + " · " + day(now).AddDate(0, 0, d).Format("2006-01-02")
				o.ObservedAt = day(now).AddDate(0, 0, d)
				o.Unit = "mm"
				if metric > 0 {
					o.Unit = "°C"
				}
				o.Value = float64(metric + d)
				obs = append(obs, o)
			}
		}
	}
	b := mustBuild(t, now, c, obs, nil)
	count := 0
	original := 0
	for _, e := range b.Evidence {
		if e.Category == "weather_forecast" {
			count++
			original += len(e.OriginalIDs)
			if len(e.OriginalIDs) != 3 {
				t.Fatalf("incomplete weather metrics: %+v", e)
			}
		}
	}
	if count != 15 || original != 45 {
		t.Fatalf("weather coverage %d groups, %d metrics", count, original)
	}
}

func TestBalancedBudgetKnownUnitsPriorArrivalsAndHash(t *testing.T) {
	now, c := fixture()
	obs := []domain.Observation{}
	for i := 0; i < 40; i++ {
		o := observation(now, fmt.Sprintf("raw-%d", i), "purchase_price")
		o.Entity = fmt.Sprintf("企业%d", i)
		o.Source = "未标单位表格"
		o.Unit = "未注明"
		obs = append(obs, o)
	}
	known := observation(now, "known-unit", "purchase_price")
	known.Source = "官方监测"
	obs = append(obs, known)
	arrival := observation(now, "arrival-now", "truck_arrivals")
	arrival.Unit = "辆"
	arrival.Value = 344
	prior := arrival
	prior.ID = "arrival-prior"
	prior.Value = 251
	prior.ObservedAt = arrival.ObservedAt.Add(-24 * time.Hour)
	prior.AvailableAt = arrival.AvailableAt.Add(-24 * time.Hour)
	imported := observation(now, "import", "import_auction")
	imported.Unit = "%"
	related := observation(now, "related", "related_price")
	related.Entity = "生猪"
	obs = append(obs, arrival, prior, imported, related)
	b := mustBuild(t, now, c, obs, nil)
	for _, id := range []string{"known-unit", "arrival-now", "arrival-prior", "import", "related"} {
		if _, ok := evidenceByOriginal(b, id); !ok {
			t.Fatalf("category/source crowded out %s", id)
		}
	}
	unknownFound := false
	for _, e := range b.Evidence {
		if e.Unit == "未注明" {
			unknownFound = true
			if !strings.Contains(e.Note, "禁止直接计算基差") {
				t.Fatal("unknown units lack guard")
			}
		}
	}
	if !unknownFound {
		t.Fatal("unknown source hidden")
	}
	for i, j := 0, len(obs)-1; i < j; i, j = i+1, j-1 {
		obs[i], obs[j] = obs[j], obs[i]
	}
	if other := mustBuild(t, now, c, obs, nil); other.InputSHA256 != b.InputSHA256 {
		t.Fatal("hash depends on input order")
	}
	for i := range obs {
		if obs[i].ID == prior.ID {
			obs[i].Value++
		}
	}
	if other := mustBuild(t, now, c, obs, nil); other.InputSHA256 == b.InputSHA256 {
		t.Fatal("prior arrival omitted from input hash")
	}
	if len(b.Evidence) > MaxEvidence {
		t.Fatal("too many evidence items")
	}
}

func TestCurrentValidationCompletedBarsAndDeepCopy(t *testing.T) {
	now, base := fixture()
	for name, mutate := range map[string]func(*currentanalysis.Report){
		"demo":                    func(c *currentanalysis.Report) { c.Technical.IsDemo = true },
		"future daily collection": func(c *currentanalysis.Report) { c.DailyAvailableAt = now.Add(time.Second) },
		"future quote reception":  func(c *currentanalysis.Report) { c.Snapshot.ReceivedAt = now.Add(time.Second) },
		"future quote":            func(c *currentanalysis.Report) { c.Snapshot.ObservedAt = now.Add(time.Second) },
		"incomplete daily": func(c *currentanalysis.Report) {
			c.Technical.Points[0].TradeDate = now.In(china).Format("2006-01-02")
			c.Technical.Points[0].Timestamp = day(now)
		},
		"future industry": func(c *currentanalysis.Report) {
			o := observation(now, "future", "purchase_price")
			o.AvailableAt = now.Add(time.Hour)
			c.Evidence = []domain.Observation{o}
		},
		"wrong contract": func(c *currentanalysis.Report) { c.Technical.Symbol = "c2703" },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var c currentanalysis.Report
			json.Unmarshal(raw, &c)
			mutate(&c)
			if _, err := Build("pre", "2026-10-29", &c, nil, nil, now); err == nil {
				t.Fatal("invalid current accepted")
			}
		})
	}
	b := mustBuild(t, now, base, nil, nil)
	base.Technical.Points[0].Close = 9000
	base.Warnings[0] = "changed"
	if b.Current.Technical.Points[0].Close == 9000 || b.Current.Warnings[0] == "changed" {
		t.Fatal("bundle aliases current input")
	}
	if err := validateBundle(b); err != nil {
		t.Fatal(err)
	}
	b.Current.DailyURL = "https://example.org/changed"
	if err := validateBundle(b); err == nil {
		t.Fatal("changed full current metadata escaped hash validation")
	}
	if _, err := Build("unknown", "2026-10-29", base, nil, nil, now); err == nil {
		t.Fatal("invalid kind accepted")
	}
	if _, err := Build("pre", "2026-02-30", base, nil, nil, now); err == nil {
		t.Fatal("invalid date accepted")
	}
}

func TestQuotesDoNotReplaceFrozenSelectedQuoteAndFutureExcluded(t *testing.T) {
	now, c := fixture()
	changed := c.Snapshot
	changed.Last = 9000
	changed.ObservedAt = now.Add(-time.Minute)
	changed.ReceivedAt = now
	related := c.Snapshot
	related.Symbol = "lh2701"
	related.Product = "lh"
	related.Last = 14000
	future := related
	future.Symbol = "lh2703"
	future.ReceivedAt = now.Add(time.Second)
	b, err := BuildWithQuotes("pre", "2026-10-29", c, nil, nil, []market.Quote{changed, related, future}, now)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, e := range b.Evidence {
		if e.Category == "market" && strings.Contains(e.Text, "9000") {
			t.Fatal("live quote replaced frozen selection")
		}
		if strings.Contains(e.Title, "lh2703") {
			t.Fatal("future quote included")
		}
		if strings.Contains(e.Title, "lh2701") {
			seen = true
		}
	}
	if !seen {
		t.Fatal("related market quote missing")
	}
}

func TestDailyCloseWaitsUntil1515(t *testing.T) {
	_, c := fixture()
	closeTime := time.Date(2026, 10, 29, 15, 0, 0, 0, china)
	c.Technical.AsOf = closeTime
	c.Technical.Points[0].Timestamp = closeTime
	c.Technical.Points[0].TradeDate = "2026-10-29"
	for _, minute := range []int{5, 14, 15} {
		now := closeTime.Add(time.Duration(minute) * time.Minute)
		c.GeneratedAt = now
		c.DailyAvailableAt = now
		_, err := Build("post", "2026-10-29", c, nil, nil, now)
		if minute < 15 && err == nil {
			t.Fatalf("15:%02d incomplete public daily accepted", minute)
		}
		if minute == 15 && err != nil {
			t.Fatalf("mature daily rejected: %v", err)
		}
	}
}

func TestLongNotesCannotCrowdOutWholeCategories(t *testing.T) {
	now, c := fixture()
	obs := []domain.Observation{}
	for _, kind := range []string{"purchase_price", "truck_arrivals", "import_auction", "related_price"} {
		for i := 0; i < 30; i++ {
			o := observation(now, fmt.Sprintf("%s-%d", kind, i), kind)
			o.Entity = fmt.Sprintf("实体%d", i)
			o.Note = strings.Repeat("原始口径需要核验。", 300)
			obs = append(obs, o)
		}
	}
	events := []domain.Event{{ID: "pending", Title: "已公布通知", Body: "明日起生效。" + strings.Repeat("原文说明。", 500), Source: "公告测试源", PublishedAt: now.Add(-time.Hour), AvailableAt: now.Add(-time.Minute)}}
	b := mustBuild(t, now, c, obs, events)
	for _, category := range []string{"purchase_price", "truck_arrivals", "import", "related_price", "event"} {
		found := false
		for _, e := range b.Evidence {
			if e.Category == category {
				found = true
			}
		}
		if !found {
			t.Fatalf("long notes crowded out %s", category)
		}
	}
}
