package research

import (
	"fmt"
	"math"
)

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func lotsFloor(v float64) int {
	maxInt := int(^uint(0) >> 1)
	if math.IsInf(v, 1) || v >= float64(maxInt) {
		return maxInt
	}
	return int(math.Floor(v))
}

// SizeRisk gives an account-equity budget, not an exchange-approved order size.
// Corn: 10 tonnes/lot, 1 yuan/tonne/tick. Costs are charged on both sides.
func SizeRisk(c RiskConfig) (RiskResult, error) {
	if !finite(c.Equity) || c.Equity <= 0 || c.Equity > 1e12 {
		return RiskResult{}, fmt.Errorf("equity must be finite and in (0, 1e12]")
	}
	for name, v := range map[string]float64{"entry": c.Entry, "stop": c.Stop} {
		if !finite(v) || v <= 0 || v > 1e9 || math.Round(v) < 1 || math.Abs(v-math.Round(v)) > 1e-8 {
			return RiskResult{}, fmt.Errorf("%s must be positive and align to corn's 1 yuan tick", name)
		}
	}
	if (c.Direction != "long" && c.Direction != "short") || (c.Direction == "long" && c.Stop >= c.Entry) || (c.Direction == "short" && c.Stop <= c.Entry) {
		return RiskResult{}, fmt.Errorf("stop must be below long entry or above short entry")
	}
	if !finite(c.RiskPct) || c.RiskPct <= 0 || c.RiskPct > 100 {
		return RiskResult{}, fmt.Errorf("risk_pct must be in (0,100], with 1 meaning 1%%")
	}
	if !finite(c.MarginRate) || c.MarginRate <= 0 || c.MarginRate > 1 {
		return RiskResult{}, fmt.Errorf("margin_rate must be in (0,1]")
	}
	if !finite(c.MaxMarginPct) || c.MaxMarginPct <= 0 || c.MaxMarginPct > 100 {
		return RiskResult{}, fmt.Errorf("max_margin_pct must be in (0,100]")
	}
	if !finite(c.FeePerLot) || c.FeePerLot < 0 || c.FeePerLot > 1e6 || c.SlippageTicks < 0 || c.SlippageTicks > 10000 {
		return RiskResult{}, fmt.Errorf("fee_per_lot must be in [0,1e6] and slippage_ticks in [0,10000]")
	}
	cost := 2 * (c.FeePerLot + float64(c.SlippageTicks)*10)
	r := RiskResult{RiskBudget: c.Equity * c.RiskPct / 100, LossPerLot: math.Abs(c.Entry-c.Stop)*10 + cost, MarginPerLot: c.Entry * 10 * c.MarginRate, Warnings: []string{}}
	r.RiskLots = lotsFloor(r.RiskBudget / r.LossPerLot)
	r.MarginLots = lotsFloor((c.Equity * c.MaxMarginPct / 100) / r.MarginPerLot)
	// Even a 100% margin allocation must leave cash for opening fees/slippage.
	cashLots := lotsFloor(c.Equity / (r.MarginPerLot + cost/2))
	if cashLots < r.MarginLots {
		r.MarginLots = cashLots
		r.Warnings = append(r.Warnings, "保证金手数已同时受扣除开仓费用和滑点后可用权益约束。")
	}
	r.MaxLots = min(r.RiskLots, r.MarginLots)
	r.PlannedLoss = float64(r.MaxLots) * r.LossPerLot
	r.MarginUsed = float64(r.MaxLots) * r.MarginPerLot
	r.Notional = float64(r.MaxLots) * c.Entry * 10
	r.Leverage = r.Notional / c.Equity
	r.Warnings = append(r.Warnings, "预计止损损失包含双边手续费和滑点；止损价格不保证成交，跳空、涨跌停或流动性不足可能使实际亏损超过预算。")
	r.Warnings = append(r.Warnings, "此处按全账户权益估算一个独立计划，尚未扣除已有仓位占用、挂单保证金或关联风险；多计划不能分别使用同一份预算。保证金率和费用应使用期货公司的当前有效值。")
	if r.MaxLots == 0 {
		r.Warnings = append(r.Warnings, "当前风险或保证金预算不足1手，计划仓位为0；不要为凑够1手而缩小结构性止损。")
	}
	if c.RiskPct > 2 {
		r.Warnings = append(r.Warnings, "单次计划风险超过账户权益的2%；这是风险暴露提示，不代表2%是适合所有人的安全阈值。")
	}
	if c.MaxMarginPct > 50 {
		r.Warnings = append(r.Warnings, "保证金占用上限超过权益的50%，需为波动和保证金上调预留现金。")
	}
	if r.MaxLots > 2000 {
		r.Warnings = append(r.Warnings, "输出是总风险预算手数，不是可直接发送的单笔委托；还须核验当前单笔委托上限、持仓限额及市场容量。")
	}
	return r, nil
}
