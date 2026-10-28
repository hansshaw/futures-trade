package quant

import (
	"fmt"
	"sort"
	"time"

	"cornwatch/internal/domain"
)

const confirmationDays = 2

// ResolveMain reconstructs a research main-contract decision from the history
// available by asOf. This is not an exchange-provided official main symbol.
// The first selection requires two comparable contracts and the same volume
// AND open-interest leader on two consecutive observed trading dates. A roll
// requires both volume and open interest to exceed the incumbent by >10% on
// two dates, and can only move to a later delivery month. Delivery-month
// contracts are excluded. Missing incumbent records interrupt confirmation.
func ResolveMain(bars []domain.Bar, asOf time.Time) domain.MainDecision {
	r := domain.MainDecision{AsOf: asOf, RequiredDays: confirmationDays, Reason: "数据不足：需要至少两个可比较合约的连续两个交易日数据。"}
	if asOf.IsZero() || len(bars) > MaxBars {
		r.Reason = "as_of 无效或数据超过限制。"
		return r
	}
	byDate := map[string]map[string]domain.Bar{}
	modeSet, mode := false, false
	for _, b := range bars {
		if b.Timestamp.After(asOf) || b.TradeDate > asOf.In(china).Format("2006-01-02") {
			continue
		}
		if validateBar(b) != nil {
			continue
		}
		if modeSet && b.IsDemo != mode {
			r.Reason = "拒绝混合演示与导入数据计算主力。"
			return r
		}
		modeSet, mode = true, b.IsDemo
		if byDate[b.TradeDate] == nil {
			byDate[b.TradeDate] = map[string]domain.Bar{}
		}
		// Repeated records on one trade date never count as multiple days.
		old, ok := byDate[b.TradeDate][b.Symbol]
		if !ok || b.Timestamp.After(old.Timestamp) {
			byDate[b.TradeDate][b.Symbol] = b
		}
	}
	r.IsDemo = mode
	dates := make([]string, 0, len(byDate))
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	current, previous, pending := "", "", ""
	days, furthest := 0, 0
	reset := func() { pending, days = "", 0 }
	confirm := func(symbol string) {
		if pending != symbol {
			pending, days = symbol, 1
		} else {
			days++
		}
		if days >= confirmationDays {
			if current != "" {
				previous = current
			}
			current = symbol
			furthest = maturity(symbol)
			reset()
		}
	}
	for _, date := range dates {
		d, _ := time.Parse("2006-01-02", date)
		month := d.Year()*100 + int(d.Month())
		if current != "" && maturity(current) <= month {
			previous, current = current, ""
			reset()
		}
		eligible := make([]domain.Bar, 0, len(byDate[date]))
		for _, b := range byDate[date] {
			if maturity(b.Symbol) > month && b.Volume > 0 && b.OpenInterest > 0 {
				eligible = append(eligible, b)
			}
		}
		sort.Slice(eligible, func(i, j int) bool {
			if eligible[i].Volume != eligible[j].Volume {
				return eligible[i].Volume > eligible[j].Volume
			}
			if eligible[i].OpenInterest != eligible[j].OpenInterest {
				return eligible[i].OpenInterest > eligible[j].OpenInterest
			}
			return maturity(eligible[i].Symbol) < maturity(eligible[j].Symbol)
		})
		if current == "" {
			if len(eligible) < 2 {
				reset()
				continue
			}
			leader := eligible[0]
			if maturity(leader.Symbol) <= furthest {
				reset()
				continue
			}
			unambiguous := true
			for _, b := range eligible[1:] {
				if b.OpenInterest > leader.OpenInterest || (b.Volume == leader.Volume && b.OpenInterest == leader.OpenInterest) {
					unambiguous = false
					break
				}
			}
			if !unambiguous {
				reset()
				continue
			}
			confirm(leader.Symbol)
			continue
		}
		incumbent, ok := byDate[date][current]
		if !ok || incumbent.Volume <= 0 || incumbent.OpenInterest <= 0 {
			reset()
			continue
		}
		challenger := ""
		for _, b := range eligible {
			if maturity(b.Symbol) > furthest && float64(b.Volume) > float64(incumbent.Volume)*1.1 && float64(b.OpenInterest) > float64(incumbent.OpenInterest)*1.1 {
				challenger = b.Symbol
				break
			}
		}
		if challenger == "" {
			reset()
			continue
		}
		confirm(challenger)
	}
	// A stale historical selection must not be presented as today's eligible
	// main when the contract has reached delivery month. No synthetic roll.
	nowMonth := asOf.In(china).Year()*100 + int(asOf.In(china).Month())
	if current != "" && maturity(current) <= nowMonth {
		previous, current = current, ""
		reset()
	}
	if pending != "" && maturity(pending) <= nowMonth {
		reset()
	}
	r.Symbol, r.PreviousSymbol, r.PendingSymbol, r.ConfirmationDays = current, previous, pending, days
	if current != "" {
		r.Reason = "研究主力：连续2个有数据交易日确认；换月要求远月成交量及持仓量均超过原合约10%，仅向远月切换，排除交割月。"
		if len(dates) > 0 {
			r.Reason += " 最近可用交易日：" + dates[len(dates)-1] + "。"
			latest, ok := byDate[dates[len(dates)-1]][current]
			if !ok || latest.Volume <= 0 || latest.OpenInterest <= 0 {
				r.Reason += " 最近交易日缺少原主力有效量仓数据，沿用历史已确认合约，未新确认换月。"
			}
		}
	} else if previous != "" {
		r.Reason = "原合约已进入交割月或到期；可用数据不足以确认远月主力，未按日历强行换月。"
	}
	if pending != "" {
		r.Reason += fmt.Sprintf(" 待确认 %s（%d/%d 个交易日）。", pending, days, confirmationDays)
	}
	return r
}
