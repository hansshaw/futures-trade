package market

import (
	"testing"
	"time"

	"cornwatch/internal/domain"
)

func snapshots(date, clock string, volumes, oi []int64) ([]Quote, time.Time) {
	now := at(date + " " + clock)
	observed := now
	if now.Hour() >= 15 && now.Hour() < 21 {
		observed = at(date + " 15:04:55")
	}
	quotes := make([]Quote, len(volumes))
	for i, symbol := range []string{"c2701", "c2703", "c2705"} {
		if i >= len(volumes) {
			break
		}
		quotes[i] = Quote{Symbol: symbol, Product: "c", Exchange: "DCE", TradeDate: date, Last: 2200, Volume: volumes[i], OpenInterest: oi[i], ObservedAt: observed, ReceivedAt: now, Source: SourceName}
	}
	return quotes, now
}
func applyClose(state State, date string, v, oi []int64) State {
	q, now := snapshots(date, "16:00:00", v, oi)
	return Update(state, q, now)
}

func TestInitialTwoDistinctObservedCloses(t *testing.T) {
	q, now := snapshots("2026-10-27", "14:00:00", []int64{200, 100}, []int64{200, 100})
	s := Update(State{}, q, now)
	if s.Decision.Symbol != "" || s.ProvisionalSymbol != "c2701" || s.Decision.ConfirmationDays != 0 || s.LastEvaluatedDate != "" {
		t.Fatalf("%+v", s)
	}
	s = applyClose(s, "2026-10-27", []int64{200, 100}, []int64{200, 100})
	if s.Decision.Symbol != "" || s.Decision.ConfirmationDays != 1 {
		t.Fatalf("%+v", s)
	}
	for i := 0; i < 4; i++ {
		s = applyClose(s, "2026-10-27", []int64{200, 100}, []int64{200, 100})
	}
	if s.Decision.ConfirmationDays != 1 || len(s.Snapshots) != 1 {
		t.Fatal("polls accumulated days")
	}
	s = applyClose(s, "2026-10-28", []int64{200, 100}, []int64{200, 100})
	if s.Decision.Symbol != "c2701" || len(s.History) != 1 || s.History[0].OldSymbol != "" || s.History[0].NewSymbol != "c2701" || !s.History[0].EffectiveAt.Equal(at("2026-10-28 16:00:00")) {
		t.Fatalf("%+v", s)
	}
}

func TestRollStrictDualTenPercentAndOnlyLater(t *testing.T) {
	s := State{Decision: domain.MainDecision{Symbol: "c2701"}}
	s = applyClose(s, "2026-10-26", []int64{100, 110}, []int64{100, 111})
	if s.Decision.PendingSymbol != "" {
		t.Fatal("exactly10% accepted")
	}
	s = applyClose(s, "2026-10-27", []int64{100, 111}, []int64{100, 110})
	if s.Decision.PendingSymbol != "" {
		t.Fatal("only volume sufficient accepted")
	}
	s = applyClose(s, "2026-10-28", []int64{100, 111}, []int64{100, 111})
	if s.Decision.PendingSymbol != "c2703" || s.Decision.ConfirmationDays != 1 {
		t.Fatalf("%+v", s)
	}
	s = applyClose(s, "2026-10-29", []int64{100, 111}, []int64{100, 111})
	if s.Decision.Symbol != "c2703" || s.Decision.PreviousSymbol != "c2701" || len(s.History) != 1 {
		t.Fatalf("%+v", s)
	}
	s = applyClose(s, "2026-10-30", []int64{500, 100}, []int64{500, 100})
	if s.Decision.PendingSymbol != "" || s.Decision.Symbol != "c2703" {
		t.Fatal("rolled back")
	}
}

func TestSplitLeadersAndBrokenObservationReset(t *testing.T) {
	s := applyClose(State{}, "2026-10-26", []int64{200, 100}, []int64{100, 200})
	if s.ProvisionalSymbol != "" || s.Decision.ConfirmationDays != 0 {
		t.Fatal("split leaders accepted")
	}
	s = applyClose(s, "2026-10-27", []int64{200, 100}, []int64{200, 100})
	s = applyClose(s, "2026-10-28", []int64{200, 100}, []int64{100, 200})
	s = applyClose(s, "2026-10-29", []int64{200, 100}, []int64{200, 100})
	if s.Decision.Symbol != "" || s.Decision.ConfirmationDays != 1 {
		t.Fatal("failed observed day did not break consecutive confirmations")
	}
}

func TestStaleFutureAndMixedSourcesCannotConfirm(t *testing.T) {
	base := applyClose(State{}, "2026-10-27", []int64{200, 100}, []int64{200, 100})
	for _, tc := range []struct {
		name   string
		mutate func([]Quote, time.Time) ([]Quote, time.Time)
	}{
		{"oldDay", func(q []Quote, now time.Time) ([]Quote, time.Time) { return q, now.AddDate(0, 0, 1) }},
		{"oldReceipt", func(q []Quote, now time.Time) ([]Quote, time.Time) {
			for i := range q {
				q[i].ReceivedAt = now.AddDate(0, 0, -1)
			}
			return q, now
		}},
		{"future", func(q []Quote, now time.Time) ([]Quote, time.Time) {
			for i := range q {
				q[i].ObservedAt = now.Add(time.Hour)
			}
			return q, now
		}},
		{"mixed", func(q []Quote, now time.Time) ([]Quote, time.Time) { q[1].Source = "different"; return q, now }},
		{"duplicate", func(q []Quote, now time.Time) ([]Quote, time.Time) { return append(q, q[0]), now }},
		{"earlyClock", func(q []Quote, now time.Time) ([]Quote, time.Time) {
			for i := range q {
				q[i].ObservedAt = at("2026-10-28 14:59:59")
			}
			return q, now
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, now := snapshots("2026-10-28", "16:00:00", []int64{200, 100}, []int64{200, 100})
			q, now = tc.mutate(q, now)
			s := Update(base, q, now)
			if s.Decision.Symbol != "" || s.Decision.ConfirmationDays != 1 || s.LastEvaluatedDate != "2026-10-27" {
				t.Fatalf("advanced: %+v", s)
			}
		})
	}
}

func TestUnchangedCloseKeepsRealReceiptAndCanConfirm(t *testing.T) {
	s := applyClose(State{}, "2026-10-27", []int64{200, 100}, []int64{200, 100})
	q, now := snapshots("2026-10-28", "16:00:00", []int64{200, 100}, []int64{200, 100})
	for i := range q {
		q[i].ObservedAt = at("2026-10-28 15:00:00")
		q[i].ReceivedAt = at("2026-10-28 15:00:01")
	}
	s = Update(s, q, now)
	if s.Decision.Symbol != "c2701" {
		t.Fatalf("legitimate unchanged closing snapshot was rejected: %+v", s)
	}
	q, now = snapshots("2026-10-29", "14:00:00", []int64{200, 100}, []int64{200, 100})
	for i := range q {
		q[i].ObservedAt = now.Add(-6 * time.Minute)
		q[i].ReceivedAt = now.Add(-6 * time.Minute)
	}
	s = Update(s, q, now)
	if s.Status != "stale" {
		t.Fatalf("old intraday receipt was accepted: %+v", s)
	}
}

func TestNightDoesNotInferTradingDateAndCloseNeedsMaturity(t *testing.T) {
	for _, clock := range []string{"15:10:00", "21:00:00", "00:10:00"} {
		q, now := snapshots("2026-10-28", clock, []int64{200, 100}, []int64{200, 100})
		if clock == "15:10:00" {
			for i := range q {
				q[i].ObservedAt = at("2026-10-28 15:04:55")
			}
		}
		s := Update(State{}, q, now)
		if s.Decision.ConfirmationDays != 0 || s.LastEvaluatedDate != "" {
			t.Fatalf("clock %s advanced %+v", clock, s)
		}
	}
}

func TestMissingIncumbentBreaksConfirmation(t *testing.T) {
	s := State{Decision: domain.MainDecision{Symbol: "c2701", PendingSymbol: "c2703", ConfirmationDays: 1}, LastEvaluatedDate: "2026-10-27"}
	q, now := snapshots("2026-10-28", "16:00:00", []int64{100, 200, 150}, []int64{100, 200, 150})
	s = Update(s, q[1:], now)
	if s.Decision.PendingSymbol != "" || s.Decision.ConfirmationDays != 0 || s.Decision.Symbol != "c2701" {
		t.Fatalf("%+v", s)
	}
}

func TestDeliveryMonthRemovedAndPastDateCannotAdvance(t *testing.T) {
	s := State{Decision: domain.MainDecision{Symbol: "c2701"}}
	q, now := snapshots("2027-01-05", "16:00:00", []int64{900, 200, 100}, []int64{900, 200, 100})
	s = Update(s, q, now)
	if s.Decision.Symbol != "" || s.ProvisionalSymbol != "c2703" || s.Decision.PreviousSymbol != "c2701" {
		t.Fatalf("%+v", s)
	}
	s = applyClose(s, "2027-01-04", []int64{900, 200, 100}, []int64{900, 200, 100})
	if s.Decision.ConfirmationDays != 1 || len(s.Snapshots) != 1 {
		t.Fatal("past date advanced")
	}
}

func TestStateSlicesAreNotMutated(t *testing.T) {
	s := State{History: make([]RollEvent, 1, 4), Snapshots: make([]CloseSnapshot, 1, 4)}
	s.History[0] = RollEvent{NewSymbol: "c2609"}
	s.Snapshots[0] = CloseSnapshot{TradeDate: "2026-10-26"}
	next := applyClose(s, "2026-10-27", []int64{200, 100}, []int64{200, 100})
	next.Snapshots[0].TradeDate = "changed"
	if s.Snapshots[0].TradeDate != "2026-10-26" {
		t.Fatal("mutated caller state")
	}
}
