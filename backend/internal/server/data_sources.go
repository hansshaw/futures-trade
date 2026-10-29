package server

import (
	"cornwatch/internal/datahub"
	"cornwatch/internal/domain"
	"cornwatch/internal/market"
	"sort"
	"time"
)

// Real snapshot cards deliberately do not fill gaps with demo/CSV prices.
func liveCards(live datahub.Live) []domain.Quote {
	result := []domain.Quote{}
	main := live.Main.Decision.Symbol
	if main == "" {
		main = live.Main.ProvisionalSymbol
	}
	for _, product := range []string{"c", "cs", "lh", "m"} {
		choices := []market.Quote{}
		for _, q := range live.Quotes {
			if q.Product == product {
				choices = append(choices, q)
			}
		}
		sort.Slice(choices, func(i, j int) bool {
			if product == "c" && (choices[i].Symbol == main || choices[j].Symbol == main) {
				return choices[i].Symbol == main
			}
			if choices[i].TradeDate != choices[j].TradeDate {
				return choices[i].TradeDate > choices[j].TradeDate
			}
			if choices[i].Volume != choices[j].Volume {
				return choices[i].Volume > choices[j].Volume
			}
			return choices[i].OpenInterest > choices[j].OpenInterest
		})
		if len(choices) == 0 {
			continue
		}
		q := choices[0]
		freshness, note := live.Freshness, live.FreshnessNote
		if live.LatestDataAt != nil && live.LatestDataAt.Sub(q.ObservedAt) > 5*time.Minute {
			freshness = "stale"
			note = "该合约报价时间落后于本批其他合约，请核验单合约状态"
		}
		result = append(result, domain.Quote{Product: q.Product, Symbol: q.Symbol, Name: q.Name, Last: q.Last, PreviousSettlement: q.PreviousSettlement, ChangePct: q.ChangePct, ChangeBasis: q.ChangeBasis, Volume: q.Volume, OpenInterest: q.OpenInterest, ObservedAt: q.ObservedAt, ReceivedAt: q.ReceivedAt, Source: q.Source, SourceURL: q.SourceURL, Freshness: freshness, FreshnessNote: note})
	}
	return result
}
