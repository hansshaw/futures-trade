// Package research exposes auditable daily-bar diagnostics and risk studies.
// None of its outputs are executable orders or predictions of future returns.
package research

import (
	"cornwatch/internal/quant"
	"time"
)

type Point struct {
	Timestamp    time.Time `json:"timestamp"`
	TradeDate    string    `json:"trade_date"`
	Open         float64   `json:"open"`
	High         float64   `json:"high"`
	Low          float64   `json:"low"`
	Close        float64   `json:"close"`
	Volume       int64     `json:"volume"`
	OpenInterest int64     `json:"open_interest"`
	MA5          *float64  `json:"ma5"`
	MA20         *float64  `json:"ma20"`
	MA60         *float64  `json:"ma60"`
	ATR14        *float64  `json:"atr14"`
	RSI14        *float64  `json:"rsi14"`
	MACD         *float64  `json:"macd"`
	MACDSignal   *float64  `json:"macd_signal"`
	MACDHist     *float64  `json:"macd_hist"`
	PriorHigh20  *float64  `json:"prior_high20"`
	PriorLow20   *float64  `json:"prior_low20"`
}

type Latest struct {
	Close         float64  `json:"close"`
	ChangePct     *float64 `json:"change_pct"`
	OIChange      *int64   `json:"oi_change"`
	OIChangePct   *float64 `json:"oi_change_pct"`
	VolumeRatio20 *float64 `json:"volume_ratio20"`
	ATR14         *float64 `json:"atr14"`
	ATRPct        *float64 `json:"atr_pct"`
	RSI14         *float64 `json:"rsi14"`
	MA20          *float64 `json:"ma20"`
	MA60          *float64 `json:"ma60"`
	PriorHigh20   *float64 `json:"prior_high20"`
	PriorLow20    *float64 `json:"prior_low20"`
	Trend         string   `json:"trend"`
	PriceOILabel  string   `json:"price_oi_label"`
}

type Insight struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tone  string `json:"tone"`
}

type TermPoint struct {
	Symbol       string  `json:"symbol"`
	TradeDate    string  `json:"trade_date"`
	Close        float64 `json:"close"`
	Volume       int64   `json:"volume"`
	OpenInterest int64   `json:"open_interest"`
}

type Spread struct {
	NearSymbol   string  `json:"near_symbol"`
	FarSymbol    string  `json:"far_symbol"`
	NearMinusFar float64 `json:"near_minus_far"`
	TradeDate    string  `json:"trade_date"`
}

type MarketReport struct {
	Symbol        string      `json:"symbol"`
	IsDemo        bool        `json:"is_demo"`
	AsOf          time.Time   `json:"as_of"`
	BarCount      int         `json:"bar_count"`
	Points        []Point     `json:"points"`
	Latest        Latest      `json:"latest"`
	Insights      []Insight   `json:"insights"`
	Warnings      []string    `json:"warnings"`
	TermStructure []TermPoint `json:"term_structure"`
	Spread        *Spread     `json:"spread"`
}

type RiskConfig struct {
	Equity        float64 `json:"equity"`
	Entry         float64 `json:"entry"`
	Stop          float64 `json:"stop"`
	Direction     string  `json:"direction"`
	RiskPct       float64 `json:"risk_pct"`       // 1 means 1% of account equity.
	MarginRate    float64 `json:"margin_rate"`    // 0.12 means 12%.
	MaxMarginPct  float64 `json:"max_margin_pct"` // 40 means 40% of equity.
	FeePerLot     float64 `json:"fee_per_lot"`    // Per side, yuan per lot.
	SlippageTicks int     `json:"slippage_ticks"` // Per side.
}

type RiskResult struct {
	RiskBudget   float64  `json:"risk_budget"`
	LossPerLot   float64  `json:"loss_per_lot"`
	MarginPerLot float64  `json:"margin_per_lot"`
	RiskLots     int      `json:"risk_lots"`
	MarginLots   int      `json:"margin_lots"`
	MaxLots      int      `json:"max_lots"`
	PlannedLoss  float64  `json:"planned_loss"`
	MarginUsed   float64  `json:"margin_used"`
	Notional     float64  `json:"notional"`
	Leverage     float64  `json:"leverage"`
	Warnings     []string `json:"warnings"`
}

type Segment struct {
	Name     string        `json:"name"`
	Start    string        `json:"start"`
	End      string        `json:"end"`
	BarCount int           `json:"bar_count"`
	Metrics  quant.Metrics `json:"metrics"`
}

type CostScenario struct {
	Name          string        `json:"name"`
	FeePerLot     float64       `json:"fee_per_lot"`
	SlippageTicks int           `json:"slippage_ticks"`
	Metrics       quant.Metrics `json:"metrics"`
}

type ValidationReport struct {
	Symbol        string         `json:"symbol"`
	IsDemo        bool           `json:"is_demo"`
	Config        quant.Config   `json:"config"`
	InputSHA256   string         `json:"input_sha256"`
	EngineVersion string         `json:"engine_version"`
	SplitDate     string         `json:"split_date"`
	TrainBars     int            `json:"train_bars"`
	TestBars      int            `json:"test_bars"`
	Segments      []Segment      `json:"segments"`
	CostScenarios []CostScenario `json:"cost_scenarios"`
	Warnings      []string       `json:"warnings"`
}
