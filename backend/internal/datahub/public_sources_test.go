package datahub

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"cornwatch/internal/domain"
	"cornwatch/internal/store"
)

func TestPublicEnterpriseAndImportSourcesRegistered(t *testing.T) {
	h, _, _ := testHub(t)
	for _, id := range []string{"boyar_prices", "boyar_arrivals", "import_auction"} {
		t.Run(id, func(t *testing.T) {
			d, ok := h.definition(id)
			if !ok || h.fetchers[id] == nil {
				t.Fatalf("source %s not registered", id)
			}
			source := sourceByID(t, h, id)
			if d.Kind != "fundamental" || d.ConfigurableEndpoint || d.AccountRequired || !source.Config.Enabled || source.Config.TokenConfigured || source.Config.Endpoint != "" {
				t.Fatalf("not a public independent source: %+v", source)
			}
			if d.MinIntervalSeconds < 3600 || d.DefaultInterval < d.MinIntervalSeconds || d.DefaultInterval > d.MaxIntervalSeconds {
				t.Fatal("source lacks bounded low-frequency schedule")
			}
			if source.LastSuccess != nil || source.LatestDataAt != nil || source.Freshness != "never" {
				t.Fatal("new source claims unseen data")
			}
		})
	}
	d, _ := h.definition("import_auction")
	if d.DefaultInterval != 86400 {
		t.Fatal("auction should check daily by default")
	}
	if !strings.Contains(sourceByID(t, h, "boyar_prices").LicenseNote, "单位") || !strings.Contains(sourceByID(t, h, "boyar_arrivals").Description, "待卸") {
		t.Fatal("public source disclosure missing")
	}
}
func TestNoAuctionPublicationPreservesPriorDataAndRecordsHealthyCheck(t *testing.T) {
	h, s, path := testHub(t)
	stamp := testNow.Add(-24 * time.Hour)
	o := domain.Observation{ID: "auction-result", Kind: "import_auction_rate", Entity: "进口玉米竞价销售成交率", Region: "全国", Value: 56.07, Unit: "%", ObservedAt: stamp, AvailableAt: testNow, Source: "中储粮公开结果"}
	e := domain.Event{ID: "auction-event", Title: "进口玉米拍卖结果", Category: "import_auction", PublishedAt: stamp, AvailableAt: testNow}
	h.fetchers["import_auction"] = func(context.Context, Config) (Batch, error) {
		return observationBatch([]domain.Observation{o}, []domain.Event{e}), nil
	}
	if _, err := h.Run(context.Background(), "import_auction", false); err != nil {
		t.Fatal(err)
	}
	before := sourceByID(t, h, "import_auction")
	h.fetchers["import_auction"] = func(context.Context, Config) (Batch, error) { return Batch{}, errors.New("temporary outage") }
	if _, err := h.Run(context.Background(), "import_auction", false); err == nil {
		t.Fatal("expected failed attempt")
	}
	checked := testNow.Add(3 * 24 * time.Hour) // Saturday: no result is distinct from a zero trade.
	h.now = func() time.Time { return checked }
	message := "本次公开栏目未发现进口玉米成交结果；不以零成交入库，保留以前的场次。"
	h.fetchers["import_auction"] = func(context.Context, Config) (Batch, error) { return Batch{NoPublication: message}, nil }
	recordBeforeTest := cloneRecord(h.records["import_auction"])
	tested, err := h.Run(context.Background(), "import_auction", true)
	if err != nil || !tested.OK || tested.RecordCount != 0 || !strings.Contains(tested.Message, message) || !reflect.DeepEqual(recordBeforeTest, h.records["import_auction"]) {
		t.Fatalf("empty-publication test mutated state: %+v %v", tested, err)
	}
	out, err := h.Run(context.Background(), "import_auction", false)
	if err != nil || !out.OK || out.RecordCount != 0 || out.Added != 0 || out.Duplicates != 0 || out.LatestDataAt != nil {
		t.Fatalf("no-publication outcome: %+v %v", out, err)
	}
	got := sourceByID(t, h, "import_auction")
	if got.Status != "ok" || got.ConsecutiveFailures != 0 || got.LastError != "" || !got.LastSuccess.Equal(checked) || !got.LastAttempt.Equal(checked) {
		t.Fatalf("healthy check not distinguished: %+v", got)
	}
	if !got.LatestDataAt.Equal(*before.LatestDataAt) || !reflect.DeepEqual(got.Coverage, before.Coverage) || !strings.Contains(got.FreshnessNote, message) || !got.NextRun.Equal(checked.Add(24*time.Hour)) {
		t.Fatalf("prior data or schedule lost: %+v", got)
	}
	observations, err := s.Observations()
	if err != nil || len(observations) != 1 || observations[0].Value != 56.07 || !observations[0].AvailableAt.Equal(testNow) {
		t.Fatalf("fabricated/overwrote auction data: %+v %v", observations, err)
	}
	events, _ := s.Events()
	bars, _ := s.Bars("", false)
	if len(events) != 1 || len(bars) != 0 {
		t.Fatal("empty publication generated event or market bars")
	}
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
	after := sourceByID(t, restored, "import_auction")
	if !after.LastSuccess.Equal(checked) || !after.LatestDataAt.Equal(stamp) || !strings.Contains(after.FreshnessNote, message) {
		t.Fatal("no-publication health lost on restart")
	}
}
func TestFirstEmptyAuctionWindowIsHealthyButHasNoDataTimestamp(t *testing.T) {
	h, s, _ := testHub(t)
	h.fetchers["import_auction"] = func(context.Context, Config) (Batch, error) {
		return Batch{NoPublication: "本期没有公开场次；没有填零。"}, nil
	}
	out, err := h.Run(context.Background(), "import_auction", false)
	if err != nil || !out.OK {
		t.Fatalf("valid empty window failed: %v", err)
	}
	source := sourceByID(t, h, "import_auction")
	if source.Status != "ok" || source.LastSuccess == nil || source.LatestDataAt != nil || source.Freshness != "unknown" || !strings.Contains(source.FreshnessNote, "没有填零") {
		t.Fatalf("invented data freshness: %+v", source)
	}
	observations, _ := s.Observations()
	events, _ := s.Events()
	if len(observations) != 0 || len(events) != 0 {
		t.Fatal("empty window inserted zero placeholder")
	}
	// An ordinary empty fetch is still an error; a transport failure cannot be
	// hidden by merely attaching a no-publication explanation.
	for _, failure := range []bool{false, true} {
		h.fetchers["import_auction"] = func(context.Context, Config) (Batch, error) {
			if failure {
				return Batch{NoPublication: "no results"}, errors.New("network failed")
			}
			return Batch{}, nil
		}
		if _, err := h.Run(context.Background(), "import_auction", false); err == nil {
			t.Fatal("invalid empty fetch accepted")
		}
	}
}
func TestPublicDailyAndAuctionFreshnessFollowPublicationCadence(t *testing.T) {
	now := testNow
	for _, tc := range []struct {
		id               string
		age              time.Duration
		want, disclosure string
	}{
		{"boyar_prices", 3 * 24 * time.Hour, "fresh", "不是当前厂门实时"},
		{"boyar_arrivals", 4*24*time.Hour + time.Second, "stale", "超过4天"},
		{"import_auction", 13 * 24 * time.Hour, "fresh", "不定期拍卖"},
		{"import_auction", 15 * 24 * time.Hour, "stale", "无新公告不等于成交为零"},
	} {
		t.Run(tc.id+tc.want, func(t *testing.T) {
			dataAt := now.Add(-tc.age)
			record := Record{LastSuccess: &now, LatestDataAt: &dataAt}
			state, note := freshness(tc.id, record, now)
			if state != tc.want || !strings.Contains(note, tc.disclosure) {
				t.Fatalf("freshness %s / %s", state, note)
			}
		})
	}
}
