package research

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"cornwatch/internal/domain"
)

func sequence(n int) []domain.Bar {
	bars := make([]domain.Bar, n)
	start := time.Date(2026, 1, 1, 15, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	for i := range bars {
		d := start.AddDate(0, 0, i)
		p := 1000 + float64(i)
		bars[i] = domain.Bar{Symbol: "c2701", Timestamp: d, TradeDate: d.Format("2006-01-02"), Open: p, High: p + 2, Low: p - 2, Close: p, Settlement: p, Volume: 1000 + int64(i), OpenInterest: 10000 + int64(i)}
	}
	return bars
}

func closeTo(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-8 {
		t.Fatalf("got %.12f want %.12f", got, want)
	}
}

func TestIndicatorInitializationAndHandCalculatedRamp(t *testing.T) {
	r, err := Analyze(sequence(70), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Points[3].MA5 != nil || r.Points[18].MA20 != nil || r.Points[58].MA60 != nil || r.Points[12].ATR14 != nil || r.Points[13].RSI14 != nil || r.Points[24].MACD != nil || r.Points[32].MACDSignal != nil || r.Points[19].PriorHigh20 != nil {
		t.Fatal("indicator before full warmup")
	}
	closeTo(t, *r.Points[4].MA5, 1002)
	closeTo(t, *r.Points[19].MA20, 1009.5)
	closeTo(t, *r.Points[59].MA60, 1029.5)
	closeTo(t, *r.Points[13].ATR14, 4)
	closeTo(t, *r.Points[14].RSI14, 100)
	// With linear +1/day closes, seeded EMA12 lags 5.5 and EMA26
	// lags 12.5, so DIF and its 9-sample average are exactly seven.
	closeTo(t, *r.Points[25].MACD, 7)
	closeTo(t, *r.Points[33].MACDSignal, 7)
	closeTo(t, *r.Points[33].MACDHist, 0)
	closeTo(t, *r.Points[20].PriorHigh20, 1021)
	closeTo(t, *r.Points[20].PriorLow20, 998)
	closeTo(t, *r.Latest.VolumeRatio20, 1069/1058.5)
	if r.Latest.OIChange == nil || *r.Latest.OIChange != 1 {
		t.Fatal("bad OI change")
	}
}

func TestWilderSmoothingKnownChanges(t *testing.T) {
	b := sequence(16)
	// First 14 differences alternate +1/-1: average gain=loss=0.5.
	for i := range b {
		p := 1000 + float64(i%2)
		b[i].Close = p
		b[i].Open = p
		b[i].High = p
		b[i].Low = p
		b[i].Settlement = p
	}
	// Next difference is +2. TR seed=(0+13*1)/14; at i14 TR=1.
	b[15].Open = 1002
	b[15].Close = 1002
	b[15].High = 1002
	b[15].Low = 1002
	b[15].Settlement = 1002
	r, err := Analyze(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, *r.Points[13].ATR14, 13.0/14)
	closeTo(t, *r.Points[14].ATR14, (13.0/14*13+1)/14)
	closeTo(t, *r.Points[14].RSI14, 50)
	closeTo(t, *r.Points[15].RSI14, 100*8.5/15)
}

func TestFlatSeriesAndMissingDenominators(t *testing.T) {
	b := sequence(70)
	for i := range b {
		b[i].Open = 1000
		b[i].Close = 1000
		b[i].High = 1000
		b[i].Low = 1000
		b[i].Settlement = 1000
		b[i].Volume = 0
		b[i].OpenInterest = 0
	}
	r, err := Analyze(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, *r.Latest.RSI14, 50)
	closeTo(t, *r.Latest.ATR14, 0)
	if r.Latest.VolumeRatio20 != nil || r.Latest.OIChangePct != nil {
		t.Fatal("invented ratio for zero denominator")
	}
	r, err = Analyze(b[:1], nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(j), `"ma5":null`) || !strings.Contains(string(j), `"change_pct":null`) {
		t.Fatal("missing data not serialized as null")
	}
}

func TestMACDImpulseHandCalculation(t *testing.T) {
	b := sequence(35)
	for i := range b {
		b[i].Open = 1000
		b[i].High = 1000
		b[i].Low = 1000
		b[i].Close = 1000
		b[i].Settlement = 1000
	}
	b[34].Open = 1013
	b[34].High = 1013
	b[34].Low = 1013
	b[34].Close = 1013
	b[34].Settlement = 1013
	r, err := Analyze(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, *r.Points[34].MACD, 28.0/27)
	closeTo(t, *r.Points[34].MACDSignal, 28.0/135)
	closeTo(t, *r.Points[34].MACDHist, 224.0/135)
}

func TestEveryPrefixIsUnchangedByFutureData(t *testing.T) {
	b := sequence(80)
	for i := 40; i < len(b); i++ {
		b[i].Close += float64(i * 3)
		b[i].High = b[i].Close + 5
		b[i].Settlement = b[i].Close
	}
	full, err := Analyze(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n < len(b); n++ {
		prefix, err := Analyze(b[:n], nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(prefix.Points, full.Points[:n]) {
			t.Fatalf("future input contaminated prefix of length %d", n)
		}
	}
}

func TestCurrentBarCannotMoveOwnBreakoutThreshold(t *testing.T) {
	b := sequence(21)
	r, _ := Analyze(b, nil)
	b[20].High = 9000
	b[20].Low = 1
	b[20].Close = 8000
	b[20].Settlement = 8000
	changed, err := Analyze(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, *r.Latest.PriorHigh20, *changed.Latest.PriorHigh20)
	closeTo(t, *r.Latest.PriorLow20, *changed.Latest.PriorLow20)
}

func TestTermStructureUsesSameVisibleDateModeAndCornOnly(t *testing.T) {
	b := sequence(70)
	last := b[len(b)-1]
	far := last
	far.Symbol = "c2705"
	far.Close += 20
	far.Open += 20
	far.High += 20
	far.Low += 20
	far.Settlement += 20
	demo := far
	demo.Symbol = "c2707"
	demo.IsDemo = true
	future := far
	future.Symbol = "c2709"
	future.Timestamp = future.Timestamp.Add(time.Hour)
	stale := b[len(b)-2]
	stale.Symbol = "c2711"
	starch := far
	starch.Symbol = "cs2701"
	badMonth := far
	badMonth.Symbol = "c2702"
	expired := far
	expired.Symbol = "c2601"
	conflictA := far
	conflictA.Symbol = "c2703"
	conflictB := conflictA
	conflictB.Volume++
	r, err := Analyze(b, []domain.Bar{far, demo, future, stale, starch, badMonth, expired, conflictA, conflictB})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.TermStructure) != 2 || r.Spread == nil || r.Spread.NearSymbol != "c2701" || r.Spread.FarSymbol != "c2705" {
		t.Fatalf("bad selection %+v %+v", r.TermStructure, r.Spread)
	}
	closeTo(t, r.Spread.NearMinusFar, -20)
	if r.Spread.TradeDate != last.TradeDate {
		t.Fatal("wrong spread date")
	}
}

func TestAnalyzeRejectsInvalidOrMixedInputs(t *testing.T) {
	for name, mutate := range map[string]func([]domain.Bar){"mixed contract": func(b []domain.Bar) { b[1].Symbol = "c2705" }, "mixed mode": func(b []domain.Bar) { b[1].IsDemo = true }, "unordered": func(b []domain.Bar) { b[0], b[1] = b[1], b[0] }, "nonfinite": func(b []domain.Bar) { b[0].Close = math.NaN() }} {
		t.Run(name, func(t *testing.T) {
			b := sequence(70)
			mutate(b)
			if _, err := Analyze(b, nil); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
	if _, err := Analyze(nil, nil); err == nil {
		t.Fatal("accepted empty input")
	}
}
