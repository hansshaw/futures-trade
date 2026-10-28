package quant

import (
	"fmt"
	"math"
	"testing"
	"time"

	"cornwatch/internal/domain"
)

// Synthetic but nonconstant, validated daily inputs. No benchmark measures an
// empty, failing, or mocked execution path. This is not historical market data.
func benchmarkBars(n int) []domain.Bar {
	bars := make([]domain.Bar, 0, n)
	day := time.Date(2026, 1, 1, 15, 0, 0, 0, china)
	for len(bars) < n {
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			i := len(bars)
			price := 2200 + math.Round(80*math.Sin(float64(i)/8)+20*math.Cos(float64(i)/3))
			bars = append(bars, domain.Bar{Symbol: "c2701", Timestamp: day, TradeDate: day.Format("2006-01-02"), Open: price, High: price + 10, Low: price - 10, Close: price + math.Round(5*math.Sin(float64(i))), Settlement: price, Volume: 200000 + int64(i*137), OpenInterest: 300000, LimitUp: 2500, LimitDown: 1900, IsDemo: true})
		}
		day = day.AddDate(0, 0, 1)
	}
	return bars
}

func BenchmarkBacktest(b *testing.B) {
	bars := benchmarkBars(240)
	c := config()
	c.Demo = true
	c.FastWindow = 5
	c.SlowWindow = 20
	if _, err := Backtest(c, bars); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Backtest(c, bars); err != nil {
			b.Fatal(err)
		}
	}
}

// 1,200 observed weekdays, six odd-month corn delivery contracts per date.
// Each contract expires within roughly the next year; leading volume and OI
// migrate with maturity, exercising initial selection and repeated rolls.
func benchmarkUniverse() []domain.Bar {
	rows := make([]domain.Bar, 0, 7200)
	day := time.Date(2021, 1, 4, 15, 0, 0, 0, china)
	for count := 0; count < 1200; day = day.AddDate(0, 0, 1) {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		count++
		listed := 0
		for months := 1; listed < 6; months++ {
			expiry := time.Date(day.Year(), day.Month()+time.Month(months), 1, 0, 0, 0, 0, china)
			if int(expiry.Month())%2 == 0 {
				continue
			}
			listed++
			symbol := fmt.Sprintf("c%02d%02d", expiry.Year()%100, expiry.Month())
			liquidity := int64(1000000 / (1 + int(math.Abs(float64(months)-4))))
			rows = append(rows, domain.Bar{Symbol: symbol, Timestamp: day, TradeDate: day.Format("2006-01-02"), Open: 2200, High: 2220, Low: 2180, Close: 2205, Settlement: 2203, Volume: liquidity, OpenInterest: liquidity * 2, LimitUp: 2400, LimitDown: 2000, IsDemo: true})
		}
	}
	return rows
}

func BenchmarkResolveMain(b *testing.B) {
	bars := benchmarkUniverse()
	if err := ValidateBars(bars); err != nil {
		b.Fatal(err)
	}
	asOf := bars[len(bars)-1].Timestamp
	if r := ResolveMain(bars, asOf); r.Symbol == "" {
		b.Fatal("benchmark dataset did not produce a main selection")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ResolveMain(bars, asOf)
	}
}
