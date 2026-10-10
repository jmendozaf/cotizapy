package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jmendozaf/cotizapy/internal/rates"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "cotiza.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUpsertCountsOnlyChanges(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	list := []rates.Rate{
		{Source: rates.BCP, Currency: "USD", Date: "2026-10-08", Reference: "5690.10"},
		{Source: rates.BCP, Currency: "USD", Date: "2026-10-09", Reference: "5694.47"},
	}
	if n, err := st.Upsert(ctx, list); err != nil || n != 2 {
		t.Fatalf("first upsert = %d, %v; want 2", n, err)
	}
	if n, err := st.Upsert(ctx, list); err != nil || n != 0 {
		t.Fatalf("same data = %d, %v; want 0", n, err)
	}
	list[1].Reference = "5694.50" // the BCP corrected a quote
	if n, err := st.Upsert(ctx, list); err != nil || n != 1 {
		t.Fatalf("correction = %d, %v; want 1", n, err)
	}
}

func TestFindFallsBackToLastPublishedQuote(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	st.Upsert(ctx, []rates.Rate{
		{Source: rates.BCP, Currency: "USD", Date: "2026-10-08", Reference: "5690.10"},
		{Source: rates.BCP, Currency: "USD", Date: "2026-10-09", Reference: "5694.47"}, // viernes
	})

	q, err := st.Find(ctx, rates.BCP, "USD", "2026-10-09")
	if err != nil || !q.Exact || q.Reference != "5694.47" {
		t.Fatalf("exact = %+v, %v", q, err)
	}
	q, err = st.Find(ctx, rates.BCP, "USD", "2026-10-11") // domingo
	if err != nil || q.Exact || q.Date != "2026-10-09" || q.RequestedDate != "2026-10-11" {
		t.Fatalf("sunday = %+v, %v", q, err)
	}
	if _, err := st.Find(ctx, rates.BCP, "USD", "2026-10-30"); !errors.Is(err, ErrNotFound) {
		t.Errorf("too far from the last quote: err = %v; want ErrNotFound", err)
	}
	if _, err := st.Find(ctx, rates.BCP, "USD", "2026-10-07"); !errors.Is(err, ErrNotFound) {
		t.Errorf("before the first quote: err = %v; want ErrNotFound", err)
	}
	if _, err := st.Find(ctx, rates.SET, "USD", "2026-10-09"); !errors.Is(err, ErrNotFound) {
		t.Errorf("other source: err = %v; want ErrNotFound", err)
	}
}

func TestLatestAndSeries(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	st.Upsert(ctx, []rates.Rate{
		{Source: rates.SET, Currency: "USD", Date: "2026-10-08", Buy: "1", Sell: "2"},
		{Source: rates.SET, Currency: "USD", Date: "2026-10-09", Buy: "3", Sell: "4"},
		{Source: rates.BCP, Currency: "EUR", Date: "2026-10-01", Reference: "5"},
	})
	latest, err := st.Latest(ctx)
	if err != nil || len(latest) != 2 || latest[0].Source != rates.BCP || latest[1].Date != "2026-10-09" {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
	series, err := st.Series(ctx)
	if err != nil || len(series) != 2 || series[1].Count != 2 || series[1].First != "2026-10-08" || series[1].Name == "" {
		t.Fatalf("series = %+v, %v", series, err)
	}
}
