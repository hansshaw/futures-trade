package market

import (
	"fmt"
	"sort"
	"time"
)

// Update advances a research main-contract state using quotes received now.
// It observes real calendar dates, not an invented exchange holiday calendar.
// Closed-day data remain displayable but can never advance today's decision.
// Call only after a successful fresh source fetch. For a bridge the caller must
// validate generated_at and worker health: unchanged quotes legitimately keep
// their original received_at after the market closes.
func Update(state State, quotes []Quote, now time.Time) State {
	// Copy slices so the caller's persisted previous value remains unchanged.
	state.History = append([]RollEvent(nil), state.History...)
	state.Snapshots = append([]CloseSnapshot(nil), state.Snapshots...)
	d := &state.Decision
	d.AsOf, d.RequiredDays, d.IsDemo = now, 2, false
	if state.AlgorithmVersion != "" && state.AlgorithmVersion != AlgorithmVersion {
		d.PendingSymbol, d.ConfirmationDays = "", 0
		state.LastEvaluatedDate = ""
	}
	state.AlgorithmVersion = AlgorithmVersion
	block := func(status, reason string) State { state.Status, d.Reason = status, reason; return state }
	if now.IsZero() {
		return block("blocked", "当前时间无效，未推进主力确认。")
	}
	local := now.In(china)
	today := local.Format("2006-01-02")
	month := local.Year()*100 + int(local.Month())
	furthest := 0
	for _, e := range state.History {
		_, maturity, _ := contract(e.NewSymbol)
		if maturity > furthest {
			furthest = maturity
		}
	}
	if d.Symbol != "" {
		_, maturity, ok := contract(d.Symbol)
		if maturity > furthest {
			furthest = maturity
		}
		if !ok || maturity <= month {
			d.PreviousSymbol, d.Symbol, d.PendingSymbol, d.ConfirmationDays = d.Symbol, "", "", 0
		}
	}
	if d.PendingSymbol != "" {
		_, maturity, _ := contract(d.PendingSymbol)
		if maturity <= month {
			d.PendingSymbol, d.ConfirmationDays = "", 0
		}
	}
	state.ProvisionalSymbol = ""
	minute := local.Hour()*60 + local.Minute()
	closeWindow := minute >= 15*60+15 && minute < 20*60+55
	eligible := make([]Quote, 0, len(quotes))
	seen := map[string]bool{}
	source := ""
	var latest time.Time
	for _, q := range quotes {
		product, maturity, ok := contract(q.Symbol)
		if !ok || product != "c" || q.Product != "c" || q.Exchange != "DCE" || maturity <= month {
			continue
		}
		if seen[q.Symbol] {
			return block("blocked", "实际玉米合约记录重复，未推进主力确认。")
		}
		seen[q.Symbol] = true
		if q.Source == "" || q.ObservedAt.IsZero() || q.ReceivedAt.IsZero() || q.ObservedAt.After(now.Add(2*time.Minute)) || q.ReceivedAt.After(now.Add(2*time.Minute)) || q.ReceivedAt.Before(q.ObservedAt.Add(-2*time.Minute)) || q.ReceivedAt.In(china).Format("2006-01-02") != today || q.TradeDate != today || q.ObservedAt.In(china).Format("2006-01-02") != today || q.Volume <= 0 || q.OpenInterest <= 0 || !finite(q.Last) || q.Last <= 0 {
			continue
		}
		qt := q.ObservedAt.In(china)
		qm := qt.Hour()*60 + qt.Minute()
		if closeWindow {
			if qm < 15*60 || qm > 15*60+20 {
				continue
			}
		} else if now.Sub(q.ObservedAt) > 10*time.Minute || now.Sub(q.ReceivedAt) > 5*time.Minute {
			continue
		}
		if source != "" && source != q.Source {
			return block("blocked", "拒绝混合不同来源的快照确认主力。")
		}
		source = q.Source
		eligible = append(eligible, q)
		if q.ObservedAt.After(latest) {
			latest = q.ObservedAt
		}
	}
	if len(eligible) < 2 {
		return block("stale", "缺少当日、同时段、同来源的至少两个有效玉米实际合约；沿用已确认记录，陈旧或缺失报价不推进确认。")
	}
	if !closeWindow {
		// Compare intraday observations only when their timestamps are close.
		filtered := eligible[:0]
		for _, q := range eligible {
			if latest.Sub(q.ObservedAt) <= 2*time.Minute {
				filtered = append(filtered, q)
			}
		}
		eligible = filtered
		if len(eligible) < 2 {
			return block("stale", "盘中合约快照时间不一致，仅展示已确认主力。")
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].Volume != eligible[j].Volume {
			return eligible[i].Volume > eligible[j].Volume
		}
		if eligible[i].OpenInterest != eligible[j].OpenInterest {
			return eligible[i].OpenInterest > eligible[j].OpenInterest
		}
		return eligible[i].Symbol < eligible[j].Symbol
	})
	candidate, reason := "", "成交量与持仓量尚未同时满足候选条件。"
	if d.Symbol == "" {
		leader := eligible[0]
		_, maturity, _ := contract(leader.Symbol)
		unambiguous := maturity > furthest
		for _, q := range eligible[1:] {
			if q.OpenInterest > leader.OpenInterest || (q.Volume == leader.Volume && q.OpenInterest == leader.OpenInterest) {
				unambiguous = false
			}
		}
		if unambiguous {
			candidate, reason = leader.Symbol, "初始候选在可比较实际合约中成交量、持仓量同时居前。"
		}
	} else {
		var incumbent *Quote
		for i := range eligible {
			if eligible[i].Symbol == d.Symbol {
				incumbent = &eligible[i]
				break
			}
		}
		if incumbent == nil {
			reason = "当日缺少原主力可比较快照，确认中断。"
		} else {
			for _, q := range eligible {
				_, maturity, _ := contract(q.Symbol)
				if maturity > furthest && float64(q.Volume) > float64(incumbent.Volume)*1.1 && float64(q.OpenInterest) > float64(incumbent.OpenInterest)*1.1 {
					candidate, reason = q.Symbol, "远月合约成交量及持仓量均严格超过原主力 10%。"
					break
				}
			}
		}
	}
	state.ProvisionalSymbol = candidate
	if !closeWindow {
		return block("observing", "当前为盘中或非收盘确认时段，仅更新候选，不累计确认天数。"+reason)
	}
	if today <= state.LastEvaluatedDate {
		return block("observed", "本收盘日期已评估，重复刷新不增加确认天数；保留当天首次成熟快照的审计记录。")
	}
	state.LastEvaluatedDate = today
	if candidate == "" {
		d.PendingSymbol, d.ConfirmationDays = "", 0
	} else if d.PendingSymbol == candidate {
		d.ConfirmationDays++
	} else {
		d.PendingSymbol, d.ConfirmationDays = candidate, 1
	}
	state.Snapshots = append(state.Snapshots, CloseSnapshot{TradeDate: today, Candidate: candidate, Accepted: candidate != "", Reason: reason, Quotes: append([]Quote(nil), eligible...), EvaluatedAt: now})
	if len(state.Snapshots) > 64 {
		state.Snapshots = state.Snapshots[len(state.Snapshots)-64:]
	}
	if candidate != "" && d.ConfirmationDays >= 2 {
		old := d.Symbol
		d.PreviousSymbol, d.Symbol, d.PendingSymbol, d.ConfirmationDays = old, candidate, "", 0
		state.History = append(state.History, RollEvent{OldSymbol: old, NewSymbol: candidate, EffectiveAt: now, TradeDate: today, ObservedAt: latest, Basis: reason + "连续两个已观察到的不同收盘日期确认；决策仅从此采集时刻生效。"})
		if len(state.History) > 100 {
			state.History = state.History[len(state.History)-100:]
		}
		return block("confirmed", "研究主力已由连续两个已观察收盘日期确认；这不是交易所或行情商发布的主力认定。")
	}
	if candidate == "" {
		return block("observed", reason+"本收盘日期未确认换月。")
	}
	return block("pending", fmt.Sprintf("%s 候选 %s，收盘确认 %d/2；不能用同日刷新替代另一个收盘日期。", reason, candidate, d.ConfirmationDays))
}
