package market

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, china)
	if err != nil {
		panic(err)
	}
	return t
}
func fixtureFields() []string {
	f := strings.Split("玉米2701,150455,2220.000,2229.000,2211.000,2224.000,2223.000,2224.000,2224.000,2219.000,2208.000,1562,1805,1276926.000,609271,连,玉米,2026-10-28,1,,,,,,,,,2219.448,0.000,0,0.000,0,0.000,0,0.000,0,0.000,0,0.000,0,0.000,0", ",")
	return append(f, "0.000", "0")
}
func encoded(text string) []byte {
	b, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(text))
	if err != nil {
		panic(err)
	}
	return b
}
func wrapped(symbol string, f []string) []byte {
	return encoded(fmt.Sprintf("var hq_str_nf_%s=\"%s\";\n", symbol, strings.Join(f, ",")))
}

func TestParseActualContractAndSettlementChange(t *testing.T) {
	q, err := parseSina(wrapped("C2701", fixtureFields()), []string{"c2701"}, at("2026-10-28 17:37:35"), "https://hq.sinajs.cn/list=nf_C2701")
	if err != nil || len(q) != 1 {
		t.Fatalf("%v %#v", err, q)
	}
	got := q[0]
	if got.Symbol != "c2701" || got.Product != "c" || got.Name != "玉米2701" || got.OpenInterest != 1276926 || got.Volume != 609271 || got.PreviousSettlement != 2208 || got.ChangeBasis != "previous_settlement" || got.ChangePct < 0.7246 || got.ChangePct > 0.7247 || !got.ObservedAt.Equal(at("2026-10-28 15:04:55")) {
		t.Fatalf("%#v", got)
	}
}

func TestParserRejectsMalformedPayload(t *testing.T) {
	now := at("2026-10-28 17:00:00")
	address := "https://hq.sinajs.cn/list=nf_C2701"
	for _, tc := range []struct {
		name   string
		mutate func([]string) []string
	}{
		{"nan", func(f []string) []string { f[8] = "NaN"; return f }},
		{"infinity", func(f []string) []string { f[8] = "+Inf"; return f }},
		{"badOHLC", func(f []string) []string { f[3] = "2000"; return f }},
		{"fractionalVolume", func(f []string) []string { f[14] = "1.5"; return f }},
		{"overflowOI", func(f []string) []string { f[13] = "1e100"; return f }},
		{"future", func(f []string) []string { f[17] = "2026-10-29"; return f }},
		{"badClock", func(f []string) []string { f[1] = "256199"; return f }},
		{"badDate", func(f []string) []string { f[17] = "2026-02-31"; return f }},
		{"unsupportedFields", func(f []string) []string { return append(f, "extra") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseSina(wrapped("C2701", tc.mutate(fixtureFields())), []string{"c2701"}, now, address); err == nil {
				t.Fatal("accepted invalid")
			}
		})
	}
	valid := wrapped("C2701", fixtureFields())
	for _, body := range [][]byte{append(append([]byte{}, valid...), []byte("alert(1);")...), append(append([]byte{}, valid...), valid...), []byte(`var hq_str_nf_C0="";`), []byte(`<html>challenge</html>`), []byte("\xff"), []byte(strings.Repeat("x", maxResponseBytes+1))} {
		if _, err := parseSina(body, []string{"c2701"}, now, address); err == nil {
			t.Fatal("accepted unsafe wrapper")
		}
	}
	if _, err := parseSina(valid, []string{"c2701", "c2703"}, now, address); err == nil {
		t.Fatal("missing response accepted")
	}
}

func TestParserAllowsExplicitEmptyUnlistedButRequiresEachProduct(t *testing.T) {
	body := append(wrapped("C2701", fixtureFields()), []byte("var hq_str_nf_C2711=\"\";\n")...)
	if q, err := parseSina(body, []string{"c2701", "c2711"}, at("2026-10-28 17:00:00"), "https://hq.sinajs.cn/list=x"); err != nil || len(q) != 1 {
		t.Fatalf("%v %#v", err, q)
	}
	body = append(wrapped("C2701", fixtureFields()), []byte("var hq_str_nf_M2701=\"\";\n")...)
	if _, err := parseSina(body, []string{"c2701", "m2701"}, at("2026-10-28 17:00:00"), "https://hq.sinajs.cn/list=x"); err == nil {
		t.Fatal("missing entire requested product accepted")
	}
}

func TestContractSymbolsActualAndMonthRules(t *testing.T) {
	symbols := ContractSymbols(at("2026-10-28 17:00:00"))
	seen := map[string]bool{}
	for _, s := range symbols {
		if _, _, ok := contract(s); !ok {
			t.Fatal(s)
		}
		seen[s] = true
	}
	if !seen["c2701"] || !seen["cs2701"] || !seen["lh2701"] || !seen["m2612"] || !seen["m2708"] || seen["c2612"] || seen["c0"] || seen["c2801"] {
		t.Fatalf("%v", symbols)
	}
	for _, s := range []string{"c0", "c2713", "c2702", "m2702", "DCE.c2701", "c2711junk"} {
		if _, _, ok := contract(s); ok {
			t.Fatal(s)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

// Explicit opt-in only: public endpoint availability is not a deterministic CI assertion.
func TestPublicLiveProbe(t *testing.T) {
	if os.Getenv("CORNWATCH_PUBLIC_QUOTE_PROBE") != "1" {
		t.Skip("public network probe disabled")
	}
	quotes, err := New().Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range quotes {
		t.Logf("%s last=%.2f volume=%d oi=%d observed=%s received=%s", q.Symbol, q.Last, q.Volume, q.OpenInterest, q.ObservedAt.Format(time.RFC3339), q.ReceivedAt.Format(time.RFC3339))
	}
}

func (r roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return r(req) }
func TestCollectBoundedHTTPSAndEncoding(t *testing.T) {
	now := at("2026-10-28 17:00:00")
	c := New()
	c.Now = func() time.Time { return now }
	c.Client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "hq.sinajs.cn" || req.Header.Get("Referer") != "https://finance.sina.com.cn/" {
			t.Fatal(req.URL)
		}
		if _, ok := req.Context().Deadline(); !ok {
			t.Fatal("missing deadline")
		}
		var b strings.Builder
		for _, s := range ContractSymbols(now) {
			if strings.HasSuffix(s, "2701") {
				f := fixtureFields()
				fmt.Fprintf(&b, "var hq_str_nf_%s=\"%s\";\n", strings.ToUpper(s), strings.Join(f, ","))
			} else {
				fmt.Fprintf(&b, "var hq_str_nf_%s=\"\";\n", strings.ToUpper(s))
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(encoded(b.String())))), Request: req}, nil
	})}
	q, err := c.Collect(context.Background())
	if err != nil || len(q) != 4 {
		t.Fatalf("%v %v", err, q)
	}
}

func TestCollectRejectsHTTPFailureAndOversize(t *testing.T) {
	for _, code := range []int{403, 429, 200} {
		c := New()
		c.Client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1))), Request: req}, nil
		})}
		if _, err := c.Collect(context.Background()); err == nil {
			t.Fatal("failure accepted")
		}
	}
}
