package datahub

import (
	"context"
	"fmt"
	"os"
	"time"
)

// SaveWhenIdle gives a user configuration update priority over the next poll.
// An in-flight request finishes against its original config; no new requests
// for this source start while the save waits. Other sources remain independent.
// Direct Save keeps its immediate ErrBusy contract for non-HTTP callers.
func (h *Hub) SaveWhenIdle(ctx context.Context, id string, request ConfigRequest) (Source, error) {
	return h.saveWhenIdle(ctx, id, request, 20*time.Second)
}

func (h *Hub) saveWhenIdle(ctx context.Context, id string, request ConfigRequest, maxWait time.Duration) (Source, error) {
	if err := ctx.Err(); err != nil {
		return Source{}, err
	}
	h.mu.Lock()
	if _, ok := h.definition(id); !ok {
		h.mu.Unlock()
		return Source{}, os.ErrNotExist
	}
	if h.pending[id] {
		h.mu.Unlock()
		return Source{}, fmt.Errorf("%w：已有该源配置等待保存", ErrBusy)
	}
	if request.Revision != h.configs[id].Revision {
		h.mu.Unlock()
		return Source{}, ErrConflict
	}
	h.pending[id] = true
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.pending, id); h.mu.Unlock() }()
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return Source{}, err
		}
		h.mu.Lock()
		busy := h.busy[id]
		h.mu.Unlock()
		if !busy {
			return h.Save(id, request)
		}
		select {
		case <-ctx.Done():
			return Source{}, ctx.Err()
		case <-timer.C:
			return Source{}, fmt.Errorf("%w：等待当前请求结束超时，配置未修改，编辑可保留后重试", ErrBusy)
		case <-poll.C:
		}
	}
}
