package currentanalysis

import (
	"strings"
	"testing"
	"time"

	"cornwatch/internal/marketdaily"
)

func TestInputDigestIncludesDailyReportMetadata(t *testing.T) {
	now, live, daily := evidenceInputs()
	baseline, err := Build(live, daily, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*marketdaily.Report)
	}{
		{"open interest availability", func(d *marketdaily.Report) { d.HasOpenInterest = false }},
		{"settlement availability", func(d *marketdaily.Report) { d.HasSettlement = false }},
		{"source identity", func(d *marketdaily.Report) { d.Source = "another explicitly identified source" }},
		{"source URL", func(d *marketdaily.Report) { d.SourceURL = "https://example.invalid/other-daily-endpoint" }},
		{"raw response digest", func(d *marketdaily.Report) { d.SHA256 = strings.Repeat("b", 64) }},
		{"actual collection time", func(d *marketdaily.Report) { d.AvailableAt = d.AvailableAt.Add(-time.Minute) }},
		{"source limitations", func(d *marketdaily.Report) {
			d.Warnings = append(append([]string{}, d.Warnings...), "source revised its reporting scope")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// All OHLCV bars and quote/industry inputs remain identical. Availability
			// flags and provenance still change the interpretation/audit input.
			revised := daily
			tc.mutate(&revised)
			report, err := Build(live, revised, nil, now)
			if err != nil {
				t.Fatal(err)
			}
			if report.InputSHA256 == baseline.InputSHA256 {
				t.Fatalf("daily %s omitted from input fingerprint", tc.name)
			}
			if tc.name == "open interest availability" && (report.Technical.Latest.OIChange != nil || baseline.Technical.Latest.OIChange == nil) {
				t.Fatal("fixture did not exercise differing interest interpretation")
			}
		})
	}
}
