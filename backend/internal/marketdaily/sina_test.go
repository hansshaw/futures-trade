package marketdaily

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// These tiny fixtures are synthetic parser inputs, never market observations.
const fixture = `var _cornwatch=([{"d":"2026-10-27","o":"2214","h":"2223","l":"2196","c":"2217","v":"666871","p":"1259904","s":"2208"},{"d":"2026-10-28","o":"2220","h":"2229","l":"2211","c":"2224","v":"609271","p":"1276926","s":"2219"},{"d":"2026-10-29","o":"2225","h":"2227","l":"2218","c":"2224","v":"148047","p":"1279369","s":"0"}]);`

func at(t *testing.T, text string) time.Time {
	t.Helper()
	now, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatal(err)
	}
	return now
}

func TestMatureBarsExcludeIntradayAndNight(t *testing.T) {
	for _, test := range []struct {
		instant string
		want    int
	}{
		{"2026-10-28T14:30:00+08:00", 1},
		{"2026-10-28T15:14:59+08:00", 1},
		{"2026-10-28T15:15:00+08:00", 2},
		{"2026-10-28T22:10:00+08:00", 2},
		{"2026-10-29T00:30:00+08:00", 2},
	} {
		t.Run(test.instant, func(t *testing.T) {
			report, err := Parse([]byte(prefix+fixture), "C2701", at(t, test.instant))
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Bars) != test.want || len(report.ExcludedIncomplete) != 3-test.want {
				t.Fatalf("wrong maturity: %+v", report)
			}
			if !report.HasOpenInterest || !report.HasSettlement {
				t.Fatal("incomplete future row affected mature coverage")
			}
			last := report.Bars[len(report.Bars)-1]
			if last.IsDemo || last.Symbol != "c2701" || last.Timestamp.Hour() != 15 || last.TradeDate != report.LastTradingDate {
				t.Fatalf("wrong metadata: %+v", last)
			}
			if len(report.SHA256) != 64 || report.SourceURL == "" || report.AvailableAt.IsZero() {
				t.Fatal("missing provenance")
			}
		})
	}
}

func TestMissingOptionalFieldsAreExplicit(t *testing.T) {
	input := strings.Replace(fixture, `,"p":"1276926","s":"2219"`, "", 1)
	r, err := Parse([]byte(input), "c2701", at(t, "2026-10-28T22:10:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	if r.HasOpenInterest || r.HasSettlement || len(r.MissingOpenInterestDates) != 1 || len(r.MissingSettlementDates) != 1 {
		t.Fatalf("missing coverage: %+v", r)
	}
	if r.Bars[1].OpenInterest != 0 || r.Bars[1].Settlement != 0 {
		t.Fatal("missing data was fabricated")
	}
	zero := strings.Replace(fixture, `"p":"1276926"`, `"p":"0"`, 1)
	r, err = Parse([]byte(zero), "c2701", at(t, "2026-10-28T22:10:00+08:00"))
	if err != nil || !r.HasOpenInterest {
		t.Fatalf("actual zero OI is valid: %v", err)
	}
}

func TestRejectUnsafeOrInvalidSource(t *testing.T) {
	cases := map[string]string{
		"injection":        fixture + `alert(1)`,
		"extra JSON array": strings.TrimSuffix(fixture, ");") + " []);",
		"callback":         strings.Replace(fixture, "_cornwatch", "_other", 1),
		"negative volume":  strings.Replace(fixture, `"609271"`, `"-1"`, 1),
		"fractional OI":    strings.Replace(fixture, `"1276926"`, `"1.5"`, 1),
		"nan":              strings.Replace(fixture, `"2224"`, `"NaN"`, 1),
		"OHLC":             strings.Replace(fixture, `"2229"`, `"2200"`, 1),
		"duplicate date":   strings.Replace(fixture, "2026-10-28", "2026-10-27", 1),
		"missing price":    strings.Replace(fixture, `"c":"2224",`, "", 1),
		"unknown field":    strings.Replace(fixture, `"d":"2026-10-27"`, `"x":1,"d":"2026-10-27"`, 1),
		"invalid date":     strings.Replace(fixture, "2026-10-28", "2026-02-30", 1),
		"empty":            "var _cornwatch=([]);",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(input), "c2701", at(t, "2026-10-28T22:10:00+08:00")); err == nil {
				t.Fatal("accepted bad source")
			}
		})
	}
	for _, symbol := range []string{"C0", "c2702", "c2713", "rb2701", "c2701&evil=1", "m2702"} {
		if _, err := Parse([]byte(fixture), symbol, time.Now()); err == nil {
			t.Fatalf("accepted %s", symbol)
		}
	}
	for _, symbol := range []string{"c2701", "cs2701", "lh2701", "m2708", "m2712"} {
		if _, err := Parse([]byte(fixture), symbol, at(t, "2026-10-28T22:10:00+08:00")); err != nil {
			t.Fatalf("rejected %s: %v", symbol, err)
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchUsesFixedHTTPSRequestAndRetainsSource(t *testing.T) {
	c := New()
	c.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != endpoint+"C2701" || r.Header.Get("Referer") != "https://finance.sina.com.cn/" {
			t.Fatalf("unexpected request: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture)), Header: make(http.Header), Request: r}, nil
	})
	r, err := c.Fetch(context.Background(), "C2701", at(t, "2026-10-28T22:10:00+08:00"))
	if err != nil || len(r.Bars) != 2 {
		t.Fatalf("fetch: %+v %v", r, err)
	}
	c.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { return nil, fmt.Errorf("secret upstream detail") })
	if _, err = c.Fetch(context.Background(), "c2701", time.Now()); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}
