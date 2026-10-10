package bcp

import (
	"os"
	"slices"
	"testing"
)

func TestParseYear(t *testing.T) {
	f, err := os.Open("../../testdata/bcp-historica-2025-usd.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	list, err := ParseYear(f, 2025, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 200 || len(list) > 260 {
		t.Errorf("got %d business days, want roughly 250", len(list))
	}
	byDate := map[string]string{}
	for _, r := range list {
		byDate[r.Date] = string(r.Reference)
		if r.Source != "bcp" || r.Currency != "USD" || r.Buy != "" {
			t.Fatalf("unexpected rate %+v", r)
		}
	}
	if byDate["2025-04-01"] != "8008.23" {
		t.Errorf("2025-04-01 = %q; want 8008.23", byDate["2025-04-01"])
	}
	if byDate["2025-01-02"] != "7865.88" {
		t.Errorf("2025-01-02 = %q; want 7865.88", byDate["2025-01-02"])
	}
	if _, ok := byDate["2025-01-01"]; ok {
		t.Error("2025-01-01 is ND and should be missing")
	}
	if list[0].Date > list[len(list)-1].Date {
		t.Error("rates should be sorted by date")
	}
}

func TestParseYearChecksTheYear(t *testing.T) {
	f, err := os.Open("../../testdata/bcp-historica-2025-usd.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := ParseYear(f, 2024, "USD"); err == nil {
		t.Error("expected an error when the page is for another year")
	}
}

func TestParseForm(t *testing.T) {
	page, err := os.ReadFile("../../testdata/bcp-historica-form.html")
	if err != nil {
		t.Fatal(err)
	}
	form, err := ParseForm(page)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(form.Years, 2026) || !slices.Contains(form.Years, 2002) || !slices.IsSorted(form.Years) {
		t.Errorf("years = %v", form.Years)
	}
	for _, c := range []string{"USD", "EUR", "BRL", "ARS"} {
		if !slices.Contains(form.Currencies, c) {
			t.Errorf("missing currency %s in %v", c, form.Currencies)
		}
	}
}
