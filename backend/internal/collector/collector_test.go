package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseRSSAndAtomPreserveAvailability(t *testing.T) {
	now := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	fixtures := map[string]string{
		"rss":  `<rss><channel><title>授权源</title><item><title>山东玉米收购调整</title><description><![CDATA[<p>收购价2200元/吨</p>]]></description><link>https://example.com/one</link><pubDate>Wed, 02 Sep 2026 08:00:00 +0000</pubDate></item></channel></rss>`,
		"atom": `<feed xmlns="http://www.w3.org/2005/Atom"><title>授权源</title><entry><title>山东玉米收购调整</title><content type="html">&lt;p&gt;收购价2200元/吨&lt;/p&gt;</content><link rel="alternate" href="https://example.com/one"/><published>2026-09-02T16:00:00+08:00</published></entry></feed>`,
	}
	for name, data := range fixtures {
		t.Run(name, func(t *testing.T) {
			items, err := ParseFeed([]byte(data), "https://example.com/feed", now)
			if err != nil || len(items) != 1 {
				t.Fatalf("parse: %+v %v", items, err)
			}
			e := items[0]
			if e.Category != "purchase_price" || e.Region != "山东" || e.Body != "收购价2200元/吨" || !e.AvailableAt.Equal(now) || !e.IngestedAt.Equal(now) || e.IsDemo {
				t.Fatalf("incorrect provenance: %+v", e)
			}
		})
	}
}

func TestFeedIDsNormalizeEquivalentPublicationTimeZones(t *testing.T) {
	now := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	ids := []string{}
	for _, date := range []string{"2026-09-02T08:00:00Z", "2026-09-02T16:00:00+08:00"} {
		data := `<feed xmlns="http://www.w3.org/2005/Atom"><title>授权源</title><entry><title>玉米报价</title><summary>报价2200元</summary><link href="https://example.com/one"/><published>` + date + `</published></entry></feed>`
		items, err := ParseFeed([]byte(data), "https://example.com/feed", now)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, items[0].ID)
	}
	if ids[0] != ids[1] {
		t.Fatalf("same instant produced different IDs: %v", ids)
	}
}

func TestFeedRejectsMissingAndFutureDates(t *testing.T) {
	now := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	for _, date := range []string{"", "not-a-date", "2099-01-01T00:00:00Z"} {
		data := `<rss><channel><item><title>报价</title><description>2200元</description><pubDate>` + date + `</pubDate></item></channel></rss>`
		if _, err := ParseFeed([]byte(data), "https://example.com/feed", now); err == nil {
			t.Errorf("invalid publication accepted: %s", date)
		}
	}
}

type reviewRoundTripper func(*http.Request) (*http.Response, error)

func (f reviewRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWeatherTargetDateUsesRequestedTimeZone(t *testing.T) {
	c := New()
	c.Client.Transport = reviewRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("timezone") != "Asia/Shanghai" {
			t.Error("unexpected forecast timezone")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"daily":{"time":["2026-09-03"],"precipitation_sum":[10],"temperature_2m_max":[25],"temperature_2m_min":[15]}}`)), Header: make(http.Header)}, nil
	})
	items, err := c.Weather(context.Background())
	if err != nil || len(items) != 15 {
		t.Fatalf("weather: count=%d err=%v", len(items), err)
	}
	shanghai := time.FixedZone("Asia/Shanghai", 8*3600)
	for _, item := range items {
		if item.ObservedAt.In(shanghai).Hour() != 0 {
			t.Fatalf("forecast target date shifted by timezone: %s", item.ObservedAt)
		}
		if item.AvailableAt.IsZero() || item.IsDemo {
			t.Fatalf("invalid acquisition metadata: %+v", item)
		}
	}
}
