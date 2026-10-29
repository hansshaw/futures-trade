package currentanalysis

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"cornwatch/internal/datahub"
	"cornwatch/internal/domain"
	"cornwatch/internal/market"
)

func TestArrivalComparisonRetainsBothInputsAndChangesDigest(t *testing.T) {
	now, live, daily := evidenceInputs()
	current := domain.Observation{ID: "today-arrivals", Kind: "truck_arrivals", Region: "test-region", Entity: "same sample and window", Value: 344, Unit: "辆", Source: "test-arrivals-source", ObservedAt: now.Add(-12 * time.Hour), AvailableAt: now.Add(-time.Hour), Note: "isolated fixture; morning sample, not daily inflow"}
	previous := current
	previous.ID = "previous-arrivals"
	previous.Value = 251
	previous.ObservedAt = current.ObservedAt.Add(-24 * time.Hour)
	previous.AvailableAt = current.AvailableAt.Add(-24 * time.Hour)
	build := func(prior domain.Observation) Report {
		t.Helper()
		r, err := Build(live, daily, []domain.Observation{current, prior}, now)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	report := build(previous)
	evidence := map[string]domain.Observation{}
	for _, o := range report.Evidence {
		evidence[o.ID] = o
	}
	if len(evidence) != 2 || evidence[current.ID].Value != 344 || evidence[previous.ID].Value != 251 {
		t.Fatalf("comparison input missing from frozen evidence: %+v", report.Evidence)
	}
	found := false
	for _, f := range report.Factors {
		if f.Title != "到车与短时供应" {
			continue
		}
		found = true
		if !strings.Contains(f.Body, "+93") || len(f.EvidenceIDs) != 2 || f.EvidenceIDs[0] != current.ID || f.EvidenceIDs[1] != previous.ID {
			t.Fatalf("untraceable comparison: %+v", f)
		}
	}
	if !found {
		t.Fatal("arrival factor missing")
	}
	// Mutate only the older observation. The current observation, quote and
	// completed bars stay byte-identical; the audit digest must still change.
	previous.Value = 260
	revised := build(previous)
	if revised.InputSHA256 == report.InputSHA256 {
		t.Fatal("derived result changed without changing input digest")
	}
	for _, f := range revised.Factors {
		if f.Title == "到车与短时供应" && !strings.Contains(f.Body, "+84") {
			t.Fatalf("previous-period revision not used: %+v", f)
		}
	}
}
func TestUnavailablePreviousArrivalCannotEnterComparison(t *testing.T) {
	now, live, daily := evidenceInputs()
	current := domain.Observation{ID: "current", Kind: "truck_arrivals", Region: "region", Entity: "sample", Value: 344, Unit: "辆", Source: "fixture", ObservedAt: now.Add(-time.Hour), AvailableAt: now.Add(-time.Minute)}
	prior := current
	prior.ID = "not-yet-known"
	prior.Value = 251
	prior.ObservedAt = current.ObservedAt.Add(-24 * time.Hour)
	prior.AvailableAt = now.Add(time.Second)
	report, err := Build(live, daily, []domain.Observation{current, prior}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range report.Evidence {
		if o.ID == prior.ID {
			t.Fatal("future availability leaked into comparison evidence")
		}
	}
	for _, f := range report.Factors {
		if f.Title == "到车与短时供应" && (len(f.EvidenceIDs) != 1 || strings.Contains(f.Body, "上一公开记录")) {
			t.Fatalf("comparison cites not-yet-known prior: %+v", f)
		}
	}
}
func TestFallbackSelectionUsesOneRecentWindowIndependentOfInputOrder(t *testing.T) {
	now, base, _ := evidenceInputs()
	q := base.Quotes[0]
	candidates := make([]market.Quote, 3)
	for i, symbol := range []string{"c2701", "c2703", "c2705"} {
		candidates[i] = q
		candidates[i].Symbol = symbol
		candidates[i].ObservedAt = now.Add(-time.Duration(8-4*i) * time.Minute)
		candidates[i].Volume = int64(300 - 100*i)
	}
	// Pairwise "within five minutes" comparisons form a cycle for these three
	// records. A single window relative to the newest time excludes c2701 first.
	for _, order := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		live := datahub.Live{Quotes: []market.Quote{candidates[order[0]], candidates[order[1]], candidates[order[2]]}}
		got, _, err := Select(live, now)
		if err != nil || got.Symbol != "c2703" {
			t.Fatalf("order %v selected %s: %v", order, got.Symbol, err)
		}
	}
}
func TestEvidenceBudgetKeepsASecondPriceSource(t *testing.T) {
	now, live, daily := evidenceInputs()
	obs := []domain.Observation{}
	for i := 0; i < 22; i++ {
		obs = append(obs, domain.Observation{ID: fmt.Sprintf("enterprise-%d", i), Kind: "purchase_price", Region: "undisclosed", Entity: fmt.Sprintf("company %d", i), Value: 2200, Unit: "未注明", Source: "public-units-unknown", ObservedAt: now.Add(-24 * time.Hour), AvailableAt: now.Add(-time.Hour)})
	}
	known := domain.Observation{ID: "official-monitor", Kind: "purchase_price", Region: "official-region", Entity: "sample purchase mean", Value: 2100, Unit: "元/吨", Source: "official-weekly-monitor", ObservedAt: now.Add(-3 * 24 * time.Hour), AvailableAt: now.Add(-2 * 24 * time.Hour)}
	obs = append(obs, known)
	report, err := Build(live, daily, obs, now)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range report.Evidence {
		if o.ID == known.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("newer unknown-unit rows crowded all usable-unit source evidence out")
	}
	for _, f := range report.Factors {
		if f.Title == "收购端证据与口径" && !strings.Contains(f.Body, "1条有明确价格单位") {
			t.Fatalf("price source counts: %+v", f)
		}
	}
}
