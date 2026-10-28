package quant

import "testing"

func TestEvaluationWarmupCannotTradeOrPayCosts(t *testing.T) {
	c := config()
	b := series(1000, 1100, 1200, 1300, 1400)
	r, err := BacktestFrom(c, b, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.EquityCurve) != 2 || r.BarCount != 2 || r.EvaluationStart != 3 {
		t.Fatal("warmup included in evaluation output")
	}
	if len(r.Trades) != 2 || r.Trades[0].Timestamp != b[3].Timestamp || r.Trades[0].Price != 1300 {
		t.Fatalf("bad holdout fills %+v", r.Trades)
	}
	near(t, r.Metrics.FinalEquity, 100977.6)
	near(t, r.Metrics.TotalFees, 2.4)
	near(t, r.Metrics.TotalSlippage, 20)
}

func TestEvaluationWarmupCannotHaltFreshAccount(t *testing.T) {
	c := config()
	c.InitialCapital = 10000
	c.MaxDrawdownPct = 1
	b := series(1000, 1100, 900, 800, 900, 1000)
	// Full evaluation loses and halts at index 3; an account starting
	// at index 4 must still execute its preceding-close short signal.
	full, err := Backtest(c, b)
	if err != nil {
		t.Fatal(err)
	}
	if !warningContains(full, "触发权益") {
		t.Fatal("fixture did not trigger risk")
	}
	r, err := BacktestFrom(c, b, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) == 0 || r.Trades[0].Timestamp != b[4].Timestamp {
		t.Fatal("pre-evaluation risk state leaked into fresh account")
	}
}

func TestFirstEvaluationOpenDoesNotReadOwnClose(t *testing.T) {
	b := series(1000, 1100, 1200, 1300, 1400)
	r, err := BacktestFrom(config(), b, 3)
	if err != nil {
		t.Fatal(err)
	}
	b[3].Close = 700
	b[3].Low = 698
	b[3].Settlement = 700
	z, err := BacktestFrom(config(), b, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Trades[0] != z.Trades[0] {
		t.Fatal("holdout first fill has look-ahead")
	}
}

func TestEvaluationStartBounds(t *testing.T) {
	for _, start := range []int{-1, 3, 4} {
		if _, err := BacktestFrom(config(), series(1000, 1100, 1200), start); err == nil {
			t.Fatal("invalid evaluation start")
		}
	}
}
