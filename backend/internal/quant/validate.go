// Package quant implements deliberately conservative, daily-bar research tools.
// It does not place orders or reconstruct exchange order-book liquidity.
package quant

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"cornwatch/internal/domain"
)

const MaxBars = 200000

// The current DCE corn product specification lists odd delivery months only.
var cornSymbol = regexp.MustCompile(`^c[0-9]{2}(0[13579]|11)$`)
var china = time.FixedZone("Asia/Shanghai", 8*60*60)

func maturity(symbol string) int {
	if !cornSymbol.MatchString(symbol) {
		return 0
	}
	year, _ := strconv.Atoi(symbol[1:3])
	month, _ := strconv.Atoi(symbol[3:5])
	return (2000+year)*100 + month
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func validateBar(b domain.Bar) error {
	if maturity(b.Symbol) == 0 {
		return fmt.Errorf("symbol must be a single corn contract cYYMM: %q", b.Symbol)
	}
	d, err := time.Parse("2006-01-02", b.TradeDate)
	if err != nil || d.Format("2006-01-02") != b.TradeDate {
		return fmt.Errorf("invalid trade_date %q", b.TradeDate)
	}
	if b.Timestamp.IsZero() || b.Timestamp.In(china).Format("2006-01-02") != b.TradeDate {
		return fmt.Errorf("daily bar end timestamp must fall on trade_date in Asia/Shanghai")
	}
	if d.Year()*100+int(d.Month()) > maturity(b.Symbol) {
		return fmt.Errorf("bar is after contract delivery month")
	}
	for name, p := range map[string]float64{"open": b.Open, "high": b.High, "low": b.Low, "close": b.Close, "settlement": b.Settlement} {
		if !finite(p) || p <= 0 || p > 1e9 {
			return fmt.Errorf("%s must be finite and positive", name)
		}
		if name != "settlement" && math.Abs(p-math.Round(p)) > 1e-8 {
			return fmt.Errorf("%s must align to corn's 1 yuan/tick", name)
		}
	}
	if b.Low > b.High || b.Open < b.Low || b.Open > b.High || b.Close < b.Low || b.Close > b.High {
		return fmt.Errorf("invalid OHLC range")
	}
	if b.Volume < 0 || b.OpenInterest < 0 {
		return fmt.Errorf("volume and open_interest cannot be negative")
	}
	if !finite(b.LimitUp) || !finite(b.LimitDown) {
		return fmt.Errorf("non-finite price limits")
	}
	if b.LimitUp == 0 && b.LimitDown == 0 {
		return nil
	}
	if b.LimitDown <= 0 || b.LimitUp <= b.LimitDown || math.Abs(b.LimitUp-math.Round(b.LimitUp)) > 1e-8 || math.Abs(b.LimitDown-math.Round(b.LimitDown)) > 1e-8 {
		return fmt.Errorf("limits must both be zero (unknown) or ordered positive ticks")
	}
	if b.Low < b.LimitDown || b.High > b.LimitUp || b.Settlement < b.LimitDown || b.Settlement > b.LimitUp {
		return fmt.Errorf("bar exceeds declared daily price limits")
	}
	return nil
}

// ValidateBars validates daily records without sorting or repairing them. Multiple
// contracts are accepted for imports, but each contract must have strictly
// increasing timestamps and exactly one record per increasing trade_date.
func ValidateBars(bars []domain.Bar) error {
	if len(bars) == 0 {
		return fmt.Errorf("bars cannot be empty")
	}
	if len(bars) > MaxBars {
		return fmt.Errorf("at most %d bars are accepted", MaxBars)
	}
	last := make(map[string]domain.Bar)
	for i, b := range bars {
		if err := validateBar(b); err != nil {
			return fmt.Errorf("bar %d (%s): %w", i+1, b.Symbol, err)
		}
		if p, ok := last[b.Symbol]; ok {
			if !b.Timestamp.After(p.Timestamp) || b.TradeDate <= p.TradeDate {
				return fmt.Errorf("bar %d: timestamps and trade_dates must strictly increase per contract", i+1)
			}
			if p.IsDemo != b.IsDemo {
				return fmt.Errorf("bar %d: demo and imported records cannot be mixed", i+1)
			}
		}
		last[b.Symbol] = b
	}
	return nil
}
