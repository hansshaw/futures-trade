package datahub

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
)

func send(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func replyError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, ErrBusy) {
		status = 429
	}
	if errors.Is(err, ErrConflict) {
		status = 409
	}
	if errors.Is(err, os.ErrNotExist) {
		status = 404
	}
	send(w, status, map[string]string{"error": err.Error()})
}
func (h *Hub) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/data-sources", func(w http.ResponseWriter, r *http.Request) {
		live := h.Live()
		send(w, 200, map[string]any{"sources": h.Sources(), "active_market_source": live.SourceID, "auto_sync_paused": h.Paused()})
	})
	mux.HandleFunc("GET /api/market/live", func(w http.ResponseWriter, r *http.Request) { send(w, 200, h.Live()) })
	mux.HandleFunc("POST /api/data-sources/{id}/config", func(w http.ResponseWriter, r *http.Request) {
		var request ConfigRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			send(w, 400, map[string]string{"error": "配置JSON格式无效"})
			return
		}
		result, err := h.Save(r.PathValue("id"), request)
		if err != nil {
			replyError(w, err)
			return
		}
		send(w, 200, result)
	})
	for _, action := range []string{"test", "sync"} {
		action := action
		mux.HandleFunc("POST /api/data-sources/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			out, err := h.Run(r.Context(), r.PathValue("id"), action == "test")
			if err != nil {
				replyError(w, err)
				return
			}
			send(w, 200, out)
		})
	}
}
