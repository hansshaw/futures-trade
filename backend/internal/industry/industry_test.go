package industry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const lnReportURL = "https://lcj.ln.gov.cn/lswzcb/xwdt24/tjxx/2026102814442589536/index.shtml"
const moaReportURL = "https://xmsyj.moa.gov.cn/jcyj/202610/t20261027_6488962.htm"

var testNow = time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name + ".html")
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestLiaoningFactualFixture(t *testing.T) {
	obs, events, err := ParseLiaoning(fixture(t, "liaoning_report"), lnReportURL, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 || len(events) != 1 {
		t.Fatal("wrong counts")
	}
	if obs[0].Value != 2134 || obs[1].Value != 2152 || obs[0].Unit != "元/吨" {
		t.Fatalf("wrong prices: %+v", obs)
	}
	if obs[0].ObservedAt.In(shanghai).Format("2006-01-02") != "2026-10-27" || !obs[0].AvailableAt.Equal(testNow) {
		t.Fatal("date or first available time wrong")
	}
	if !strings.Contains(obs[0].Entity, "全省14市") || !strings.Contains(obs[0].Note, "不是单个工厂当日报价") || obs[0].IsDemo {
		t.Fatal("scope lost")
	}
	if events[0].PublishedAt.In(shanghai).Format("2006-01-02") != "2026-10-27" || events[0].SourceURL != lnReportURL || !events[0].AvailableAt.Equal(testNow) {
		t.Fatal("event provenance lost")
	}
	second, next, err := ParseLiaoning(fixture(t, "liaoning_report"), lnReportURL, testNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if obs[0].ID != second[0].ID || events[0].ID != next[0].ID {
		t.Fatal("fetch time changed immutable IDs")
	}
	corrected := strings.Replace(string(fixture(t, "liaoning_report")), "2,134", "2,135", 1)
	changed, _, err := ParseLiaoning([]byte(corrected), lnReportURL, testNow)
	if err != nil || changed[0].ID == obs[0].ID {
		t.Fatal("source correction not versioned", err)
	}
}

func TestMOAFactualFixture(t *testing.T) {
	obs, events, err := ParseMOAFeed(fixture(t, "moa_report"), moaReportURL, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 3 || len(events) != 1 {
		t.Fatal("wrong counts")
	}
	for i, want := range []float64{2.42, 10.71, 3.42} {
		if obs[i].Value != want || obs[i].Unit != "元/公斤" || obs[i].Kind != "related_price" {
			t.Fatalf("units/scopes mixed: %+v", obs[i])
		}
	}
	if obs[0].ObservedAt.In(shanghai).Format("2006-01-02") != "2026-10-22" || !obs[0].AvailableAt.Equal(testNow) {
		t.Fatal("collection date inferred from publication")
	}
	if events[0].PublishedAt.In(shanghai).Format("2006-01-02 15:04:05") != "2026-10-27 14:38:00" {
		t.Fatal("precise publication lost")
	}
	if !strings.Contains(obs[0].Note, "全国500个县") || !strings.Contains(obs[1].Note, "非生猪期货交割现货") {
		t.Fatal("scope caveat missing")
	}
}

func TestListsAreBoundedToOfficialDomains(t *testing.T) {
	for _, source := range []string{"liaoning", "moa"} {
		t.Run(source, func(t *testing.T) {
			parse := ParseLiaoningList
			if source == "moa" {
				parse = ParseMOAList
			}
			data := fixture(t, source+"_list")
			links, err := parse(data)
			if err != nil || len(links) != 5 {
				t.Fatalf("list: %+v %v", links, err)
			}
			for _, link := range links {
				if !allowedURL(link.URL) {
					t.Fatal("untrusted URL")
				}
			}
			// Matching titles do not grant arbitrary links network access.
			poison := `<a href="https://127.0.0.1/private">` + links[0].Title + `</a><a href="javascript:alert(1)">` + links[0].Title + `</a>`
			links, err = parse(append(data, []byte(poison)...))
			if err != nil || len(links) != 5 {
				t.Fatal("poisoned link accepted", err)
			}
		})
	}
	for _, address := range []string{"http://lcj.ln.gov.cn/lswzcb/xwdt24/tjxx/index.shtml", "https://lcj.ln.gov.cn.evil.test/lswzcb/xwdt24/tjxx/index.shtml", "https://lcj.ln.gov.cn:444/lswzcb/xwdt24/tjxx/index.shtml", "https://a@lcj.ln.gov.cn/lswzcb/xwdt24/tjxx/index.shtml", lnReportURL + "?url=https://localhost/", lnReportURL + "#evil"} {
		if allowedURL(address) {
			t.Fatal("unsafe URL allowed", address)
		}
	}
}

func TestLiaoningFailsClosedOnSchemaAndDataChanges(t *testing.T) {
	original := string(fixture(t, "liaoning_report"))
	for name, mutate := range map[string]func(string) string{
		"unit": func(s string) string {
			return strings.Replace(s, "收购价格（元/吨）", "收购价格（元/公斤）", 1)
		},
		"column meaning":      func(s string) string { return strings.Replace(s, "进厂价格", "批发价格", 1) },
		"scope":               func(s string) string { return strings.Replace(s, "全省14市平均价", "某工厂报价", 1) },
		"missing price":       func(s string) string { return strings.Replace(s, "2,134", "—", 1) },
		"range":               func(s string) string { return strings.Replace(s, "2,134", "2100-2200", 1) },
		"zero":                func(s string) string { return strings.Replace(s, "2,134", "0", 1) },
		"invalid comma":       func(s string) string { return strings.Replace(s, "2,134", "21,34", 1) },
		"future publication":  func(s string) string { return strings.Replace(s, `content="2026-10-27"`, `content="2027-10-27"`, 1) },
		"missing publication": func(s string) string { return strings.Replace(s, `name="PubDate"`, `name="NoDate"`, 1) },
		"wrong content":       func(s string) string { return strings.Replace(s, "UCAP-CONTENT", "missing", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			if obs, _, err := ParseLiaoning([]byte(mutate(original)), lnReportURL, testNow); err == nil || len(obs) > 0 {
				t.Fatal("invalid report yielded prices")
			}
		})
	}
}

func TestMOAFailsClosedAndHandlesYearBoundary(t *testing.T) {
	original := string(fixture(t, "moa_report"))
	for name, mutate := range map[string]func(string) string{
		"scope": func(s string) string { return strings.Replace(s, "500个县", "300个市场", 1) },
		"unit":  func(s string) string { return strings.ReplaceAll(s, "元/公斤", "元/吨") },
		"missing hog": func(s string) string {
			return strings.Replace(s, "全国生猪平均价格", "全国猪肉平均价格", 1)
		},
		"future observation": func(s string) string {
			return strings.Replace(s, "采集日为10月22日", "采集日为10月29日", 1)
		},
		"invalid date":     func(s string) string { return strings.Replace(s, "采集日为10月22日", "采集日为2月30日", 1) },
		"stale collection": func(s string) string { return strings.Replace(s, "采集日为10月22日", "采集日为8月22日", 1) },
		"wrong body":       func(s string) string { return strings.ReplaceAll(s, "TRS_Editor", "other") },
	} {
		t.Run(name, func(t *testing.T) {
			if obs, _, err := ParseMOAFeed([]byte(mutate(original)), moaReportURL, testNow); err == nil || len(obs) > 0 {
				t.Fatal("invalid report yielded prices")
			}
		})
	}
	boundary := strings.Replace(original, "2026-10-27 14:38:00", "2026-01-03 14:38:00", 1)
	boundary = strings.Replace(boundary, "采集日为10月22日", "采集日为12月30日", 1)
	obs, _, err := ParseMOAFeed([]byte(boundary), moaReportURL, testNow)
	if err != nil || obs[0].ObservedAt.In(shanghai).Format("2006-01-02") != "2025-12-30" {
		t.Fatal("year boundary failed", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestHTTPCollectionLimitAndAtomicFailure(t *testing.T) {
	c := New()
	c.RequestSpacing = 0
	c.now = func() time.Time { return testNow }
	list := string(fixture(t, "liaoning_list"))
	detail := string(fixture(t, "liaoning_report"))
	count := 0
	c.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		count++
		if r.URL.String() == LiaoningURL {
			return response(list, 200), nil
		}
		return response(detail, 200), nil
	})
	obs, events, err := c.Liaoning(context.Background())
	if err != nil || len(obs) != 6 || len(events) != 3 || count != 4 {
		t.Fatalf("limit: %d %d requests=%d err=%v", len(obs), len(events), count, err)
	}
	count = 0
	c.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		count++
		if count == 1 {
			return response(list, 200), nil
		}
		if count == 3 {
			return response("failure", 503), nil
		}
		return response(detail, 200), nil
	})
	obs, events, err = c.Liaoning(context.Background())
	if err == nil || obs != nil || events != nil {
		t.Fatal("partial source falsely succeeded")
	}
	c.ReportsPerSource = 4
	if _, _, err = c.Liaoning(context.Background()); err == nil {
		t.Fatal("unbounded reports allowed")
	}
}

func TestHTTPRedirectResponseBoundsAndCancellation(t *testing.T) {
	c := New()
	c.RequestSpacing = 0
	for name, body := range map[string]string{"oversize": strings.Repeat("x", maxBodyBytes+1), "encoding": "\xff\xfe"} {
		t.Run(name, func(t *testing.T) {
			c.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(body, 200), nil })
			if _, _, err := c.Liaoning(context.Background()); err == nil {
				t.Fatal("invalid HTTP data accepted")
			}
		})
	}
	for _, target := range []string{"http://lcj.ln.gov.cn/", "https://example.com/", "https://lcj.ln.gov.cn/private"} {
		var requests atomic.Int32
		c.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			r := response("", 302)
			r.Header.Set("Location", target)
			return r, nil
		})
		if _, _, err := c.Liaoning(context.Background()); err == nil || requests.Load() != 1 {
			t.Fatal("redirect escaped source")
		}
	}
	c.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Liaoning(ctx); err == nil {
		t.Fatal("cancel ignored")
	}
	c.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network unavailable") })
	if _, _, err := c.MOAFeed(context.Background()); err == nil {
		t.Fatal("network failure ignored")
	}
}

// Opt-in only: routine tests never access a remote service.
func TestLiveOfficialSources(t *testing.T) {
	if os.Getenv("CORNWATCH_LIVE_INDUSTRY_TEST") != "1" {
		t.Skip("set CORNWATCH_LIVE_INDUSTRY_TEST=1 for explicit public-source probe")
	}
	c := New()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Run("Liaoning", func(t *testing.T) {
		obs, events, err := c.Liaoning(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("observations=%d events=%d", len(obs), len(events))
		for _, o := range obs {
			t.Logf("%s | %s | %.2f %s | %s", o.ObservedAt.In(shanghai).Format("2006-01-02"), o.Entity, o.Value, o.Unit, o.SourceURL)
		}
	})
	t.Run("MOAFeed", func(t *testing.T) {
		obs, events, err := c.MOAFeed(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("observations=%d events=%d", len(obs), len(events))
		for _, o := range obs {
			t.Logf("%s | %s | %.2f %s | %s", o.ObservedAt.In(shanghai).Format("2006-01-02"), o.Entity, o.Value, o.Unit, o.SourceURL)
		}
	})
}
