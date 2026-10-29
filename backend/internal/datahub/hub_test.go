package datahub

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cornwatch/internal/domain"
	"cornwatch/internal/market"
	"cornwatch/internal/store"
)

var testNow = time.Date(2026, 10, 28, 7, 30, 0, 0, time.UTC)

func testHub(t *testing.T) (*Hub, *store.Store, string) {
	t.Helper()
	t.Setenv("CORNWATCH_AUTO_SYNC", "true")
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return testNow }
	return h, s, path
}
func advanceHubClock(h *Hub, delta time.Duration) {
	now := h.now().Add(delta)
	h.now = func() time.Time { return now }
}
func saveBridge(t *testing.T, h *Hub, id, endpoint, token string) Source {
	t.Helper()
	c := h.configs[id]
	got, err := h.Save(id, ConfigRequest{Revision: c.Revision, Enabled: true, IntervalSeconds: c.IntervalSeconds, Endpoint: endpoint, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func sourceByID(t *testing.T, h *Hub, id string) Source {
	t.Helper()
	for _, s := range h.Sources() {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("source %s missing", id)
	return Source{}
}
func exampleQuotes(endpoint string, at time.Time) []market.Quote {
	return []market.Quote{
		{Symbol: "c2701", Product: "c", Exchange: "DCE", TradeDate: at.Format("2006-01-02"), ObservedAt: at, ReceivedAt: at.Add(time.Second), Source: "bridge", SourceURL: endpoint, Last: 2300, PreviousSettlement: 2290, Volume: 200, OpenInterest: 400},
		{Symbol: "c2705", Product: "c", Exchange: "DCE", TradeDate: at.Format("2006-01-02"), ObservedAt: at, ReceivedAt: at.Add(time.Second), Source: "bridge", SourceURL: endpoint, Last: 2320, PreviousSettlement: 2310, Volume: 100, OpenInterest: 200},
	}
}
func exampleObservation() domain.Observation {
	return domain.Observation{ID: "one", Kind: "truck_arrivals", Region: "山东", Entity: "厂区A", Source: "授权供应商", SourceURL: "https://source.example/report", Note: "2026-10-28 08时门前待卸车辆；单厂统计", Value: 42, Unit: "辆", ObservedAt: testNow.Add(-time.Hour), AvailableAt: testNow}
}
func TestConfigRevisionAndCredentialOrigins(t *testing.T) {
	h, _, _ := testHub(t)
	first := saveBridge(t, h, "market_bridge", "https://EXAMPLE.com/first", "secret-one")
	if first.Config.Revision != 2 || !first.Config.TokenConfigured {
		t.Fatalf("bad view: %+v", first.Config)
	}
	b, _ := json.Marshal(h.Sources())
	if strings.Contains(string(b), "secret-one") {
		t.Fatal("token leaked in sources")
	}
	for _, tc := range []struct{ endpoint, token, want string }{
		{"https://example.com:443/second", "", "secret-one"},
		{"https://example.com:444/second", "", ""},
		{"https://example.com:444/second", "new-token", "new-token"},
		{"https://elsewhere.example/path", "explicit-new", "explicit-new"},
		{"", "", ""},
		{"http://localhost:8080/data", "local", "local"},
		{"https://localhost:8080/data", "", ""},
	} {
		saveBridge(t, h, "market_bridge", tc.endpoint, tc.token)
		if got := h.configs["market_bridge"].Token; got != tc.want {
			t.Fatalf("%s token=%q want %q", tc.endpoint, got, tc.want)
		}
	}
	current := h.configs["market_bridge"]
	request := ConfigRequest{Revision: current.Revision - 1, Enabled: true, IntervalSeconds: 10, Endpoint: current.Endpoint}
	if _, err := h.Save("market_bridge", request); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	request.Revision = current.Revision
	request.ClearToken = true
	request.Token = "bad"
	if _, err := h.Save("market_bridge", request); err == nil {
		t.Fatal("ambiguous credential operation accepted")
	}
	if h.configs["market_bridge"].Revision != current.Revision {
		t.Fatal("invalid save changed revision")
	}
	if _, err := h.Save("missing", request); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown: %v", err)
	}
}
func TestConcurrentConfigRevision(t *testing.T) {
	h, _, _ := testHub(t)
	request := ConfigRequest{Revision: 1, Enabled: true, IntervalSeconds: 10, Endpoint: "https://example.com/data"}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := h.Save("market_bridge", request); results <- err }()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d", successes, conflicts)
	}
}
func TestConcurrentRunAndSaveAreBusy(t *testing.T) {
	h, _, _ := testHub(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	h.fetchers["sina"] = func(ctx context.Context, _ Config) (Batch, error) {
		close(entered)
		select {
		case <-release:
			return quoteBatch(exampleQuotes("public", testNow)), nil
		case <-ctx.Done():
			return Batch{}, ctx.Err()
		}
	}
	result := make(chan error, 1)
	go func() { _, err := h.Run(context.Background(), "sina", false); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("fetch not entered")
	}
	if sourceByID(t, h, "sina").Status != "running" {
		t.Fatal("running status missing")
	}
	if _, err := h.Run(context.Background(), "sina", true); !errors.Is(err, ErrBusy) {
		t.Fatalf("parallel run: %v", err)
	}
	if _, err := h.RunScheduled(context.Background(), "sina"); !errors.Is(err, ErrBusy) {
		t.Fatalf("report/scheduled parallel run: %v", err)
	}
	c := h.configs["sina"]
	if _, err := h.Save("sina", ConfigRequest{Revision: c.Revision, Enabled: true, IntervalSeconds: c.IntervalSeconds}); !errors.Is(err, ErrBusy) {
		t.Fatalf("save while running: %v", err)
	}
	once.Do(func() { close(release) })
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
func TestConnectionTestDoesNotWrite(t *testing.T) {
	h, s, _ := testHub(t)
	before := cloneRecord(h.records["liaoning"])
	o := exampleObservation()
	e := domain.Event{ID: "event-one", Title: "source title", PublishedAt: testNow, AvailableAt: testNow}
	h.fetchers["liaoning"] = func(context.Context, Config) (Batch, error) {
		return observationBatch([]domain.Observation{o}, []domain.Event{e}), nil
	}
	result, err := h.Run(context.Background(), "liaoning", true)
	if err != nil || !result.OK || result.RecordCount != 2 {
		t.Fatalf("test: %+v %v", result, err)
	}
	if !reflect.DeepEqual(before, h.records["liaoning"]) {
		t.Fatal("test mutated status")
	}
	obs, _ := s.Observations()
	events, _ := s.Events()
	if len(obs) != 0 || len(events) != 0 {
		t.Fatal("test wrote domain data")
	}
	var rec Record
	if err := s.GetMeta("datahub_record_liaoning", &rec); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("test wrote metadata: %v", err)
	}
	h.fetchers["liaoning"] = func(context.Context, Config) (Batch, error) { return Batch{}, errors.New("offline") }
	advanceHubClock(h, 6*time.Second)
	if _, err := h.Run(context.Background(), "liaoning", true); err == nil {
		t.Fatal("failed test accepted")
	}
	if !reflect.DeepEqual(before, h.records["liaoning"]) {
		t.Fatal("failed test mutated status")
	}
}
func TestFailedSyncRetainsSnapshotsAndBacksOff(t *testing.T) {
	h, s, _ := testHub(t)
	endpoint := "https://example.com/quotes"
	saveBridge(t, h, "market_bridge", endpoint, "very-secret-token")
	at := testNow.Add(-30 * time.Minute)
	quotes := exampleQuotes(endpoint, at)
	h.fetchers["market_bridge"] = func(context.Context, Config) (Batch, error) { return quoteBatch(quotes), nil }
	if _, err := h.Run(context.Background(), "market_bridge", false); err != nil {
		t.Fatal(err)
	}
	before := h.Live()
	h.fetchers["market_bridge"] = func(context.Context, Config) (Batch, error) {
		return Batch{}, errors.New("failure at " + endpoint + " with very-secret-token")
	}
	for i := 1; i <= 8; i++ {
		advanceHubClock(h, 6*time.Second)
		if _, err := h.Run(context.Background(), "market_bridge", false); err == nil || strings.Contains(err.Error(), "very-secret-token") || strings.Contains(err.Error(), endpoint) {
			t.Fatalf("unsafe error: %v", err)
		}
		got := h.Live()
		status := sourceByID(t, h, "market_bridge")
		if !reflect.DeepEqual(got.Quotes, before.Quotes) || !reflect.DeepEqual(got.Main, before.Main) || !reflect.DeepEqual(got.PriceHistory, before.PriceHistory) || !got.LastSuccess.Equal(*before.LastSuccess) {
			t.Fatal("failure replaced last good snapshot")
		}
		if status.Status != "error" || status.ConsecutiveFailures != i || status.NextRun.Sub(h.now()) != 2500*time.Millisecond*time.Duration(1<<min(i, 6)) {
			t.Fatalf("backoff=%+v", status)
		}
	}
	bars, err := s.Bars("", false)
	if err != nil || len(bars) > 0 {
		t.Fatalf("snapshots wrote bars: %d %v", len(bars), err)
	}
	h.fetchers["market_bridge"] = func(context.Context, Config) (Batch, error) { return quoteBatch(quotes), nil }
	advanceHubClock(h, 6*time.Second)
	out, err := h.Run(context.Background(), "market_bridge", false)
	if err != nil || out.Added != 0 || out.Duplicates != 2 || h.records["market_bridge"].ConsecutiveFailures != 0 {
		t.Fatalf("recovery: %+v %v", out, err)
	}
	if runDelay(Config{IntervalSeconds: 604800}, 2) != 7*24*time.Hour || runDelay(Config{IntervalSeconds: 604800}, 0) != 7*24*time.Hour {
		t.Fatal("backoff or normal schedule shortened the configured weekly interval")
	}
}

type failingMeta struct {
	*store.Store
	key string
}

func (s failingMeta) SetMeta(key string, v any) error {
	if key == s.key {
		return errors.New("disk full")
	}
	return s.Store.SetMeta(key, v)
}
func TestPersistenceFailureDoesNotPublishTentativeState(t *testing.T) {
	h, s, _ := testHub(t)
	quotes := exampleQuotes("public", testNow.Add(-30*time.Minute))
	h.fetchers["sina"] = func(context.Context, Config) (Batch, error) { return quoteBatch(quotes), nil }
	if _, err := h.Run(context.Background(), "sina", false); err != nil {
		t.Fatal(err)
	}
	before := cloneRecord(h.records["sina"])
	h.store = failingMeta{s, "datahub_record_sina"}
	quotes = exampleQuotes("public", testNow.Add(-29*time.Minute))
	quotes[0].Last = 9999
	advanceHubClock(h, 6*time.Second)
	if _, err := h.Run(context.Background(), "sina", false); err == nil {
		t.Fatal("disk error lost")
	}
	after := h.records["sina"]
	if !reflect.DeepEqual(before.Quotes, after.Quotes) || !reflect.DeepEqual(before.PriceHistory, after.PriceHistory) || !reflect.DeepEqual(before.Main, after.Main) || !after.LastSuccess.Equal(*before.LastSuccess) {
		t.Fatal("tentative state escaped failed persistence")
	}
	if after.ConsecutiveFailures != 1 || after.NextRun.Sub(h.now()) != 5*time.Second {
		t.Fatal("persistence failure not backed off")
	}
	var disk Record
	if err := s.GetMeta("datahub_record_sina", &disk); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.PriceHistory, disk.PriceHistory) || !disk.LastSuccess.Equal(*before.LastSuccess) {
		t.Fatal("failed write changed committed snapshot")
	}
	c := h.configs["sina"]
	h.store = failingMeta{s, "datahub_config_sina"}
	if _, err := h.Save("sina", ConfigRequest{Revision: c.Revision, Enabled: false, IntervalSeconds: 60}); err == nil {
		t.Fatal("config disk failure accepted")
	}
	if h.configs["sina"] != c {
		t.Fatal("failed config write changed memory")
	}
}
func TestCopiesAndTokenRotationRetainDataset(t *testing.T) {
	h, _, _ := testHub(t)
	endpoint := "https://example.com/data"
	saveBridge(t, h, "market_bridge", endpoint, "one")
	quotes := exampleQuotes(endpoint, testNow.Add(-30*time.Minute))
	h.fetchers["market_bridge"] = func(context.Context, Config) (Batch, error) { return quoteBatch(quotes), nil }
	out, err := h.Run(context.Background(), "market_bridge", false)
	if err != nil {
		t.Fatal(err)
	}
	r := h.records["market_bridge"]
	r.Main.History = []market.RollEvent{{NewSymbol: "c2701", EffectiveAt: testNow}}
	h.records["market_bridge"] = r
	before := cloneRecord(r)
	copied := cloneRecord(r)
	copied.Main.Snapshots[0].Quotes[0].Last = -1
	if r.Main.Snapshots[0].Quotes[0].Last == -1 {
		t.Fatal("snapshot quotes shallow copied")
	}
	out.Coverage[0] = "changed"
	*out.LatestDataAt = time.Time{}
	live := h.Live()
	live.Quotes[0].Last = -1
	live.PriceHistory["c2701"][0].Last = -1
	live.Main.History[0].NewSymbol = "bad"
	live.StateHistory[0].NewSymbol = "bad"
	*live.LastSuccess = time.Time{}
	*live.LatestDataAt = time.Time{}
	source := sourceByID(t, h, "market_bridge")
	source.Coverage[0] = "bad"
	*source.LastAttempt = time.Time{}
	*source.LastSuccess = time.Time{}
	*source.LatestDataAt = time.Time{}
	*source.NextRun = time.Time{}
	if !reflect.DeepEqual(before, h.records["market_bridge"]) {
		t.Fatal("public view mutated stored record")
	}
	saveBridge(t, h, "market_bridge", endpoint, "two")
	if sourceByID(t, h, "market_bridge").Freshness != "stale" {
		t.Fatal("rotated token is not marked unverified")
	}
	if _, err := h.Run(context.Background(), "market_bridge", false); err != nil {
		t.Fatal(err)
	}
	after := h.records["market_bridge"]
	if !reflect.DeepEqual(before.PriceHistory, after.PriceHistory) || !reflect.DeepEqual(before.Main.History, after.Main.History) || !reflect.DeepEqual(before.Main.Snapshots, after.Main.Snapshots) {
		t.Fatal("token rotation reset dataset")
	}
	if sourceByID(t, h, "market_bridge").Freshness == "stale" {
		t.Fatal("successful new credential stayed unverified")
	}
	saveBridge(t, h, "market_bridge", "https://new.example/data", "")
	quotes = exampleQuotes("https://new.example/data", testNow.Add(-29*time.Minute))
	if _, err := h.Run(context.Background(), "market_bridge", false); err != nil {
		t.Fatal(err)
	}
	if len(h.records["market_bridge"].Main.History) != 0 || len(h.records["market_bridge"].PriceHistory["c2701"]) != 1 {
		t.Fatal("different endpoint mixed histories")
	}
}
func TestRestartPreservesConfigDataAndFailureHealth(t *testing.T) {
	h, s, path := testHub(t)
	endpoint := "https://example.com/quotes"
	saveBridge(t, h, "market_bridge", endpoint, "persistent-secret")
	h.fetchers["market_bridge"] = func(context.Context, Config) (Batch, error) {
		return quoteBatch(exampleQuotes(endpoint, testNow.Add(-time.Minute))), nil
	}
	if _, err := h.Run(context.Background(), "market_bridge", false); err != nil {
		t.Fatal(err)
	}
	h.fetchers["market_bridge"] = func(context.Context, Config) (Batch, error) { return Batch{}, errors.New("offline") }
	advanceHubClock(h, 6*time.Second)
	if _, err := h.Run(context.Background(), "market_bridge", false); err == nil {
		t.Fatal("expected failure")
	}
	before := h.Live()
	cfg := h.configs["market_bridge"]
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := New(reopened)
	if err != nil {
		t.Fatal(err)
	}
	restored.now = h.now
	after := restored.Live()
	// Rolling request telemetry belongs to the running process, not source data.
	before.Polling = after.Polling
	if !reflect.DeepEqual(before, after) || restored.configs["market_bridge"] != cfg {
		t.Fatal("restart lost snapshot or credential/config")
	}
	if sourceByID(t, restored, "market_bridge").ConsecutiveFailures != 1 {
		t.Fatal("restart lost failure history")
	}
	saveBridge(t, restored, "market_bridge", "https://corrected.example/quotes", "")
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	last, err := New(again)
	if err != nil {
		t.Fatal(err)
	}
	if last.records["market_bridge"].NextRun != nil {
		t.Fatal("old endpoint backoff pinned corrected endpoint after restart")
	}
}

func TestPollRespectsDueTimeDisabledSourcesAndCancellation(t *testing.T) {
	h, s, _ := testHub(t)
	// Persist a future schedule and reconstruct the Hub before polling, so a
	// restart cannot turn a backoff or normal interval into an immediate fetch.
	for id, c := range h.configs {
		c.Enabled = id == "sina"
		if err := s.SetMeta("datahub_config_"+id, c); err != nil {
			t.Fatal(err)
		}
	}
	next := testNow.Add(time.Minute)
	if err := s.SetMeta("datahub_record_sina", Record{NextRun: &next, Binding: binding(h.configs["sina"]), ConfigRevision: h.configs["sina"].Revision}); err != nil {
		t.Fatal(err)
	}
	h, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(testNow.UnixNano())
	h.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	calls := make(chan string, 8)
	for id := range h.fetchers {
		id := id
		h.fetchers[id] = func(context.Context, Config) (Batch, error) {
			calls <- id
			return quoteBatch(exampleQuotes("public", h.now())), nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); h.Poll(ctx) }()
	select {
	case id := <-calls:
		t.Fatalf("future or disabled source fetched after restart: %s", id)
	case <-time.After(60 * time.Millisecond):
	}
	clock.Store(next.UnixNano())
	select {
	case id := <-calls:
		if id != "sina" {
			t.Fatalf("disabled source fetched: %s", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("due source did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poll did not stop on cancellation")
	}
	select {
	case id := <-calls:
		t.Fatalf("unexpected extra fetch: %s", id)
	default:
	}
	if sourceByID(t, h, "sina").LastSuccess == nil {
		t.Fatal("poll did not persist successful fetch")
	}
}

func TestRestartReevaluatesChangedIntervalWithoutEndpointChange(t *testing.T) {
	h, s, _ := testHub(t)
	initial := h.configs["sina"]
	if _, err := h.Save("sina", ConfigRequest{Revision: initial.Revision, Enabled: true, IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	h.fetchers["sina"] = func(context.Context, Config) (Batch, error) { return Batch{}, errors.New("offline") }
	for i := 0; i < 3; i++ {
		advanceHubClock(h, 6*time.Second)
		if _, err := h.Run(context.Background(), "sina", false); err == nil {
			t.Fatal("expected source failure")
		}
	}
	old := h.records["sina"]
	if old.NextRun.Sub(h.now()) != 8*time.Minute {
		t.Fatal("test requires a persisted long backoff")
	}
	c := h.configs["sina"]
	if _, err := h.Save("sina", ConfigRequest{Revision: c.Revision, Enabled: true, IntervalSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	// The old record is deliberately left intact on disk. The successful atomic
	// config write itself is enough for reconstruction to invalidate scheduling.
	var persisted Record
	if err := s.GetMeta("datahub_record_sina", &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.ConfigRevision != old.ConfigRevision || !persisted.NextRun.Equal(*old.NextRun) {
		t.Fatal("save unexpectedly rewrote source history")
	}
	restarted, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.records["sina"].NextRun != nil {
		t.Fatal("old backoff delayed the newly saved interval")
	}
	if restarted.records["sina"].ConsecutiveFailures != old.ConsecutiveFailures {
		t.Fatal("schedule invalidation erased failure history")
	}
	restarted.now = h.now
	restarted.fetchers["sina"] = func(context.Context, Config) (Batch, error) { return quoteBatch(exampleQuotes("public", testNow)), nil }
	if _, err := restarted.Run(context.Background(), "sina", false); err != nil {
		t.Fatal(err)
	}
	if err := s.GetMeta("datahub_record_sina", &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.ConfigRevision != c.Revision+1 || persisted.NextRun.Sub(h.now()) != 30*time.Second {
		t.Fatal("new run did not persist the current scheduling revision")
	}
}

func TestPausedPollReturnsWithoutFetching(t *testing.T) {
	_, s, _ := testHub(t)
	t.Setenv("CORNWATCH_AUTO_SYNC", "false")
	h, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	for id := range h.fetchers {
		h.fetchers[id] = func(context.Context, Config) (Batch, error) { calls.Add(1); return Batch{}, nil }
	}
	done := make(chan struct{})
	go func() { defer close(done); h.Poll(context.Background()) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("paused poll did not return")
	}
	if calls.Load() != 0 {
		t.Fatal("paused source fetched")
	}
}
