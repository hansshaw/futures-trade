package quant

import (
	"math"
	"strings"
	"testing"
	"time"

	"cornwatch/internal/domain"
)

func config() Config {
	return Config{Symbol: "c2701", FastWindow: 1, SlowWindow: 2, InitialCapital: 100000, Lots: 1, FeePerLot: 1.2, SlippageTicks: 1, MarginRate: 0.12, MaxDrawdownPct: 20}
}

func series(closes ...float64) []domain.Bar {
	start := time.Date(2026, 10, 1, 15, 0, 0, 0, china)
	bars := make([]domain.Bar, len(closes))
	for i, p := range closes {
		d := start.AddDate(0, 0, i)
		bars[i] = domain.Bar{Symbol: "c2701", Timestamp: d, TradeDate: d.Format("2006-01-02"), Open: p, High: p + 2, Low: p - 2, Close: p, Settlement: p, Volume: 10000, OpenInterest: 100000, LimitDown: 1, LimitUp: 5000}
	}
	return bars
}

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %.10f want %.10f", got, want)
	}
}
func warningContains(r Result, s string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w, s) {
			return true
		}
	}
	return false
}

func TestNextOpenAndExactCashAccounting(t *testing.T) {
	bars := series(1000, 1100, 1200, 1300)
	r, err := Backtest(config(), bars)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 2 || r.Trades[0].Timestamp != bars[2].Timestamp || r.Trades[0].Price != 1200 {
		t.Fatalf("unexpected fills: %+v", r.Trades)
	}
	near(t, r.Metrics.FinalEquity, 100977.6)
	near(t, r.Metrics.TotalFees, 2.4)
	near(t, r.Metrics.TotalSlippage, 20)
	if r.Metrics.TradeCount != 1 || r.Metrics.WinRatePct != 100 || r.Metrics.OpenPosition != 0 {
		t.Fatalf("metrics %+v", r.Metrics)
	}
	if r.EquityCurve[1].Position != 0 || r.EquityCurve[2].Position != 1 {
		t.Fatal("position precedes its next-open execution")
	}
	near(t, r.EquityCurve[2].Equity, 99988.8)
	sum := 0.0
	for _, f := range r.Trades {
		sum += f.PnL
	}
	near(t, r.Metrics.FinalEquity, config().InitialCapital+sum)
}

func TestCurrentAndFutureCloseCannotChangeOpeningOrder(t *testing.T) {
	a := series(1000, 1100, 1200, 1300, 1400)
	b := append([]domain.Bar{}, a...)
	b[2].Close = 900
	b[2].Low = 898
	b[2].Settlement = 900
	b[3].Close = 800
	b[3].Low = 798
	b[3].Settlement = 800
	ra, err := Backtest(config(), a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := Backtest(config(), b)
	if err != nil {
		t.Fatal(err)
	}
	if ra.Trades[0] != rb.Trades[0] {
		t.Fatal("same opening order changed in response to current/future close")
	}
	if ra.Trades[0].Timestamp != a[2].Timestamp {
		t.Fatal("look-ahead execution")
	}
}

func TestReversalsChargeEverySideAndCountRoundTrips(t *testing.T) {
	r, err := Backtest(config(), series(1000, 1100, 1000, 900, 1000))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 4 || r.Metrics.TradeCount != 2 {
		t.Fatalf("fills %d trips %d", len(r.Trades), r.Metrics.TradeCount)
	}
	near(t, r.Metrics.TotalFees, 4.8)
	near(t, r.Metrics.TotalSlippage, 40)
	near(t, r.Metrics.FinalEquity, 97955.2)
	near(t, r.Metrics.WinRatePct, 0)
}

// This ledger is intentionally hand-calculated independently of the engine:
// short 3 lots at 1000, cover at 900; gross 3*10*100 = 3000;
// each side's fee 3*2.5 = 7.5 and slip 3*10*2 = 60.
func TestIndependentThreeLotShortLedger(t *testing.T) {
	c := config()
	c.Lots, c.FeePerLot, c.SlippageTicks = 3, 2.5, 2
	r, err := Backtest(c, series(1200, 1100, 1000, 900))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 2 || r.Trades[0].Side != "sell" || r.Trades[1].Side != "buy" {
		t.Fatalf("bad short ledger fills: %+v", r.Trades)
	}
	near(t, r.EquityCurve[2].Equity, 99932.5)
	near(t, r.Trades[0].PnL, -67.5)
	near(t, r.Trades[1].PnL, 2932.5)
	near(t, r.Metrics.FinalEquity, 102865)
	near(t, r.Metrics.TotalFees, 15)
	near(t, r.Metrics.TotalSlippage, 120)
	near(t, r.Metrics.TotalReturnPct, 2.865)
	near(t, r.Metrics.MaxDrawdownPct, 0.0675)
	if r.Metrics.TradeCount != 1 || r.Metrics.WinRatePct != 100 {
		t.Fatalf("bad short round trip stats: %+v", r.Metrics)
	}
}

func TestWinRateUsesNetCosts(t *testing.T) {
	c := config()
	c.FeePerLot = 6
	c.SlippageTicks = 0
	r, err := Backtest(c, series(1000, 1100, 1000, 1001))
	if err != nil {
		t.Fatal(err)
	}
	// A +1 point long trade earns 10 gross, less than its 12 round-trip fee.
	if r.Trades[1].PnL <= 0 {
		t.Fatal("fixture needs positive close cash flow")
	}
	if r.Metrics.WinRatePct != 0 {
		t.Fatal("gross win incorrectly counted as net win")
	}
}

func TestOpenGapRiskFlattensAtOpenAndHalts(t *testing.T) {
	b := series(1000, 1100, 1200, 900, 1400)
	c := config()
	c.InitialCapital = 10000
	c.MaxDrawdownPct = 10
	r, err := Backtest(c, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 2 || r.Trades[1].Price != 900 || r.Trades[1].Reason != "risk_liquidation_at_open" {
		t.Fatalf("bad gap risk handling %+v", r.Trades)
	}
	near(t, r.Metrics.FinalEquity, 6977.6)
	if r.Metrics.MaxDrawdownPct < 30 {
		t.Fatal("gap loss understated")
	}
}

func TestInsufficientMarginRejectsOpening(t *testing.T) {
	c := config()
	c.InitialCapital = 1000
	r, err := Backtest(c, series(1000, 1100, 1200, 1300))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 0 {
		t.Fatal("opened without sufficient margin")
	}
	near(t, r.Metrics.FinalEquity, 1000)
}

func TestNoVolumeRejectsOpening(t *testing.T) {
	b := series(1000, 1100, 1200)
	b[2].Volume = 0
	r, err := Backtest(config(), b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 0 {
		t.Fatal("invented zero-volume fill")
	}
}

func TestLimitUpRejectsBuyEvenIfLaterUnlocked(t *testing.T) {
	b := series(1000, 1100, 1200)
	b[2].LimitUp = 1200
	b[2].High = 1200
	b[2].Close = 1199
	b[2].Settlement = 1199
	r, err := Backtest(config(), b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 0 {
		t.Fatal("buy at limit-up should be conservatively rejected without order-book evidence")
	}
}

func TestLockedFinalBarPreservesPositionAndWarns(t *testing.T) {
	b := series(1000, 1100, 1200, 900)
	b[3].High = 900
	b[3].Low = 900
	b[3].LimitDown = 900
	r, err := Backtest(config(), b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 1 || r.Metrics.OpenPosition != 1 {
		t.Fatalf("fabricated liquidation: %+v", r.Trades)
	}
	near(t, r.Metrics.TotalFees, 1.2)
	near(t, r.Metrics.TotalSlippage, 10)
	near(t, r.Metrics.UnrealizedPnL, -3000)
	near(t, r.Metrics.FinalEquity, 96988.8)
	if !warningContains(r, "仍有未平持仓") {
		t.Fatal("missing unresolved-position disclosure")
	}
}

func TestZeroVolumeFinalBarPreservesPosition(t *testing.T) {
	b := series(1000, 1100, 1200, 1300)
	b[3].Volume = 0
	r, err := Backtest(config(), b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 1 || r.Metrics.OpenPosition != 1 {
		t.Fatal("fabricated zero-volume terminal close")
	}
}

func TestDailyVolumeBudgetIncludesBothSides(t *testing.T) {
	b := series(1000, 1100, 1200)
	b[2].Volume = 1
	r, err := Backtest(config(), b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trades) != 1 || r.Metrics.OpenPosition != 1 {
		t.Fatal("reused a single day's volume for two fills")
	}
}

func TestMissingLimitsDisclosed(t *testing.T) {
	b := series(1000, 1100, 1200)
	for i := range b {
		b[i].LimitUp = 0
		b[i].LimitDown = 0
	}
	r, err := Backtest(config(), b)
	if err != nil {
		t.Fatal(err)
	}
	if !warningContains(r, "缺少涨跌停价") {
		t.Fatal("missing unknown-liquidity warning")
	}
}

func TestBacktestRejectsWrongModeAndContracts(t *testing.T) {
	b := series(1000, 1100, 1200)
	b[1].Symbol = "c2705"
	if _, err := Backtest(config(), b); err == nil {
		t.Fatal("accepted mixed contracts")
	}
	b = series(1000, 1100, 1200)
	for i := range b {
		b[i].IsDemo = true
	}
	if _, err := Backtest(config(), b); err == nil {
		t.Fatal("accepted mismatched demo flag")
	}
	c := config()
	c.Demo = true
	r, err := Backtest(c, b)
	if err != nil || !r.IsDemo {
		t.Fatal("matching demo input failed")
	}
}

func TestInvalidBars(t *testing.T) {
	cases := map[string]func([]domain.Bar){
		"nan":                     func(b []domain.Bar) { b[0].Close = math.NaN() },
		"infinity":                func(b []domain.Bar) { b[0].Settlement = math.Inf(1) },
		"bad range":               func(b []domain.Bar) { b[0].High = b[0].Low - 1 },
		"bad tick":                func(b []domain.Bar) { b[0].Open += 0.5 },
		"negative volume":         func(b []domain.Bar) { b[0].Volume = -1 },
		"one limit only":          func(b []domain.Bar) { b[0].LimitDown = 0 },
		"limit exceeded":          func(b []domain.Bar) { b[0].LimitUp = b[0].High - 1 },
		"duplicate date":          func(b []domain.Bar) { b[1].TradeDate = b[0].TradeDate; b[1].Timestamp = b[0].Timestamp.Add(time.Hour) },
		"non increasing":          func(b []domain.Bar) { b[0], b[1] = b[1], b[0] },
		"mixed demo":              func(b []domain.Bar) { b[1].IsDemo = true },
		"invalid symbol":          func(b []domain.Bar) { b[0].Symbol = "c2713" },
		"unlisted delivery month": func(b []domain.Bar) { b[0].Symbol = "c2702" },
		"date mismatch":           func(b []domain.Bar) { b[0].TradeDate = "2026-09-30" },
		"expired":                 func(b []domain.Bar) { b[0].Symbol = "c2601" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := series(1000, 1100, 1200)
			mutate(b)
			if err := ValidateBars(b); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
	if ValidateBars(nil) == nil {
		t.Fatal("accepted empty bars")
	}
	if ValidateBars(make([]domain.Bar, MaxBars+1)) == nil {
		t.Fatal("accepted too many bars")
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []func(*Config){func(c *Config) { c.FastWindow = 0 }, func(c *Config) { c.SlowWindow = 1 }, func(c *Config) { c.InitialCapital = math.NaN() }, func(c *Config) { c.MarginRate = 0 }, func(c *Config) { c.FeePerLot = -1 }, func(c *Config) { c.SlippageTicks = -1 }, func(c *Config) { c.MaxDrawdownPct = 101 }, func(c *Config) { c.Lots = 0 }}
	for _, mutate := range cases {
		c := config()
		mutate(&c)
		if _, err := Backtest(c, series(1000, 1100, 1200)); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
}
