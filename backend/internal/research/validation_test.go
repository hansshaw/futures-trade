package research

import (
	"reflect"
	"strings"
	"testing"

	"cornwatch/internal/quant"
)

func validationConfig() quant.Config {
	return quant.Config{Symbol: "c2701", FastWindow: 5, SlowWindow: 20, InitialCapital: 100000, Lots: 1, FeePerLot: 1.2, SlippageTicks: 1, MarginRate: .12, MaxDrawdownPct: 20}
}

func TestValidationSplitsChronologicallyWithIndependentCostLedger(t *testing.T) {
	r, err := ValidateStrategy(validationConfig(), sequence(100))
	if err != nil {
		t.Fatal(err)
	}
	if r.TrainBars != 70 || r.TestBars != 30 || r.SplitDate != sequence(100)[70].TradeDate {
		t.Fatalf("bad split %+v", r)
	}
	if len(r.Segments) != 2 || len(r.CostScenarios) != 2 {
		t.Fatal("missing comparisons")
	}
	train, test := r.Segments[0].Metrics, r.Segments[1].Metrics
	// Train opens at index 20 and closes at 69: +49*10-22.4.
	closeTo(t, train.FinalEquity, 100467.6)
	// The holdout's prior-close warmup allows first open at 70, close
	// at 99: +29*10-22.4. Training gains/costs are not inherited.
	closeTo(t, test.FinalEquity, 100267.6)
	closeTo(t, test.TotalFees, 2.4)
	closeTo(t, test.TotalSlippage, 20)
	closeTo(t, r.CostScenarios[1].Metrics.FinalEquity, 100245.2)
	closeTo(t, r.CostScenarios[1].Metrics.TotalFees, 4.8)
	closeTo(t, r.CostScenarios[1].Metrics.TotalSlippage, 40)
	if test.TradeCount != 1 || r.CostScenarios[1].Metrics.TradeCount != 1 {
		t.Fatal("warmup trades counted")
	}
	if !reflect.DeepEqual(test, r.CostScenarios[0].Metrics) {
		t.Fatal("cost baseline not same test period")
	}
}

func TestValidationTrainingMetricsUnaffectedByFuture(t *testing.T) {
	b := sequence(100)
	a, err := ValidateStrategy(validationConfig(), b)
	if err != nil {
		t.Fatal(err)
	}
	for i := 70; i < len(b); i++ {
		b[i].Close += 100
		b[i].High += 100
		b[i].Settlement = b[i].Close
	}
	z, err := ValidateStrategy(validationConfig(), b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Segments[0], z.Segments[0]) {
		t.Fatal("test future altered train metrics")
	}
}

func TestValidationRejectsTooLittleHistoryAndMixedMode(t *testing.T) {
	if _, err := ValidateStrategy(validationConfig(), sequence(79)); err == nil {
		t.Fatal("accepted short holdout")
	}
	c := validationConfig()
	c.SlowWindow = 70
	if _, err := ValidateStrategy(c, sequence(100)); err == nil {
		t.Fatal("accepted insufficient train warmup")
	}
	c = validationConfig()
	c.SlippageTicks = 5001
	if _, err := ValidateStrategy(c, sequence(100)); err == nil {
		t.Fatal("accepted overflowing stress input")
	}
	b := sequence(100)
	b[99].IsDemo = true
	if _, err := ValidateStrategy(validationConfig(), b); err == nil {
		t.Fatal("accepted mixed mode")
	}
}

func TestValidationDisclosesUnfilledTestEndPosition(t *testing.T) {
	b := sequence(100)
	b[99].Volume = 0
	r, err := ValidateStrategy(validationConfig(), b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Segments[1].Metrics.OpenPosition != 1 {
		t.Fatal("missing unresolved position")
	}
	if !strings.Contains(strings.Join(r.Warnings, " "), "测试段期末仍有1手未平仓") {
		t.Fatal("missing named holdout warning")
	}
}
