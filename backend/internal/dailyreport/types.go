// Package dailyreport freezes point-in-time research evidence and generates
// auditable reports. It has no storage, scheduler, tools, or trading capability.
package dailyreport

import (
	"time"

	"cornwatch/internal/currentanalysis"
)

const PromptVersion = "daily-report-v1"

const (
	MaxEvidence      = 64
	maxEvidenceText  = 32000 // Unicode code points across evidence, including notes.
	maxBundleBytes   = 1024 * 1024
	maxRequestBytes  = 512 * 1024
	maxResponseBytes = 1024 * 1024
)

type Evidence struct {
	ID          string    `json:"id"`
	Category    string    `json:"category"`
	Title       string    `json:"title"`
	Text        string    `json:"text"`
	Source      string    `json:"source"`
	SourceURL   string    `json:"source_url"`
	ObservedAt  time.Time `json:"observed_at"`
	AvailableAt time.Time `json:"available_at"`
	IsForecast  bool      `json:"is_forecast"`
	Unit        string    `json:"unit,omitempty"`
	Note        string    `json:"note,omitempty"`
	OriginalIDs []string  `json:"original_ids,omitempty"`
}

type Bundle struct {
	AsOf        time.Time               `json:"as_of"`
	Kind        string                  `json:"kind"`
	Date        string                  `json:"date"`
	Symbol      string                  `json:"symbol"`
	Evidence    []Evidence              `json:"evidence"`
	Warnings    []string                `json:"warnings"`
	Current     *currentanalysis.Report `json:"current"`
	InputSHA256 string                  `json:"input_sha256"`
}

type Quote struct {
	EvidenceID string `json:"evidence_id"`
	Text       string `json:"text"`
}

type Section struct {
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	EvidenceIDs []string `json:"evidence_ids"`
	Quotes      []Quote  `json:"quotes"`
}

type Scenario struct {
	Name         string   `json:"name"`
	Condition    string   `json:"condition"`
	Invalidation string   `json:"invalidation"`
	EvidenceIDs  []string `json:"evidence_ids"`
}

type Output struct {
	Mode          string     `json:"mode"` // "rules" is always a local preview; "llm" is model output.
	Summary       string     `json:"summary"`
	Sections      []Section  `json:"sections"`
	Scenarios     []Scenario `json:"scenarios"`
	Risks         []string   `json:"risks"`
	Model         string     `json:"model"`
	PromptVersion string     `json:"prompt_version"`
	GeneratedAt   time.Time  `json:"generated_at"`
	InputSHA256   string     `json:"input_sha256"`
}
