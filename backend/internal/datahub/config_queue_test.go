package datahub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"cornwatch/internal/domain"
)

func waitPending(t *testing.T, h *Hub, id string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		h.mu.Lock()
		pending := h.pending[id]
		h.mu.Unlock()
		if pending {
			return
		}
		select {
		case <-deadline:
			t.Fatal("save never gained priority")
		case <-time.After(time.Millisecond):
		}
	}
}

func blockQuote(t *testing.T, h *Hub) (func(), <-chan error) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	h.fetchers["sina"] = func(ctx context.Context, _ Config) (Batch, error) {
		close(entered)
		select {
		case <-release:
			return quoteBatch(exampleQuotes("public", testNow)), nil
		case <-ctx.Done():
			return Batch{}, ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { _, err := h.RunScheduled(context.Background(), "sina"); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("quote not started")
	}
	return unblock, done
}

func TestHTTPConfigSavePrioritizesDisableWithoutStallingOtherSources(t *testing.T) {
	h, _, _ := testHub(t)
	unblock, quoteDone := blockQuote(t, h)
	mux := http.NewServeMux()
	h.Register(mux)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/data-sources/sina/config", strings.NewReader(`{"revision":1,"enabled":false,"interval_seconds":0.5,"endpoint":""}`))
	saved := make(chan struct{})
	go func() { mux.ServeHTTP(response, request); close(saved) }()
	waitPending(t, h, "sina")
	for i := 0; i < 10; i++ {
		if _, err := h.RunScheduled(context.Background(), "sina"); !errors.Is(err, ErrBusy) {
			t.Fatal("queued save did not block next auto fetch")
		}
	}
	h.fetchers["liaoning"] = func(context.Context, Config) (Batch, error) {
		return observationBatch([]domain.Observation{exampleObservation()}, nil), nil
	}
	if _, err := h.RunScheduled(context.Background(), "liaoning"); err != nil {
		t.Fatalf("unrelated source stalled: %v", err)
	}
	unblock()
	if err := <-quoteDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-saved:
	case <-time.After(time.Second):
		t.Fatal("configuration was starved")
	}
	if response.Code != 200 {
		t.Fatalf("save HTTP%d: %s", response.Code, response.Body.String())
	}
	h.mu.Lock()
	cfg, pending := h.configs["sina"], h.pending["sina"]
	h.mu.Unlock()
	if cfg.Enabled || cfg.Revision != 2 || pending {
		t.Fatalf("disable not committed %+v pending=%v", cfg, pending)
	}
	h.fetchers["sina"] = func(context.Context, Config) (Batch, error) { t.Fatal("disabled source fetched"); return Batch{}, nil }
	if out, err := h.RunScheduled(context.Background(), "sina"); err != nil || !out.Skipped {
		t.Fatal("disabled source not skipped")
	}
}

func TestQueuedSaveCancelAndTimeoutReleasePriorityWithoutMutation(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			h, _, _ := testHub(t)
			unblock, quoteDone := blockQuote(t, h)
			before := h.configs["sina"]
			request := ConfigRequest{Revision: before.Revision, Enabled: false, IntervalSeconds: .5}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			wait := time.Second
			if mode == "timeout" {
				wait = 50 * time.Millisecond
			}
			go func() { _, err := h.saveWhenIdle(ctx, "sina", request, wait); done <- err }()
			waitPending(t, h, "sina")
			if _, err := h.SaveWhenIdle(context.Background(), "sina", request); !errors.Is(err, ErrBusy) {
				t.Fatal("second config did not preserve queue priority")
			}
			if mode == "cancel" {
				cancel()
			}
			var err error
			select {
			case err = <-done:
			case <-time.After(time.Second):
				t.Fatal("save cancellation/timeout hung")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) || mode == "timeout" && !errors.Is(err, ErrBusy) {
				t.Fatalf("wrong stop cause %v", err)
			}
			h.mu.Lock()
			pending, cfg := h.pending["sina"], h.configs["sina"]
			h.mu.Unlock()
			if pending || cfg != before {
				t.Fatal("aborted wait changed config or left source paused")
			}
			unblock()
			if err := <-quoteDone; err != nil {
				t.Fatal(err)
			}
			if _, err := h.SaveWhenIdle(context.Background(), "sina", request); err != nil {
				t.Fatalf("retry failed: %v", err)
			}
			if _, err := h.SaveWhenIdle(context.Background(), "sina", request); !errors.Is(err, ErrConflict) {
				t.Fatalf("queued save bypassed revision protection: %v", err)
			}
		})
	}
}
