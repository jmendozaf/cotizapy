package set

import (
	"os"
	"strings"
	"testing"

	"github.com/jmendozaf/cotizapy/internal/rates"
)

func TestParse(t *testing.T) {
	f, err := os.Open("../../testdata/set-cotizaciones.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	list, warnings, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	byKey := map[string]rates.Rate{}
	for _, r := range list {
		byKey[r.Currency+" "+r.Date] = r
	}
	// Octubre 2026 (partial), Septiembre 2026 and Enero 2010, six currencies each.
	if want := (9 + 30 + 31) * 6; len(list) != want {
		t.Errorf("got %d rates, want %d", len(list), want)
	}
	check := func(key string, buy, sell rates.Decimal) {
		t.Helper()
		r, ok := byKey[key]
		if !ok {
			t.Fatalf("missing %s", key)
		}
		if r.Buy != buy || r.Sell != sell || r.Source != rates.SET {
			t.Errorf("%s = %+v; want buy %s sell %s", key, r, buy, sell)
		}
	}
	check("USD 2026-10-09", "5678.11", "5698.34")
	check("GBP 2026-10-09", "7510.43", "7537.77")
	check("BRL 2026-10-01", "1120.66", "1122.33")
	check("USD 2026-09-30", "5817.29", "5826.74")
	check("EUR 2010-01-31", "6418.53", "6660.35")
}

func TestParseRejectsUnknownPage(t *testing.T) {
	f, err := os.Open("../../testdata/bcp-historica-2025-usd.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, _, err := Parse(f); err == nil {
		t.Error("expected an error for a page without monthly tables")
	}
}

func TestParseKeepsTheLaterDuplicateRow(t *testing.T) {
	// Abril 2021 on the real page lists 24, 25 and 26 twice; the second 26 is the corrected Monday.
	page := `<h4>Tipos de cambios del mes de Abril 2021</h4><table>
		<tr><th></th><th>DÓLAR</th></tr><tr><td></td><td>Compra</td><td>Venta</td></tr>
		<tr><td>23</td><td>6.492,51</td><td>6.510,59</td></tr>
		<tr><td>26</td><td>6.492,51</td><td>6.510,59</td></tr>
		<tr><td>26</td><td>6.466,03</td><td>6.471,34</td></tr>
		<tr><td>27</td><td>6.493,50</td><td>6.505,92</td></tr></table>`
	list, warnings, err := Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[1].Date != "2021-04-26" || list[1].Buy != "6466.03" {
		t.Errorf("rates = %+v", list)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v; want one about 2021-04-26", warnings)
	}
}
