package marketchart

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const minuteFixture = `var _cornwatch=([{"d":"2026-10-29 10:00:00","o":"2220","h":"2222","l":"2219","c":"2221","v":"20","p":"100"},{"d":"2026-10-29 10:01:00","o":"2221","h":"2223","l":"2220","c":"2222","v":"10","p":"102"},{"d":"2026-10-29 10:02:00","o":"2222","h":"2224","l":"2221","c":"2223","v":"11","p":"103"}]);`

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, e := time.Parse(time.RFC3339, s)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestMinuteConservativeBoundaryAndNaturalTime(t *testing.T) {
	for _, tc := range []struct {
		stamp string
		count int
	}{{"2026-10-29T10:01:00+08:00", 1}, {"2026-10-29T10:02:00+08:00", 2}, {"2026-10-29T10:03:00+08:00", 3}} {
		t.Run(tc.stamp, func(t *testing.T) {
			r, e := parseMinute([]byte(minuteFixture), "c2701", "1m", at(t, tc.stamp))
			if e != nil || len(r.Bars) != tc.count {
				t.Fatalf("%+v %v", r, e)
			}
			for _, b := range r.Bars {
				if b.TradeDate != "" || b.IsDemo || b.AvailableAt.IsZero() {
					t.Fatal("invented date or missing provenance")
				}
			}
		})
	}
	r, e := parseMinute([]byte(minuteFixture), "c2701", "5m", at(t, "2026-10-29T10:05:00+08:00"))
	if e != nil || len(r.Bars) != 1 {
		t.Fatalf("period guard: %+v %v", r, e)
	}
	night := strings.ReplaceAll(minuteFixture, "2026-10-29 10:", "2026-10-28 21:")
	r, e = parseMinute([]byte(night), "c2701", "1m", at(t, "2026-10-28T22:00:00+08:00"))
	if e != nil || r.Bars[0].Timestamp.Day() != 28 || r.Bars[0].TradeDate != "" {
		t.Fatal("night timestamp relabeled")
	}
}
func TestMinuteRejectInvalid(t *testing.T) {
	cases := map[string]string{
		"javascript": minuteFixture + "alert(1)", "callback": strings.Replace(minuteFixture, "_cornwatch", "_other", 1), "empty": "var _cornwatch=([]);",
		"bad time": strings.Replace(minuteFixture, "2026-10-29 10:00:00", "2026-02-30 10:00:00", 1), "duplicate": strings.Replace(minuteFixture, "10:01:00", "10:00:00", 1),
		"OHLC": strings.Replace(minuteFixture, `"h":"2222"`, `"h":"2200"`, 1), "nan": strings.Replace(minuteFixture, `"c":"2221"`, `"c":"NaN"`, 1),
		"negative": strings.Replace(minuteFixture, `"v":"20"`, `"v":"-1"`, 1), "fractional": strings.Replace(minuteFixture, `"p":"100"`, `"p":"1.2"`, 1),
		"duplicate keys": strings.Replace(minuteFixture, `"c":"2221"`, `"c":"2200","c":"2221"`, 1),
		"unknown":        strings.Replace(minuteFixture, `"d":`, `"x":"a","d":`, 1), "missing price": strings.Replace(minuteFixture, `"o":"2220",`, "", 1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, e := parseMinute([]byte(input), "c2701", "1m", at(t, "2026-10-29T10:03:00+08:00")); e == nil {
				t.Fatal("accepted malformed input")
			}
		})
	}
	if _, e := parseMinute([]byte(minuteFixture), "c2701", "1m", at(t, "2026-10-29T10:00:00+08:00")); e == nil {
		t.Fatal("accepted no mature data")
	}
	input := strings.Replace(minuteFixture, `,"p":"100"`, "", 1)
	r, e := parseMinute([]byte(input), "c2701", "1m", at(t, "2026-10-29T10:03:00+08:00"))
	if e != nil || r.HasOpenInterest {
		t.Fatal("missing OI invented")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMinuteHTTP(t *testing.T) {
	c := newMinuteCollector()
	c.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("symbol") != "C2701" || r.URL.Query().Get("type") != "1" || r.Header.Get("Referer") == "" {
			t.Fatal("bad request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(minuteFixture)), Header: http.Header{}}, nil
	})
	if _, e := c.fetch(context.Background(), "c2701", "1m", time.Now().AddDate(1, 0, 0)); e != nil {
		t.Fatal(e)
	}
	for _, status := range []int{429, 503} {
		c.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("busy")), Header: http.Header{}}, nil
		})
		if _, e := c.fetch(context.Background(), "c2701", "1m", time.Now()); e == nil || !strings.Contains(e.Error(), fmt.Sprint(status)) {
			t.Fatal("status not handled")
		}
	}
	if e := c.client.CheckRedirect(nil, nil); e == nil {
		t.Fatal("redirect accepted")
	}
}
func TestValidateRequest(t *testing.T) {
	for _, r := range []Request{{Symbol: "C0"}, {Symbol: "c2702"}, {Symbol: "c2713"}, {Symbol: "rb2701"}, {Symbol: "c2701&x=1"}, {Symbol: "c2701", Interval: "2m"}, {Symbol: "c2701", Source: "bad"}, {Symbol: "c2701", Source: "imported", Interval: "1m"}, {Symbol: "c2701", Limit: -1}, {Symbol: "c2701", Limit: 6001}, {Symbol: "c2701", From: "2026-02-30"}, {Symbol: "c2701", From: "2026-10-29", To: "2026-10-28"}} {
		if _, e := Validate(r); e == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	for _, symbol := range []string{"c2701", "cs2703", "lh2705", "m2708", "m2712"} {
		if _, e := Validate(Request{Symbol: symbol}); e != nil {
			t.Fatal(e)
		}
	}
}
