package journal

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func decode(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("JSON请求无效、字段不支持或请求超过大小限制")
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return errors.New("请求只能包含一个JSON对象")
	}
	return nil
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, ErrBusy):
		status = http.StatusTooManyRequests
	}
	write(w, status, map[string]string{"error": err.Error()})
}

// Register must be wrapped by the server's authentication middleware. Assets
// are returned as authenticated JSON, never through a public static file route.
func (s *Service) Register(mux *http.ServeMux, onChange func(string)) {
	changed := func(kind string) {
		if onChange != nil {
			onChange(kind)
		}
	}
	mux.HandleFunc("GET /api/journals", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.Journals(r.URL.Query().Get("q"), r.URL.Query().Get("month"), r.URL.Query().Get("archived") == "true")
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 200, items)
	})
	mux.HandleFunc("GET /api/journals/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, err := s.Journal(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 200, j)
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var j Journal
		if err := decode(w, r, &j, 128*1024); err != nil {
			fail(w, err)
			return
		}
		j, err := s.Save(j, r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		changed("journals")
		status := 200
		if r.PathValue("id") == "" {
			status = 201
		}
		write(w, status, j)
	}
	mux.HandleFunc("POST /api/journals", save)
	mux.HandleFunc("POST /api/journals/{id}", save)
	mux.HandleFunc("POST /api/journals/{id}/archive", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Archived bool   `json:"archived"`
			Revision uint64 `json:"revision"`
		}
		if err := decode(w, r, &req, 1024); err != nil {
			fail(w, err)
			return
		}
		j, err := s.Archive(r.PathValue("id"), req.Revision, req.Archived)
		if err != nil {
			fail(w, err)
			return
		}
		changed("journals")
		write(w, 200, j)
	})
	mux.HandleFunc("POST /api/journal-assets", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name    string `json:"name"`
			DataURL string `json:"data_url"`
		}
		if err := decode(w, r, &req, 6*1024*1024); err != nil {
			fail(w, err)
			return
		}
		a, err := s.AddAsset(req.Name, req.DataURL)
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 201, a)
	})
	mux.HandleFunc("GET /api/journal-assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		a, err := s.Asset(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 200, a)
	})
	mux.HandleFunc("POST /api/journal-reviews", func(w http.ResponseWriter, r *http.Request) {
		var req ReviewRequest
		if err := decode(w, r, &req, 8192); err != nil {
			fail(w, err)
			return
		}
		result, err := s.Review(r.Context(), req)
		if err != nil {
			fail(w, err)
			return
		}
		changed("journal-reviews")
		write(w, 201, result)
	})
	mux.HandleFunc("GET /api/journal-reviews", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.Reviews()
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 200, items)
	})
	mux.HandleFunc("GET /api/journal-reviews/{id}/snapshot", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := s.ReviewSnapshot(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 200, snapshot)
	})
	mux.HandleFunc("POST /api/journal-reviews/{id}/lessons/{lesson_id}/accept", func(w http.ResponseWriter, r *http.Request) {
		if err := decode(w, r, &struct{}{}, 1024); err != nil {
			fail(w, err)
			return
		}
		lesson, err := s.Accept(r.PathValue("id"), r.PathValue("lesson_id"))
		if err != nil {
			fail(w, err)
			return
		}
		changed("journal-lessons")
		write(w, 200, lesson)
	})
	mux.HandleFunc("GET /api/journal-lessons", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.Lessons()
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 200, items)
	})
	mux.HandleFunc("POST /api/journal-lessons/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		if err := decode(w, r, &struct{}{}, 1024); err != nil {
			fail(w, err)
			return
		}
		lesson, err := s.Revoke(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		changed("journal-lessons")
		write(w, 200, lesson)
	})
}
