package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jmendozaf/cotizapy/internal/rates"
	"github.com/jmendozaf/cotizapy/internal/store"
)

func server(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "cotiza.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.Upsert(context.Background(), []rates.Rate{
		{Source: rates.SET, Currency: "USD", Date: "2026-10-09", Buy: "5678.11", Sell: "5698.34"},
		{Source: rates.SET, Currency: "USD", Date: "2026-10-10", Buy: "5678.11", Sell: "5698.34"},
		{Source: rates.BCP, Currency: "USD", Date: "2026-10-09", Reference: "5694.47"},
	})
	srv := httptest.NewServer(New(st, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string, wantStatus int) map[string]any {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s = %d; want %d", path, resp.StatusCode, wantStatus)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	return body
}

func TestOneSourceFallsBackOnWeekend(t *testing.T) {
	srv := server(t)
	body := get(t, srv, "/v1/bcp/usd?date=2026-10-10", http.StatusOK)
	if body["date"] != "2026-10-09" || body["requested_date"] != "2026-10-10" || body["exact"] != false || body["reference"] != 5694.47 {
		t.Errorf("body = %v", body)
	}
}

func TestBothSources(t *testing.T) {
	srv := server(t)
	body := get(t, srv, "/v1/USD?date=2026-10-10", http.StatusOK)
	set, _ := body["set"].(map[string]any)
	bcp, _ := body["bcp"].(map[string]any)
	if set["exact"] != true || set["sell"] != 5698.34 || bcp["date"] != "2026-10-09" {
		t.Errorf("body = %v", body)
	}
}

func TestErrors(t *testing.T) {
	srv := server(t)
	get(t, srv, "/v1/bcp/usd?date=10/10/2026", http.StatusBadRequest)
	get(t, srv, "/v1/xyz/usd", http.StatusNotFound)
	get(t, srv, "/v1/bcp/jpy?date=2026-10-09", http.StatusNotFound)
	get(t, srv, "/v1/set/usd/history?from=2026-10-10&to=2026-10-01", http.StatusBadRequest)
}

func TestHistoryAndLatest(t *testing.T) {
	srv := server(t)
	body := get(t, srv, "/v1/set/usd/history?from=2026-10-01&to=2026-10-31", http.StatusOK)
	if data, _ := body["data"].([]any); len(data) != 2 {
		t.Errorf("history = %v", body)
	}
	body = get(t, srv, "/v1/latest", http.StatusOK)
	if data, _ := body["data"].([]any); len(data) != 2 {
		t.Errorf("latest = %v", body)
	}
}
