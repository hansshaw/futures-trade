package store

import (
	"cornwatch/internal/domain"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func reviewStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func reviewBar(day int) domain.Bar {
	tm := time.Date(2026, 9, day, 7, 0, 0, 0, time.UTC)
	return domain.Bar{Symbol: "c2701", Timestamp: tm, TradeDate: tm.Format("2006-01-02"), Open: 2200, High: 2220, Low: 2190, Close: 2210, Settlement: 2205, Volume: 1000, OpenInterest: 2000}
}

func TestImportBarsRollsBackEntireConflictingBatch(t *testing.T) {
	s := reviewStore(t)
	original := reviewBar(2)
	if _, _, err := s.ImportBars([]domain.Bar{original}, "first"); err != nil {
		t.Fatal(err)
	}
	conflict := original
	conflict.Close = 2211
	added, duplicates, err := s.ImportBars([]domain.Bar{reviewBar(1), conflict}, "second")
	if err == nil || added != 0 || duplicates != 0 {
		t.Fatalf("want atomic failure, got %d %d %v", added, duplicates, err)
	}
	bars, err := s.Bars("c2701", false)
	if err != nil || len(bars) != 1 || bars[0].Close != original.Close {
		t.Fatalf("partial write: %+v, %v", bars, err)
	}
}

func TestConcurrentImportPreservesOneDailyBar(t *testing.T) {
	s := reviewStore(t)
	bars := []domain.Bar{reviewBar(2), reviewBar(2)}
	bars[1].Timestamp = bars[1].Timestamp.Add(time.Minute)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, b := range bars {
		wg.Add(1)
		go func(b domain.Bar) {
			defer wg.Done()
			<-start
			_, _, err := s.ImportBars([]domain.Bar{b}, "concurrent")
			errs <- err
		}(b)
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	stored, err := s.Bars("c2701", false)
	if err != nil {
		t.Fatal(err)
	}
	if successes != 1 || len(stored) != 1 {
		t.Fatalf("same contract/day admitted twice: successes=%d records=%d", successes, len(stored))
	}
}

func TestDuplicateEventPreservesFirstAvailabilityAndAnalysis(t *testing.T) {
	s := reviewStore(t)
	tm := time.Date(2026, 9, 2, 7, 0, 0, 0, time.UTC)
	e := domain.Event{ID: "event-one", Title: "原始报价", Body: "报价2200元", PublishedAt: tm.Add(-time.Hour), IngestedAt: tm, AvailableAt: tm}
	if _, created, err := s.AddEvent(e); err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	a := domain.Analysis{Model: "rules-v1", Summary: "待核验", AnalyzedAt: tm.Add(time.Minute)}
	if _, err := s.AnalyzeEvent(e.ID, a); err != nil {
		t.Fatal(err)
	}
	e.IngestedAt = tm.Add(24 * time.Hour)
	e.AvailableAt = e.IngestedAt
	e.Body = "重试不应覆盖原始事实"
	result, created, err := s.AddEvent(e)
	if err != nil || created || !result.AvailableAt.Equal(tm) || result.Body != "报价2200元" || result.Analysis == nil || result.Analysis.Model != a.Model {
		t.Fatalf("duplicate changed original: %+v created=%v err=%v", result, created, err)
	}
}

func TestDemoAndImportedBarsRemainIsolated(t *testing.T) {
	s := reviewStore(t)
	real := reviewBar(2)
	demo := real
	demo.IsDemo = true
	demo.Close = 2212
	if _, _, err := s.ImportBars([]domain.Bar{real, demo}, "test"); err != nil {
		t.Fatal(err)
	}
	actual, _ := s.Bars("c2701", false)
	synthetic, _ := s.Bars("c2701", true)
	if len(actual) != 1 || len(synthetic) != 1 || actual[0].Close != real.Close || synthetic[0].Close != demo.Close {
		t.Fatalf("keyspaces mixed: actual=%+v demo=%+v", actual, synthetic)
	}
}
