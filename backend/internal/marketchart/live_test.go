package marketchart

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"cornwatch/internal/marketdaily"
)

// Explicit opt-in performs exactly one public minute-history request. Default
// unit/race tests stay offline and never use synthetic rows as market evidence.
func TestLivePublicMinuteOptIn(t *testing.T) {
	if os.Getenv("CORNWATCH_TEST_LIVE_HISTORY") != "1" {
		t.Skip("explicit live history probe only")
	}
	interval := os.Getenv("CORNWATCH_TEST_HISTORY_INTERVAL")
	if interval == "" {
		interval = "1m"
	}
	service := New(newStore(), marketdaily.New().Fetch)
	defer service.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	report, err := service.History(ctx, Request{Symbol: "c2701", Interval: interval, Limit: 6000})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Bars) < 20 || report.Cached || report.Stale || report.LastError != "" {
		t.Fatalf("unexpected live report: bars=%d cached=%v stale=%v error=%s", len(report.Bars), report.Cached, report.Stale, report.LastError)
	}
	evidence := map[string]any{"symbol": report.Symbol, "interval": report.Interval, "source": report.Source, "source_url": report.SourceURL, "available_at": report.AvailableAt, "count": len(report.Bars), "first": report.Bars[0], "last": report.Bars[len(report.Bars)-1], "warnings": report.Warnings, "requests": 1, "note": "Actual public minute history; no account, order, pressure test, or quantum/backtest import."}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if destination := os.Getenv("CORNWATCH_HISTORY_PROBE_OUTPUT"); destination != "" {
		if err = os.WriteFile(destination, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("one public request: %d bars from %s to %s, fetched %s", len(report.Bars), report.Bars[0].Timestamp, report.Bars[len(report.Bars)-1].Timestamp, report.AvailableAt)
}
