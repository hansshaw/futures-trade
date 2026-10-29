package currentanalysis

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"cornwatch/internal/datahub"
	"cornwatch/internal/domain"
	"cornwatch/internal/market"
	"cornwatch/internal/marketdaily"
)

// Inputs in this file are isolated synthetic fixtures, never real observations.
func evidenceInputs() (time.Time, datahub.Live, marketdaily.Report) {
	now := time.Date(2026, 10, 28, 22, 30, 0, 0, china)
	q := market.Quote{Symbol: "c2701", Product: "c", Exchange: "DCE", TradeDate: "2026-10-28", ObservedAt: now.Add(-time.Second), ReceivedAt: now, Source: "isolated-test-source", SourceURL: "https://example.invalid/quotes", Last: 2200, Open: 2198, High: 2205, Low: 2195, PreviousSettlement: 2180, Volume: 10000, OpenInterest: 40000, ChangeBasis: "previous_settlement"}
	q.ChangePct = (q.Last/q.PreviousSettlement - 1) * 100
	far, starch := q, q
	far.Symbol, far.Last, far.Volume = "c2705", 2250, 1000
	starch.Symbol, starch.Product, starch.Last = "cs2701", "cs", 2500
	live := datahub.Live{Quotes: []market.Quote{q, far, starch}, SourceID: "test", Freshness: "fresh", Main: market.State{ProvisionalSymbol: "c2701"}, LatestDataAt: &q.ObservedAt}
	daily := marketdaily.Report{Source: "isolated-test-daily", SourceURL: "https://example.invalid/daily", AvailableAt: now.Add(-time.Minute), HasOpenInterest: true, HasSettlement: true, SHA256: strings.Repeat("a", 64), LastTradingDate: "2026-10-28", Warnings: []string{"fixture only"}}
	for i := 0; i < 80; i++ {
		stamp := time.Date(2026, 10, 28, 15, 0, 0, 0, china).AddDate(0, 0, i-79)
		price := float64(2100 + i)
		daily.Bars = append(daily.Bars, domain.Bar{Symbol: "c2701", Timestamp: stamp, TradeDate: stamp.Format("2006-01-02"), Open: price, High: price + 5, Low: price - 5, Close: price, Settlement: price - 3, Volume: int64(1000 + i), OpenInterest: int64(2000 + i)})
	}
	return now, live, daily
}

func TestBuildSeparatesDailyCloseAndSnapshotSettlementBasis(t *testing.T) {
	now, live, daily := evidenceInputs()
	// The completed day falls close-to-close while the separate snapshot is up
	// against previous settlement. Neither value may overwrite the other.
	for i, close := range []float64{2220, 2200} {
		b := &daily.Bars[len(daily.Bars)-2+i]
		b.Open, b.High, b.Low, b.Close = close, close+5, close-5, close
	}
	before, _ := json.Marshal(struct {
		L datahub.Live
		D marketdaily.Report
	}{live, daily})
	report, err := Build(live, daily, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	wantDaily := (2200.0/2220 - 1) * 100
	if report.Technical.Latest.ChangePct == nil || math.Abs(*report.Technical.Latest.ChangePct-wantDaily) > 1e-9 {
		t.Fatalf("daily close basis changed: %+v", report.Technical.Latest)
	}
	if report.Snapshot.ChangeBasis != "previous_settlement" || report.Snapshot.ChangePct <= 0 || report.Technical.Latest.ChangePct == nil || *report.Technical.Latest.ChangePct >= 0 {
		t.Fatal("snapshot settlement and daily close bases conflated")
	}
	if report.Technical.BarCount != len(daily.Bars) || !report.Technical.AsOf.Equal(daily.Bars[len(daily.Bars)-1].Timestamp) || report.Technical.Latest.Close != 2200 {
		t.Fatal("snapshot appended to completed daily bars")
	}
	if report.DailyURL != daily.SourceURL || report.DailySHA256 != daily.SHA256 || !report.DailyAvailableAt.Equal(daily.AvailableAt) || len(report.InputSHA256) != 64 {
		t.Fatal("missing provenance")
	}
	after, _ := json.Marshal(struct {
		L datahub.Live
		D marketdaily.Report
	}{live, daily})
	if string(before) != string(after) {
		t.Fatal("building the report mutated raw inputs")
	}
}

func TestBuildRejectsDemoAndFutureDailyEvidence(t *testing.T) {
	for _, kind := range []string{"demo", "future_bar", "future_availability", "contract_mismatch"} {
		t.Run(kind, func(t *testing.T) {
			now, live, daily := evidenceInputs()
			switch kind {
			case "demo":
				daily.Bars[0].IsDemo = true
			case "future_bar":
				b := &daily.Bars[len(daily.Bars)-1]
				b.Timestamp = now.AddDate(0, 0, 1)
				b.TradeDate = b.Timestamp.Format("2006-01-02")
			case "future_availability":
				daily.AvailableAt = now.Add(time.Hour)
			case "contract_mismatch":
				daily.Bars[len(daily.Bars)-1].Symbol = "c2705"
			}
			if _, err := Build(live, daily, nil, now); err == nil {
				t.Fatal("accepted unavailable or mismatched daily evidence")
			}
		})
	}
}

func TestBuildFiltersUnavailableObservationsAndKeepsLatestRevision(t *testing.T) {
	now, live, daily := evidenceInputs()
	base := domain.Observation{ID: "latest-valid", Kind: "purchase_price", Region: "test-region", Entity: "test-price", Unit: "元/吨", Source: "test-official", Value: 2100, ObservedAt: now.Add(-24 * time.Hour), AvailableAt: now.Add(-time.Hour)}
	observations := []domain.Observation{base}
	for _, kind := range []string{"old-revision", "future-available", "future-observed", "demo", "missing-available", "not-finite"} {
		o := base
		o.ID = kind
		switch kind {
		case "old-revision":
			o.AvailableAt = now.Add(-2 * time.Hour)
			o.Value = 2090
		case "future-available":
			o.AvailableAt = now.Add(time.Second)
			o.Entity = kind
		case "future-observed":
			o.ObservedAt = now.Add(time.Hour)
			o.Entity = kind
		case "demo":
			o.IsDemo = true
			o.Entity = kind
		case "missing-available":
			o.AvailableAt = time.Time{}
			o.Entity = kind
		case "not-finite":
			o.Value = math.Inf(1)
			o.Entity = kind
		}
		observations = append(observations, o)
	}
	r, err := Build(live, daily, observations, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Evidence) != 1 || r.Evidence[0].ID != base.ID || r.Evidence[0].Value != base.Value {
		t.Fatalf("unavailable/older observation entered report: %+v", r.Evidence)
	}
}

func TestBuildMissingInterestDoesNotInferPositioning(t *testing.T) {
	now, live, daily := evidenceInputs()
	daily.HasOpenInterest = false
	daily.Bars[len(daily.Bars)-1].OpenInterest = 0
	r, err := Build(live, daily, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Technical.Latest.OIChange != nil || r.Technical.Latest.OIChangePct != nil || !strings.Contains(r.Technical.Latest.PriceOILabel, "缺失") {
		t.Fatalf("missing OI treated as actual closing of positions: %+v", r.Technical.Latest)
	}
	for _, insight := range r.Technical.Insights {
		if insight.Title == "价格、成交量与持仓量" && (!strings.Contains(insight.Body, "缺失") || strings.Contains(insight.Body, " · 减仓") || strings.Contains(insight.Body, " · 增仓")) {
			t.Fatal("missing OI generated positioning narrative")
		}
	}
}

func TestBuildSpreadsRequireSameSourceDateAndNearbyTimes(t *testing.T) {
	for _, kind := range []string{"compatible", "different_source", "different_date", "old_time", "future_time"} {
		t.Run(kind, func(t *testing.T) {
			now, live, daily := evidenceInputs()
			for i := 1; i < len(live.Quotes); i++ {
				switch kind {
				case "different_source":
					live.Quotes[i].Source = "unrelated-source"
				case "different_date":
					live.Quotes[i].TradeDate = "2026-10-27"
				case "old_time":
					live.Quotes[i].ObservedAt = now.Add(-6 * time.Minute)
				case "future_time":
					live.Quotes[i].ObservedAt = now.Add(2 * time.Minute)
				}
			}
			r, err := Build(live, daily, nil, now)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "compatible" {
				if len(r.Spreads) != 2 || r.Spreads[0].Value != -50 || r.Spreads[1].Value != 300 {
					t.Fatalf("incorrect spread inputs: %+v", r.Spreads)
				}
			} else if len(r.Spreads) != 0 {
				t.Fatalf("incompatible quote used in spread: %+v", r.Spreads)
			}
		})
	}
}

func TestSelectRequiresActualUnexpiredCornQuote(t *testing.T) {
	now, live, _ := evidenceInputs()
	for _, kind := range []string{"empty", "expired", "delivery_month", "future", "continuous", "wrong_product"} {
		t.Run(kind, func(t *testing.T) {
			input := live
			input.Quotes = append([]market.Quote{}, live.Quotes[:1]...)
			switch kind {
			case "empty":
				input.Quotes = nil
			case "expired":
				input.Quotes[0].Symbol = "c2609"
			case "delivery_month":
				input.Quotes[0].Symbol = "c2610"
			case "future":
				input.Quotes[0].ObservedAt = now.Add(time.Hour)
			case "continuous":
				input.Quotes[0].Symbol = "c0"
			case "wrong_product":
				input.Quotes[0].Product = "cs"
			}
			if _, _, err := Select(input, now); err == nil {
				t.Fatalf("selected unusable quote %s", kind)
			}
		})
	}
}
