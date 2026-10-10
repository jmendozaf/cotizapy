// Package api serves the rates over HTTP.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/jmendozaf/cotizapy/internal/rates"
	"github.com/jmendozaf/cotizapy/internal/store"
)

// MaxHistoryDays caps a history request.
const MaxHistoryDays = 3700

// Server answers HTTP requests against a store.
type Server struct {
	st  *store.Store
	log *slog.Logger
}

// New returns a server for st.
func New(st *store.Store, log *slog.Logger) *Server {
	return &Server{st: st, log: log}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /v1/latest", s.latest)
	mux.HandleFunc("GET /v1/series", s.series)
	mux.HandleFunc("GET /v1/{currency}", s.both)
	mux.HandleFunc("GET /v1/{source}/{currency}", s.one)
	mux.HandleFunc("GET /v1/{source}/{currency}/history", s.history)
	return cors(mux)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) latest(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.Latest(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (s *Server) series(w http.ResponseWriter, r *http.Request) {
	series, err := s.st.Series(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	synced := map[string]string{}
	for _, source := range rates.Sources {
		if v, err := s.st.Meta(r.Context(), "synced_at:"+source); err == nil && v != "" {
			synced[source] = v
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": series, "synced_at": synced})
}

// both answers with the quote of every source for one currency.
func (s *Server) both(w http.ResponseWriter, r *http.Request) {
	currency := rates.NormalizeCurrency(r.PathValue("currency"))
	date, ok := dateParam(w, r)
	if !ok {
		return
	}
	out := map[string]any{"currency": currency, "requested_date": date}
	found := false
	for _, source := range rates.Sources {
		q, err := s.st.Find(r.Context(), source, currency, date)
		switch {
		case errors.Is(err, store.ErrNotFound):
			out[source] = nil
		case err != nil:
			s.fail(w, err)
			return
		default:
			out[source] = q
			found = true
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "no quote for that currency and date")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) one(w http.ResponseWriter, r *http.Request) {
	source, ok := sourceParam(w, r)
	if !ok {
		return
	}
	date, ok := dateParam(w, r)
	if !ok {
		return
	}
	q, err := s.st.Find(r.Context(), source, rates.NormalizeCurrency(r.PathValue("currency")), date)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no quote for that currency and date")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	source, ok := sourceParam(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	to := q.Get("to")
	if to == "" {
		to = rates.Today()
	}
	from := q.Get("from")
	if from == "" {
		from = shift(to, -30)
	}
	if !rates.ValidDate(from) || !rates.ValidDate(to) {
		writeError(w, http.StatusBadRequest, "invalid_date", "from and to must be YYYY-MM-DD")
		return
	}
	if from > to || from < shift(to, -MaxHistoryDays) {
		writeError(w, http.StatusBadRequest, "invalid_range", "from must be before to and at most ~10 years apart")
		return
	}
	list, err := s.st.Range(r.Context(), source, rates.NormalizeCurrency(r.PathValue("currency")), from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list})
}

func shift(date string, days int) string {
	t, _ := time.Parse(time.DateOnly, date)
	return t.AddDate(0, 0, days).Format(time.DateOnly)
}

func sourceParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	source := r.PathValue("source")
	if !slices.Contains(rates.Sources, source) {
		writeError(w, http.StatusNotFound, "unknown_source", "source must be set or bcp")
		return "", false
	}
	return source, true
}

func dateParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	date := r.URL.Query().Get("date")
	if date == "" {
		return rates.Today(), true
	}
	if !rates.ValidDate(date) {
		writeError(w, http.StatusBadRequest, "invalid_date", "date must be YYYY-MM-DD")
		return "", false
	}
	return date, true
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	s.log.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "unexpected error")
}

func writeError(w http.ResponseWriter, code int, kind, message string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": message})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
