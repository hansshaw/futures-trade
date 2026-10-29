package industrypublic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cornwatch/internal/store"
)

var observedNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

const priceURL = "https://www.boyar.cn/article/1269531.html"
const arrivalURL = "https://www.boyar.cn/article/1269532.html"
const futureURL = "https://www.boyar.cn/article/1269541.html"
const effectiveURL = "https://www.boyar.cn/article/1269319.html"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".html"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestRealPriceTablePreservesUnknownUnits(t *testing.T) {
	obs, events, err := ParseReport(fixture(t, "prices"), priceURL, observedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 22 || len(events) != 1 {
		t.Fatalf("count %d/%d", len(obs), len(events))
	}
	if obs[0].Value != 1980 || obs[0].Unit != MissingUnit || obs[13].Value != 2246 || obs[14].Value != 2252 {
		t.Fatalf("wrong price extraction: %+v", obs)
	}
	for _, o := range obs {
		if !o.AvailableAt.Equal(observedNow) || o.IsDemo || o.Region != "原页未标地区" || o.SourceURL != priceURL || !strings.Contains(o.Note, "禁止换算、基差") || !strings.Contains(o.Note, "未披露水分") {
			t.Fatalf("scope or timing lost: %+v", o)
		}
	}
	if obs[0].ObservedAt.In(china).Format("2006-01-02 15:04") != "2026-10-28 00:00" || events[0].PublishedAt.In(china).Format("2006-01-02 15:04") != "2026-10-28 13:07" {
		t.Fatal("source date confused with publish/collection")
	}
	again, againEvents, err := ParseReport(fixture(t, "prices"), priceURL, observedNow.Add(time.Hour))
	if err != nil || again[0].ID != obs[0].ID || againEvents[0].ID != events[0].ID {
		t.Fatal("identity changes with collection time")
	}
}
func TestRealArrivalsKeepOriginalScope(t *testing.T) {
	obs, events, err := ParseReport(fixture(t, "arrivals"), arrivalURL, observedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 1 || len(events) != 1 || obs[0].Value != 344 || obs[0].Unit != "辆" || obs[0].Kind != "truck_arrivals" {
		t.Fatalf("bad arrivals %+v", obs)
	}
	if !strings.Contains(obs[0].Note, "未区分晨间新增到厂与门前剩余待卸") || !strings.Contains(obs[0].Note, "不是全省全天累计") || !strings.Contains(obs[0].Note, "原始调查机构未注明") {
		t.Fatal("scope ambiguity silently lost")
	}
	if events[0].PublishedAt.In(china).Format("15:04") != "13:22" {
		t.Fatal("publication time lost")
	}
}
func TestEffectiveAndFutureNotice(t *testing.T) {
	obs, events, err := ParseReport(fixture(t, "notice-effective"), effectiveURL, observedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 4 || len(events) != 1 {
		t.Fatalf("notice counts %d/%d", len(obs), len(events))
	}
	for i, want := range []float64{2060, 2030, 2070, 2040} {
		if obs[i].Value != want || obs[i].Unit != "元/吨" || obs[i].ObservedAt.In(china).Format("2006-01-02 15:04") != "2026-10-27 07:00" || !strings.Contains(obs[i].Entity, "水分") || !strings.Contains(obs[i].Note, "筛混不返杂") {
			t.Fatalf("notice tier %d: %+v", i, obs[i])
		}
	}
	future, announcement, err := ParseReport(fixture(t, "notice-future"), futureURL, observedNow)
	if err != nil || len(future) != 0 || len(announcement) != 1 {
		t.Fatalf("future used as current: %d %d %v", len(future), len(announcement), err)
	}
	next, after, err := ParseReport(fixture(t, "notice-future"), futureURL, observedNow.Add(24*time.Hour))
	if err != nil || len(next) != 4 || next[0].Value != 2080 || next[3].Value != 2060 {
		t.Fatalf("became effective: %+v %v", next, err)
	}
	if announcement[0].ID != after[0].ID || announcement[0].Body != after[0].Body {
		t.Fatal("announcement changed after effective date")
	}
}
func TestListExcludesSidebarMembersAndForeignLinks(t *testing.T) {
	b := string(fixture(t, "list"))
	links, err := ParseList([]byte(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 5 || links[0].URL != futureURL || links[1].URL != arrivalURL {
		t.Fatalf("unexpected discovery: %+v", links)
	}
	added := `<a href="/article/9999991.html"><span style="color:red">2026年10月30日国内部分玉米深加工企业报价</span></a><a href="https://evil.example/article/9999992.html">2026年10月30日国内部分玉米深加工企业报价</a><a href="/article/9999993.html?login=1">2026年10月30日国内部分玉米深加工企业报价</a><a href="/article/1269531.html">2026年10月28日国内部分玉米深加工企业报价</a>`
	b = strings.Replace(b, `<ul>`, `<ul>`+added, 1)
	b = strings.Replace(b, `</body>`, `<div class="hot"><a href="/article/9999994.html">2026年10月31日国内部分玉米深加工企业报价</a></div></body>`, 1)
	filtered, err := ParseList([]byte(b))
	if err != nil || len(filtered) != len(links) {
		t.Fatalf("untrusted/duplicate links entered: %+v %v", filtered, err)
	}
}
func TestRejectsAmbiguousReports(t *testing.T) {
	tests := []struct{ name, fixture, url, old, new string }{
		{"price header", "prices", priceURL, "企业名称", "产区名称"},
		{"price date", "prices", priceURL, "2026/10/28", "2026/10/29"},
		{"price unit changed header", "prices", priceURL, "涨跌", "涨跌百分比"},
		{"price range", "prices", priceURL, ">1980<", ">1980-1990<"},
		{"price arithmetic", "prices", priceURL, ">2246<", ">2247<"},
		{"duplicate enterprise", "prices", priceURL, "龙凤淀粉", "京粮龙江"},
		{"wrong publication", "prices", priceURL, "发布时间：", "时间："},
		{"unknown original source", "prices", priceURL, "来源：博亚和讯", "来源：不明来源"},
		{"changed body", "prices", priceURL, "fs18 lh36 mt50", "changed-body"},
		{"arrivals date", "arrivals", arrivalURL, "10月28日，", "10月27日，"},
		{"arrivals range", "arrivals", arrivalURL, "344车", "300-344车"},
		{"arrivals different scope", "arrivals", arrivalURL, "晨间到车辆", "全天累计到车辆"},
		{"arrival tonnes", "arrivals", arrivalURL, "344车", "344吨"},
		{"notice moisture", "notice-effective", effectiveURL, "38%", "40%"},
		{"notice tax", "notice-effective", effectiveURL, "9%税率", "13%税率"},
		{"notice unit", "notice-effective", effectiveURL, "元/吨", "元/斤"},
		{"notice location", "notice-effective", effectiveURL, "5号门", "6号门"},
		{"notice paywall", "notice-effective", effectiveURL, "筛混不返杂", "会员专享"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := string(fixture(t, tc.fixture))
			if !strings.Contains(raw, tc.old) {
				t.Fatalf("mutation target absent: %s", tc.old)
			}
			raw = strings.ReplaceAll(raw, tc.old, tc.new)
			if _, _, err := ParseReport([]byte(raw), tc.url, observedNow); err == nil {
				t.Fatal("ambiguous report accepted")
			}
		})
	}
	if _, _, err := ParseReport(fixture(t, "prices"), priceURL, observedNow.Add(-24*time.Hour)); err == nil {
		t.Fatal("future publication accepted")
	}
	if _, _, err := ParseReport(fixture(t, "prices"), "https://evil.example/article/1269531.html", observedNow); err == nil {
		t.Fatal("foreign URL accepted")
	}
}
func TestExplicitUnitOnlyWhenPublished(t *testing.T) {
	b := strings.Replace(string(fixture(t, "prices")), `<table`, `<p>单位：元/吨</p><table`, 1)
	obs, _, err := ParseReport([]byte(b), priceURL, observedNow)
	if err != nil || obs[0].Unit != "元/吨" {
		t.Fatalf("explicit unit ignored: %v", err)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fakeCollector(t *testing.T) (*Collector, map[string][]byte, *int) {
	t.Helper()
	pages := map[string][]byte{ListURL: fixture(t, "list"), priceURL: fixture(t, "prices"), arrivalURL: fixture(t, "arrivals"), futureURL: fixture(t, "notice-future")}
	// The second price/arrival reports are intentionally distinct dates and URLs.
	pages["https://www.boyar.cn/article/1269407.html"] = []byte(strings.ReplaceAll(strings.ReplaceAll(string(fixture(t, "prices")), "2026/10/27", "2026/10/26"), "10/28", "10/27"))
	pages["https://www.boyar.cn/article/1269407.html"] = []byte(strings.ReplaceAll(strings.ReplaceAll(string(pages["https://www.boyar.cn/article/1269407.html"]), "10月28日", "10月27日"), "2026-10-28", "2026-10-27"))
	pages["https://www.boyar.cn/article/1269401.html"] = []byte(strings.ReplaceAll(strings.ReplaceAll(string(fixture(t, "arrivals")), "10月28日", "10月27日"), "2026-10-28", "2026-10-27"))
	c := New()
	c.RequestSpacing = 0
	c.now = func() time.Time { return observedNow }
	count := 0
	c.Client.Transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		count++
		if r.Header.Get("Authorization") != "" || r.Header.Get("User-Agent") == "" {
			t.Error("unexpected auth or missing identification")
		}
		b, ok := pages[r.URL.String()]
		if !ok {
			return nil, errors.New("not in fixture map")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Header: http.Header{"Content-Type": []string{"text/html"}}, Request: r}, nil
	})
	return c, pages, &count
}
func TestCollectorBoundedAtomicAndIdempotent(t *testing.T) {
	c, pages, count := fakeCollector(t)
	obs, events, err := c.Prices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 44 || len(events) != 3 || *count != 4 {
		t.Fatalf("bounded price collection %d obs %d events %d calls", len(obs), len(events), *count)
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if added, err := s.AddObservations(obs); err != nil || added != 44 {
		t.Fatalf("store: %d %v", added, err)
	}
	c.now = func() time.Time { return observedNow.Add(time.Hour) }
	again, _, err := c.Prices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if added, err := s.AddObservations(again); err != nil || added != 0 {
		t.Fatalf("duplicate first availability changed: %d %v", added, err)
	}
	saved, _ := s.Observation(obs[0].ID)
	if !saved.AvailableAt.Equal(observedNow) {
		t.Fatal("first available time overwritten")
	}
	pages["https://www.boyar.cn/article/1269407.html"] = []byte(`<html>会员登录</html>`)
	bad, badEvents, err := c.Prices(context.Background())
	if err == nil || len(bad) != 0 || len(badEvents) != 0 {
		t.Fatal("partial parse treated as successful source")
	}
	arrivals, arrivalEvents, err := c.Arrivals(context.Background())
	if err != nil || len(arrivals) != 2 || len(arrivalEvents) != 2 {
		t.Fatalf("independent arrivals source: %v", err)
	}
}
func TestCollectorRejectsLimitsAndTransportFailures(t *testing.T) {
	for _, limit := range []int{-1, 4} {
		c, _, count := fakeCollector(t)
		c.ReportsPerSource = limit
		if _, _, err := c.Prices(context.Background()); err == nil || *count != 0 {
			t.Fatal("invalid report bound accepted")
		}
	}
	t.Run("http restriction", func(t *testing.T) {
		c, _, _ := fakeCollector(t)
		c.Client.Transport = roundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("restricted")), Request: r}, nil
		})
		if _, _, err := c.Arrivals(context.Background()); err == nil {
			t.Fatal("403 treated as available")
		}
	})
	t.Run("body limit", func(t *testing.T) {
		c, pages, _ := fakeCollector(t)
		pages[ListURL] = []byte(strings.Repeat(" ", maxBody+1))
		if _, _, err := c.Arrivals(context.Background()); err == nil {
			t.Fatal("body limit ignored")
		}
	})
	t.Run("login redirect", func(t *testing.T) {
		c, _, count := fakeCollector(t)
		c.Client.Transport = roundTripper(func(r *http.Request) (*http.Response, error) {
			*count++
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://www.boyar.cn/login"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		})
		if _, _, err := c.Arrivals(context.Background()); err == nil || *count != 1 {
			t.Fatal("login redirect followed")
		}
	})
	t.Run("cancel spacing", func(t *testing.T) {
		c, _, _ := fakeCollector(t)
		c.RequestSpacing = time.Hour
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := c.Arrivals(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel ignored: %v", err)
		}
	})
}
