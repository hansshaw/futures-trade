package server

import (
	"errors"
	"net/http"
	"strconv"

	"cornwatch/internal/marketchart"
)

func (s *Server) marketHistory(w http.ResponseWriter, r *http.Request) {
	if s.Chart == nil {
		fail(w, 503, errors.New("历史图表服务尚未启用"))
		return
	}
	q := r.URL.Query()
	allowed := map[string]bool{"symbol": true, "interval": true, "source": true, "from": true, "to": true, "limit": true}
	for name, values := range q {
		if !allowed[name] || len(values) != 1 {
			fail(w, 400, errors.New("历史查询包含未知或重复参数"))
			return
		}
	}
	req := marketchart.Request{Symbol: q.Get("symbol"), Interval: q.Get("interval"), Source: q.Get("source"), From: q.Get("from"), To: q.Get("to")}
	if text := q.Get("limit"); text != "" {
		n, e := strconv.Atoi(text)
		if e != nil {
			fail(w, 400, errors.New("历史条数应为整数"))
			return
		}
		req.Limit = n
		if n == 0 {
			fail(w, 400, errors.New("历史条数需在1至6000之间"))
			return
		}
	}
	req, e := marketchart.Validate(req)
	if e != nil {
		fail(w, 400, e)
		return
	}
	report, e := s.Chart.History(r.Context(), req)
	if e != nil {
		fail(w, 502, e)
		return
	}
	write(w, 200, report)
}
