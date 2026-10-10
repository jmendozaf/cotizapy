package export

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmendozaf/cotizapy/internal/rates"
	"github.com/jmendozaf/cotizapy/internal/store"
)

func TestWrite(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cotiza.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Upsert(ctx, []rates.Rate{
		{Source: rates.SET, Currency: "USD", Date: "2025-12-31", Buy: "6900", Sell: "6910.5"},
		{Source: rates.SET, Currency: "USD", Date: "2026-10-09", Buy: "5678.11", Sell: "5698.34"},
		{Source: rates.BCP, Currency: "USD", Date: "2026-10-09", Reference: "5694.47"},
		{Source: rates.BCP, Currency: "CHF", Date: "2026-10-09", Reference: "7100.12"},
	})
	dir := t.TempDir()
	first := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if _, err := Write(ctx, st, dir, first); err != nil {
		t.Fatal(err)
	}

	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var year struct {
		Rates map[string]map[string]json.Number `json:"rates"`
	}
	json.Unmarshal([]byte(read("set/USD/2026.json")), &year)
	if year.Rates["2026-10-09"]["sell"] != "5698.34" {
		t.Errorf("2026.json = %v", year)
	}
	if got := read("set/USD.csv"); got != "date,buy,sell\n2025-12-31,6900,6910.5\n2026-10-09,5678.11,5698.34\n" {
		t.Errorf("USD.csv = %q", got)
	}
	if got := read("bcp/USD/2026.csv"); got != "date,reference\n2026-10-09,5694.47\n" {
		t.Errorf("bcp 2026.csv = %q", got)
	}
	if !strings.Contains(read("index.html"), "5.698,34") {
		t.Error("index.html should show the SET sell rate formatted in guaraníes")
	}
	if strings.Contains(read("index.html"), "CHF") {
		t.Error("index.html should only list the DNIT currencies")
	}
	if !strings.Contains(read("bcp/CHF.csv"), "7100.12") {
		t.Error("BCP-only currencies should still be exported")
	}
	if !strings.Contains(read("latest.json"), `"reference": 5694.47`) {
		t.Errorf("latest.json = %s", read("latest.json"))
	}

	// Nothing new: a second export a day later rewrites nothing, not even the timestamps.
	stats, err := Write(ctx, st, dir, first.Add(24*time.Hour))
	if err != nil || stats.Written != 0 {
		t.Errorf("second export wrote %d files (%v); want 0", stats.Written, err)
	}

	st.Upsert(ctx, []rates.Rate{{Source: rates.SET, Currency: "USD", Date: "2026-10-10", Buy: "5678.11", Sell: "5698.34"}})
	if _, err := Write(ctx, st, dir, first.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read("index.json"), "2026-10-12T12:00:00Z") {
		t.Error("index.json should carry the new timestamp after new data")
	}
}

func TestFormatGuaranies(t *testing.T) {
	for in, want := range map[rates.Decimal]string{"5694.47": "5.694,47", "35.96": "35,96", "1": "1", "123456": "123.456", "": "—"} {
		if got := formatGuaranies(in); got != want {
			t.Errorf("formatGuaranies(%q) = %q; want %q", in, got, want)
		}
	}
}
