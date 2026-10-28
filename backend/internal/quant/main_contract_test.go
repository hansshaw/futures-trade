package quant

import (
	"strings"
	"testing"
	"time"

	"cornwatch/internal/domain"
)

func universe(days int) []domain.Bar {
	out := []domain.Bar{}
	for _, b := range series(makePrices(days, 1000)...) {
		b.Volume = 1000
		b.OpenInterest = 1000
		out = append(out, b)
		b.Symbol = "c2705"
		b.Volume = 500
		b.OpenInterest = 500
		out = append(out, b)
	}
	return out
}

func makePrices(n int, p float64) []float64 {
	a := make([]float64, n)
	for i := range a {
		a[i] = p
	}
	return a
}
func after(b []domain.Bar) time.Time { return b[len(b)-1].Timestamp.Add(time.Hour) }

func TestMainInitialNeedsTwoDatesAndTwoContracts(t *testing.T) {
	b := universe(1)
	if r := ResolveMain(b, after(b)); r.Symbol != "" || r.ConfirmationDays != 1 {
		t.Fatalf("premature initial selection: %+v", r)
	}
	b = append(b, b...)
	if r := ResolveMain(b, after(b)); r.Symbol != "" || r.ConfirmationDays != 1 {
		t.Fatal("duplicate records counted as trading dates")
	}
	b = universe(2)
	if r := ResolveMain(b, after(b)); r.Symbol != "c2701" {
		t.Fatalf("missing initial selection: %+v", r)
	}
	b = series(1000, 1100, 1200)
	if r := ResolveMain(b, after(b)); r.Symbol != "" {
		t.Fatal("called sole available series the market's main contract")
	}
}

func TestMainAsOfAndTwoDayRoll(t *testing.T) {
	b := universe(4)
	for _, i := range []int{5, 7} {
		b[i].Volume = 1200
		b[i].OpenInterest = 1200
	}
	r := ResolveMain(b, b[5].Timestamp)
	if r.Symbol != "c2701" || r.PendingSymbol != "c2705" || r.ConfirmationDays != 1 {
		t.Fatalf("future information leaked: %+v", r)
	}
	r = ResolveMain(b, after(b))
	if r.Symbol != "c2705" || r.PreviousSymbol != "c2701" {
		t.Fatalf("roll missing: %+v", r)
	}
}

func TestMainNeverRollsBackToEarlierContract(t *testing.T) {
	b := universe(6)
	for _, i := range []int{5, 7} {
		b[i].Volume = 1200
		b[i].OpenInterest = 1200
	}
	r := ResolveMain(b, after(b))
	if r.Symbol != "c2705" {
		t.Fatalf("rolled back to earlier delivery: %+v", r)
	}
}

func TestMainRequiresBothStrictTenPercentThresholds(t *testing.T) {
	for _, v := range []int64{1100, 1200} {
		b := universe(4)
		for _, i := range []int{5, 7} {
			b[i].Volume = v
			b[i].OpenInterest = 1100
		}
		if r := ResolveMain(b, after(b)); r.Symbol != "c2701" || r.PendingSymbol != "" {
			t.Fatalf("weak signal rolled: %+v", r)
		}
	}
}

func TestMainConfirmationInterruptedByMissingIncumbent(t *testing.T) {
	b := universe(5)
	for _, i := range []int{5, 7, 9} {
		b[i].Volume = 1200
		b[i].OpenInterest = 1200
	}
	b = append(b[:6], b[7:]...)
	r := ResolveMain(b, after(b))
	if r.Symbol != "c2701" || r.ConfirmationDays != 1 {
		t.Fatalf("missing incumbent did not interrupt confirmation: %+v", r)
	}
}

func TestMainTradeDatesDoNotAssumeCalendarAdjacency(t *testing.T) {
	b := universe(2)
	for i := 2; i < 4; i++ {
		b[i].Timestamp = b[i].Timestamp.AddDate(0, 0, 8)
		b[i].TradeDate = b[i].Timestamp.In(china).Format("2006-01-02")
	}
	if r := ResolveMain(b, after(b)); r.Symbol != "c2701" {
		t.Fatalf("invented a weekend/holiday reset: %+v", r)
	}
}

func TestMainExcludesDeliveryMonthAndMixedModes(t *testing.T) {
	b := universe(2)
	if r := ResolveMain(b, time.Date(2027, 1, 1, 12, 0, 0, 0, china)); r.Symbol != "" {
		t.Fatalf("returned delivery-month contract from stale history %+v", r)
	}
	b[1].IsDemo = true
	if r := ResolveMain(b, after(b)); r.Symbol != "" {
		t.Fatal("accepted mixed data modes")
	}
}

func TestMainCannotBeInitializedByConflictingVolumeAndInterest(t *testing.T) {
	b := universe(2)
	b[1].OpenInterest = 2000
	b[3].OpenInterest = 2000
	if r := ResolveMain(b, after(b)); r.Symbol != "" {
		t.Fatal("ambiguous initial leader accepted")
	}
}

func TestMainExpiredPendingAndMissingLatestCurrent(t *testing.T) {
	b := universe(1)
	r := ResolveMain(b, time.Date(2027, 1, 1, 12, 0, 0, 0, china))
	if r.PendingSymbol != "" {
		t.Fatal("expired pending symbol presented as eligible")
	}
	b = universe(3)
	b = append(b[:4], b[5])
	r = ResolveMain(b, after(b))
	if r.Symbol != "c2701" || !strings.Contains(r.Reason, "缺少原主力") {
		t.Fatalf("missing latest incumbent was not disclosed: %+v", r)
	}
}
