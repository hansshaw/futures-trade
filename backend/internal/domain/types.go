package domain

import "time"

type Bar struct {
	Symbol       string    `json:"symbol"`
	Timestamp    time.Time `json:"timestamp"`
	TradeDate    string    `json:"trade_date"`
	Open         float64   `json:"open"`
	High         float64   `json:"high"`
	Low          float64   `json:"low"`
	Close        float64   `json:"close"`
	Settlement   float64   `json:"settlement"`
	Volume       int64     `json:"volume"`
	OpenInterest int64     `json:"open_interest"`
	LimitUp      float64   `json:"limit_up"`
	LimitDown    float64   `json:"limit_down"`
	IsDemo       bool      `json:"is_demo"`
}

type Quote struct {
	Product            string    `json:"product,omitempty"`
	PreviousSettlement float64   `json:"previous_settlement,omitempty"`
	ChangeBasis        string    `json:"change_basis,omitempty"`
	ReceivedAt         time.Time `json:"received_at,omitempty"`
	SourceURL          string    `json:"source_url,omitempty"`
	Freshness          string    `json:"freshness,omitempty"`
	FreshnessNote      string    `json:"freshness_note,omitempty"`
	Symbol             string    `json:"symbol"`
	Name               string    `json:"name"`
	Last               float64   `json:"last"`
	PreviousClose      float64   `json:"previous_close"`
	ChangePct          float64   `json:"change_pct"`
	Volume             int64     `json:"volume"`
	OpenInterest       int64     `json:"open_interest"`
	ObservedAt         time.Time `json:"observed_at"`
	Source             string    `json:"source"`
	IsDemo             bool      `json:"is_demo"`
}

type Analysis struct {
	Summary    string    `json:"summary"`
	Direction  string    `json:"direction"`
	Confidence float64   `json:"confidence"`
	Reasoning  []string  `json:"reasoning"`
	Evidence   []string  `json:"evidence"`
	RiskFlags  []string  `json:"risk_flags"`
	Model      string    `json:"model"`
	AnalyzedAt time.Time `json:"analyzed_at"`
}

type Event struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Category    string    `json:"category"`
	Region      string    `json:"region"`
	Products    []string  `json:"products"`
	Source      string    `json:"source"`
	SourceURL   string    `json:"source_url"`
	PublishedAt time.Time `json:"published_at"`
	IngestedAt  time.Time `json:"ingested_at"`
	AvailableAt time.Time `json:"available_at"`
	Severity    string    `json:"severity"`
	IsDemo      bool      `json:"is_demo"`
	Analysis    *Analysis `json:"analysis,omitempty"`
}

type Observation struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Region      string    `json:"region"`
	Entity      string    `json:"entity"`
	Value       float64   `json:"value"`
	Unit        string    `json:"unit"`
	ObservedAt  time.Time `json:"observed_at"`
	AvailableAt time.Time `json:"available_at"`
	Source      string    `json:"source"`
	SourceURL   string    `json:"source_url"`
	IsDemo      bool      `json:"is_demo"`
	Note        string    `json:"note"`
}

type MainDecision struct {
	Symbol           string    `json:"symbol"`
	PreviousSymbol   string    `json:"previous_symbol"`
	Reason           string    `json:"reason"`
	AsOf             time.Time `json:"as_of"`
	PendingSymbol    string    `json:"pending_symbol"`
	ConfirmationDays int       `json:"confirmation_days"`
	RequiredDays     int       `json:"required_days"`
	IsDemo           bool      `json:"is_demo"`
}

type SourceStatus struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	Message     string     `json:"message"`
}
