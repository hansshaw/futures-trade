package importcorn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func sampleTime() time.Time { return time.Date(2026, 10, 28, 18, 0, 0, 0, china) }
func sampleAnnouncement(t *testing.T) announcement {
	t.Helper()
	item, err := parseList(fixture(t, "list.json"), sampleTime())
	if err != nil || item == nil {
		t.Fatalf("%v %v", item, err)
	}
	return *item
}

func TestVerifiedOfficialResultKeepsRateAndSeparateDates(t *testing.T) {
	item := sampleAnnouncement(t)
	o, e, err := parseDetail(fixture(t, "result.json"), item, sampleTime())
	if err != nil || len(o) != 1 || len(e) != 1 {
		t.Fatalf("%v %v %v", o, e, err)
	}
	if o[0].Kind != "import_auction_rate" || o[0].Value != 41 || o[0].Unit != "%" || o[0].IsDemo || e[0].IsDemo || o[0].ObservedAt.Format("2006-01-02 15:04:05") != "2026-10-27 00:00:00" || !o[0].AvailableAt.Equal(sampleTime()) || e[0].PublishedAt.Format("2006-01-02 15:04:05") != "2026-10-27 10:00:13" {
		t.Fatalf("wrong field semantics: %+v %+v", o[0], e[0])
	}
	if !strings.Contains(o[0].Note, "不是当天新签进口") || !strings.Contains(o[0].Note, "不据百分比反推") || !strings.Contains(o[0].SourceURL, item.ID) || e[0].Category != "import_auction" {
		t.Fatal("provenance/semantics missing")
	}
	// Branch rows are not counted again and rates are not converted into tonnes.
	if strings.Contains(o[0].Kind, "traded") || strings.Contains(o[0].Kind, "offered") {
		t.Fatal("invented auction quantity")
	}
}

func TestSameContentStableIDsButCorrectionsAreVersioned(t *testing.T) {
	item := sampleAnnouncement(t)
	body := fixture(t, "result.json")
	o, e, err := parseDetail(body, item, sampleTime())
	if err != nil {
		t.Fatal(err)
	}
	later := sampleTime().Add(time.Hour)
	o2, e2, err := parseDetail(body, item, later)
	if err != nil {
		t.Fatal(err)
	}
	if o[0].ID != o2[0].ID || e[0].ID != e2[0].ID || !o2[0].AvailableAt.Equal(later) {
		t.Fatal("repeat result should retain identity, with collector receipt supplied independently")
	}
	updated := []byte(strings.ReplaceAll(string(body), "41%", "42%"))
	o3, e3, err := parseDetail(updated, item, later)
	if err != nil {
		t.Fatal(err)
	}
	if o3[0].ID == o[0].ID || e3[0].ID == e[0].ID || o3[0].Value != 42 {
		t.Fatal("source correction overwrites old evidence identity")
	}
}

func TestNoImportAuctionDoesNotBecomeZero(t *testing.T) {
	for _, body := range []string{`{"status":0,"data":{"rows":[]}}`, `{"status":0,"data":{"rows":[{"JYGG_BT":"中储粮网10月28日兰州分公司玉米竞价采购结果","JYGG_LX":"交易结果"},{"JYGG_BT":"中储粮网10月28日进口大豆竞价销售结果","JYGG_LX":"交易结果"}]}}`} {
		item, err := parseList([]byte(body), sampleTime())
		if err != nil || item != nil {
			t.Fatalf("irrelevant/missing import result became a fact: %+v %v", item, err)
		}
	}
	if _, err := totalRate("<table><tr><th>销售单位</th><th>成交率</th></tr><tr><td>合计</td><td>—</td></tr></table>"); err == nil {
		t.Fatal("missing rate became zero")
	}
	rate, err := totalRate("<table><tr><th>销售单位</th><th>成交率</th></tr><tr><td>合计</td><td>0%</td></tr></table>")
	if err != nil || rate != 0 {
		t.Fatal("explicit published zero should be retained")
	}
}

func TestFailClosedOnChangedRateSchema(t *testing.T) {
	for _, markup := range []string{
		"<p>合计成交率 41%</p>",
		"<table><tr><th>销售单位</th><th>成交率</th></tr><tr><td>分公司</td><td>41%</td></tr></table>",
		"<table><tr><th>销售单位</th><th>成交率</th></tr><tr><td>合计</td><td>101%</td></tr></table>",
		"<table><tr><th>销售单位</th><th>成交率</th></tr><tr><td>合计</td><td>NaN%</td></tr></table>",
		"<table><tr><th>销售单位</th><th>成交率</th></tr><tr><td>合计</td><td>41%</td></tr><tr><td>合计</td><td>42%</td></tr></table>",
		"<table><tr><th>销售单位</th><th>计划数量</th><th>成交率</th></tr><tr><td>合计</td><td>100</td><td>41%</td></tr></table>",
	} {
		if _, err := totalRate(markup); err == nil {
			t.Fatalf("unexpected markup accepted: %s", markup)
		}
	}
}

func TestFutureDateWrongIdentityAndHTTP200ThrottleRejected(t *testing.T) {
	item := sampleAnnouncement(t)
	body := fixture(t, "result.json")
	for _, modified := range [][]byte{
		fixture(t, "limited.json"),
		[]byte(strings.ReplaceAll(string(body), item.ID, "00000000000000000000000000000000")),
		[]byte(strings.ReplaceAll(string(body), "交易结果", "交易公告")),
		[]byte(strings.ReplaceAll(string(body), "2026-10-27 10:00:13", "2026-10-29 10:00:13")),
		[]byte(`{"status":108,"data":{}}`),
		[]byte(`<html>login</html>`),
	} {
		if _, _, err := parseDetail(modified, item, sampleTime()); err == nil {
			t.Fatalf("invalid result accepted %s", modified)
		}
	}
	if _, err := parseList(fixture(t, "list.json"), sampleTime().AddDate(0, 0, -10)); err == nil {
		t.Fatal("future list accepted")
	}
	if _, err := auctionDate("中储粮网2月31日进口玉米竞价销售结果", sampleTime()); err == nil {
		t.Fatal("impossible auction date accepted")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCollectorTwoPublicRequestsAndTestSyncCache(t *testing.T) {
	c := New()
	now := sampleTime()
	c.Now = func() time.Time { return now }
	calls := 0
	c.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Scheme != "https" || r.URL.Host != "www.esinograin.com" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("unexpected request context")
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("unbounded network context")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		data := fixture(t, "list.json")
		if r.URL.Path == "/api/others/getTransactionAnnouncementDetail" {
			if r.PostForm.Get("jyggId") != sampleAnnouncement(t).ID {
				t.Fatal("wrong latest announcement")
			}
			data = fixture(t, "result.json")
		} else if r.PostForm.Get("jyggBt") != "进口玉米" || r.PostForm.Get("jyggLx") != "交易结果" {
			t.Fatal("wrong list filter")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
	})}
	o, e, err := c.Auction(context.Background())
	if err != nil || calls != 2 || len(o) != 1 {
		t.Fatalf("%v calls=%d", err, calls)
	}
	e[0].Products[0] = "altered"
	now = now.Add(30 * time.Second)
	o2, e2, err := c.Auction(context.Background())
	if err != nil || calls != 2 || !o2[0].AvailableAt.Equal(o[0].AvailableAt) || e2[0].Products[0] != "c" {
		t.Fatal("cache reset timestamps or leaked mutable storage")
	}
}

func TestCollectorNoResultsMakesOnlyOneRequest(t *testing.T) {
	c := New()
	c.Now = sampleTime
	calls := 0
	c.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":0,"data":{"rows":[]}}`)), Request: r}, nil
	})}
	o, e, err := c.Auction(context.Background())
	if err != nil || len(o) != 0 || len(e) != 0 || calls != 1 {
		t.Fatalf("no result fabricated data: %v %v %v calls=%d", o, e, err, calls)
	}
}

func TestCollectorRejectsHTTPErrorOversizeAndBusinessThrottle(t *testing.T) {
	for _, tc := range []struct {
		code int
		body string
	}{{429, "limited"}, {200, strings.Repeat("x", responseLimit+1)}, {200, string(fixture(t, "limited.json"))}} {
		c := New()
		c.Now = sampleTime
		c.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
		})}
		if _, _, err := c.Auction(context.Background()); err == nil {
			t.Fatal("invalid HTTP result accepted")
		}
	}
}

func TestFixturesAreValidJSON(t *testing.T) {
	for _, name := range []string{"list.json", "result.json", "limited.json"} {
		if !json.Valid(fixture(t, name)) {
			t.Fatal(name)
		}
	}
}

func TestPublicAuctionLiveProbe(t *testing.T) {
	if os.Getenv("CORNWATCH_IMPORTCORN_PROBE") != "1" {
		t.Skip("explicit low-frequency public probe disabled")
	}
	o, e, err := New().Auction(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range o {
		t.Logf("%s=%g%s observed=%s available=%s source=%s", item.Kind, item.Value, item.Unit, item.ObservedAt.Format(time.RFC3339), item.AvailableAt.Format(time.RFC3339), item.SourceURL)
	}
	t.Logf("observations=%d events=%d", len(o), len(e))
}
