// Package datahub schedules independent data sources and preserves collection
// health separately from the timestamp of the source's actual data.
package datahub

import (
	"context"
	"cornwatch/internal/domain"
	"cornwatch/internal/market"
	"time"
)

type Config struct {
	Revision        uint64 `json:"revision"`
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"interval_seconds"`
	Endpoint        string `json:"endpoint"`
	Token           string `json:"token,omitempty"`
}
type ConfigView struct {
	Revision        uint64 `json:"revision"`
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"interval_seconds"`
	Endpoint        string `json:"endpoint"`
	TokenConfigured bool   `json:"token_configured"`
}
type ConfigRequest struct {
	Revision        uint64 `json:"revision"`
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"interval_seconds"`
	Endpoint        string `json:"endpoint"`
	Token           string `json:"token,omitempty"`
	ClearToken      bool   `json:"clear_token,omitempty"`
}
type Definition struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Kind                 string `json:"kind"`
	Description          string `json:"description"`
	OfficialURL          string `json:"official_url"`
	AccountRequired      bool   `json:"account_required"`
	CostNote             string `json:"cost_note"`
	LicenseNote          string `json:"license_note"`
	ConfigurableEndpoint bool   `json:"configurable_endpoint"`
	MinIntervalSeconds   int    `json:"min_interval_seconds"`
	MaxIntervalSeconds   int    `json:"max_interval_seconds"`
	DefaultInterval      int    `json:"-"`
}
type PricePoint struct {
	ObservedAt time.Time `json:"observed_at"`
	Last       float64   `json:"last"`
}
type Record struct {
	NoPublication       string                  `json:"no_publication,omitempty"`
	ConfigRevision      uint64                  `json:"config_revision,omitempty"` // Configuration used to compute NextRun.
	PriceHistory        map[string][]PricePoint `json:"price_history,omitempty"`
	LastAttempt         *time.Time              `json:"last_attempt,omitempty"`
	LastSuccess         *time.Time              `json:"last_success,omitempty"`
	LatestDataAt        *time.Time              `json:"latest_data_at,omitempty"`
	NextRun             *time.Time              `json:"next_run,omitempty"`
	LastError           string                  `json:"last_error"`
	ConsecutiveFailures int                     `json:"consecutive_failures"`
	Added               int                     `json:"added"`
	Duplicates          int                     `json:"duplicates"`
	TotalRecords        int                     `json:"total_records"`
	Coverage            []string                `json:"coverage"`
	Quotes              []market.Quote          `json:"quotes,omitempty"`
	Main                market.State            `json:"main"`
	Binding             string                  `json:"binding"`
}
type Source struct {
	Definition
	Config              ConfigView `json:"config"`
	Status              string     `json:"status"`
	Freshness           string     `json:"freshness"`
	FreshnessNote       string     `json:"freshness_note"`
	LastAttempt         *time.Time `json:"last_attempt,omitempty"`
	LastSuccess         *time.Time `json:"last_success,omitempty"`
	LatestDataAt        *time.Time `json:"latest_data_at,omitempty"`
	NextRun             *time.Time `json:"next_run,omitempty"`
	LastError           string     `json:"last_error"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	Added               int        `json:"added"`
	Duplicates          int        `json:"duplicates"`
	TotalRecords        int        `json:"total_records"`
	Coverage            []string   `json:"coverage"`
}
type Batch struct {
	// NoPublication distinguishes a valid empty publication window from a failed fetch.
	NoPublication string
	Quotes        []market.Quote
	Observations  []domain.Observation
	Events        []domain.Event
	// DataAt is the publication/observation time. Weather uses acquisition time:
	// its target dates are future validity dates, never a last-update timestamp.
	DataAt   time.Time
	Coverage []string
}
type Fetcher func(context.Context, Config) (Batch, error)
type Outcome struct {
	OK           bool       `json:"ok"`
	Message      string     `json:"message"`
	RecordCount  int        `json:"record_count"`
	Added        int        `json:"added"`
	Duplicates   int        `json:"duplicates"`
	LatestDataAt *time.Time `json:"latest_data_at,omitempty"`
	Coverage     []string   `json:"coverage"`
}
type Live struct {
	PriceHistory  map[string][]PricePoint `json:"price_history"`
	Status        string                  `json:"status"`
	LastError     string                  `json:"last_error"`
	SourceID      string                  `json:"source_id"`
	SourceName    string                  `json:"source_name"`
	Quotes        []market.Quote          `json:"quotes"`
	Main          market.State            `json:"main"`
	StateHistory  []market.RollEvent      `json:"state_history"`
	Freshness     string                  `json:"freshness"`
	FreshnessNote string                  `json:"freshness_note"`
	LastSuccess   *time.Time              `json:"last_success,omitempty"`
	LatestDataAt  *time.Time              `json:"latest_data_at,omitempty"`
	SelectionNote string                  `json:"selection_note"`
}
