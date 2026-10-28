package research

import (
	"math"
	"testing"
)

func riskConfig() RiskConfig {
	return RiskConfig{Equity: 100000, Entry: 2200, Stop: 2180, Direction: "long", RiskPct: 1, MarginRate: .12, MaxMarginPct: 40, FeePerLot: 1.2, SlippageTicks: 1}
}

func TestRiskSizingIndependentHandCalculation(t *testing.T) {
	r, err := SizeRisk(riskConfig())
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, r.RiskBudget, 1000)
	closeTo(t, r.LossPerLot, 222.4) // 20*10 + 2*(1.2+1*10)
	closeTo(t, r.MarginPerLot, 2640)
	if r.RiskLots != 4 || r.MarginLots != 15 || r.MaxLots != 4 {
		t.Fatalf("bad limits %+v", r)
	}
	closeTo(t, r.PlannedLoss, 889.6)
	closeTo(t, r.MarginUsed, 10560)
	closeTo(t, r.Notional, 88000)
	closeTo(t, r.Leverage, .88)
	c := riskConfig()
	c.Direction = "short"
	c.Stop = 2220
	s, err := SizeRisk(c)
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, s.LossPerLot, r.LossPerLot)
}

func TestRiskUsesBothBudgetsAndFloor(t *testing.T) {
	c := riskConfig()
	c.MaxMarginPct = 5
	r, err := SizeRisk(c)
	if err != nil {
		t.Fatal(err)
	}
	if r.MaxLots != 1 || r.MarginLots != 1 {
		t.Fatalf("margin not binding %+v", r)
	}
	c = riskConfig()
	c.Equity = 22240
	r, err = SizeRisk(c)
	if err != nil {
		t.Fatal(err)
	}
	if r.RiskLots != 1 {
		t.Fatal("exact risk budget should permit one lot")
	}
	c.Equity = 22239
	r, err = SizeRisk(c)
	if err != nil {
		t.Fatal(err)
	}
	if r.MaxLots != 0 || r.PlannedLoss != 0 {
		t.Fatal("rounded up beyond risk budget")
	}
}

func TestRiskOpeningCostCannotConsumeMargin(t *testing.T) {
	c := riskConfig()
	c.Equity = 2640
	c.RiskPct = 100
	c.MaxMarginPct = 100
	r, err := SizeRisk(c)
	if err != nil {
		t.Fatal(err)
	}
	if r.MarginLots != 0 || r.MaxLots != 0 {
		t.Fatal("all cash allocated to margin without paying open cost")
	}
	c.FeePerLot = 0
	c.SlippageTicks = 0
	r, err = SizeRisk(c)
	if err != nil {
		t.Fatal(err)
	}
	if r.MaxLots != 1 {
		t.Fatal("zero-cost exact margin budget rejected")
	}
}

func TestRiskInvalidInputs(t *testing.T) {
	cases := map[string]func(*RiskConfig){"zero equity": func(c *RiskConfig) { c.Equity = 0 }, "infinite": func(c *RiskConfig) { c.Equity = math.Inf(1) }, "entry tick": func(c *RiskConfig) { c.Entry = 2200.5 }, "nan": func(c *RiskConfig) { c.Stop = math.NaN() }, "long stop": func(c *RiskConfig) { c.Stop = 2200 }, "short stop": func(c *RiskConfig) { c.Direction = "short" }, "direction": func(c *RiskConfig) { c.Direction = "buy" }, "risk": func(c *RiskConfig) { c.RiskPct = 101 }, "margin": func(c *RiskConfig) { c.MarginRate = 0 }, "budget": func(c *RiskConfig) { c.MaxMarginPct = 0 }, "fee": func(c *RiskConfig) { c.FeePerLot = -1 }, "slip": func(c *RiskConfig) { c.SlippageTicks = -1 }}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := riskConfig()
			mutate(&c)
			if _, err := SizeRisk(c); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestTinyMarginRateDoesNotOverflowLotCount(t *testing.T) {
	c := riskConfig()
	c.MarginRate = math.SmallestNonzeroFloat64
	c.FeePerLot = 0
	c.SlippageTicks = 0
	r, err := SizeRisk(c)
	if err != nil {
		t.Fatal(err)
	}
	if r.MaxLots != 5 || r.MarginLots < r.MaxLots {
		t.Fatalf("overflowed lot bound %+v", r)
	}
}
