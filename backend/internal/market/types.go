// Package market collects public research quotes. It does not place orders or
// turn mutable quote snapshots into historical bars.
package market

import (
	"time"

	"cornwatch/internal/domain"
)

const SourceName = "新浪公开行情（研究参考，无 SLA）"
const AlgorithmVersion = "observed-close-main-v1"

type Quote struct {
	Symbol             string    `json:"symbol"`
	Product            string    `json:"product"`
	Name               string    `json:"name"`
	Exchange           string    `json:"exchange"`
	TradeDate          string    `json:"trade_date"` // Raw source date; not an inferred night-session trading date.
	ObservedAt         time.Time `json:"observed_at"`
	ReceivedAt         time.Time `json:"received_at"`
	Source             string    `json:"source"`
	SourceURL          string    `json:"source_url"`
	Last               float64   `json:"last"`
	Open               float64   `json:"open"`
	High               float64   `json:"high"`
	Low                float64   `json:"low"`
	PreviousSettlement float64   `json:"previous_settlement"`
	ChangePct          float64   `json:"change_pct"`
	ChangeBasis        string    `json:"change_basis"`
	Volume             int64     `json:"volume"`
	OpenInterest       int64     `json:"open_interest"`
}

type RollEvent struct {
	OldSymbol   string    `json:"old_symbol"`
	NewSymbol   string    `json:"new_symbol"`
	EffectiveAt time.Time `json:"effective_at"` // Actual research decision time, not a fictional exchange opening time.
	TradeDate   string    `json:"trade_date"`
	Basis       string    `json:"basis"`
	ObservedAt  time.Time `json:"observed_at"`
}

type CloseSnapshot struct {
	TradeDate   string    `json:"trade_date"`
	Candidate   string    `json:"candidate"`
	Accepted    bool      `json:"accepted"`
	Reason      string    `json:"reason"`
	Quotes      []Quote   `json:"quotes"`
	EvaluatedAt time.Time `json:"evaluated_at"`
}

type State struct {
	Decision          domain.MainDecision `json:"decision"`
	ProvisionalSymbol string              `json:"provisional_symbol"`
	Status            string              `json:"status"`
	History           []RollEvent         `json:"history"`
	Snapshots         []CloseSnapshot     `json:"snapshots"`
	LastEvaluatedDate string              `json:"last_evaluated_date"`
	AlgorithmVersion  string              `json:"algorithm_version"`
}
