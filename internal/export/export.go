// Package export writes the database as a static dataset (JSON and CSV files) meant to be served
// from GitHub Pages or any static host, so the rates can be used without running anything.
//
//	index.json                     sources and series (first/last date, count)
//	latest.json                    last rate of every source and currency
//	{source}/{CUR}/latest.json     last rate of one currency
//	{source}/{CUR}/{year}.json     one year, keyed by date
//	{source}/{CUR}/{year}.csv      one year
//	{source}/{CUR}.csv             full history
package export

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jmendozaf/cotizapy/internal/rates"
	"github.com/jmendozaf/cotizapy/internal/store"
)

// Source describes a data source in index.json.
type Source struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Fields      string `json:"fields"`
}

// SourceInfo documents both sources.
var SourceInfo = map[string]Source{
	rates.SET: {
		Name:        "DNIT",
		URL:         "https://www.dnit.gov.py/web/portal-institucional/cotizaciones",
		Description: "Tipos de cambio que publica la DNIT para fines tributarios. Incluye todos los días del calendario: fines de semana y feriados repiten el último día hábil.",
		Fields:      "buy, sell",
	},
	rates.BCP: {
		Name:        "Banco Central del Paraguay",
		URL:         "https://www.bcp.gov.py/webapps/web/cotizacion/monedas-historica",
		Description: "Cotización referencial diaria del BCP. Solo días hábiles.",
		Fields:      "reference",
	},
}

// Stats counts what an export wrote.
type Stats struct {
	Files   int `json:"files"`
	Written int `json:"written"`
}

// Write exports the whole database into dir. Files whose content did not change are left untouched.
func Write(ctx context.Context, st *store.Store, dir string, now time.Time) (Stats, error) {
	w := &writer{dir: dir}
	series, err := st.Series(ctx)
	if err != nil {
		return Stats{}, err
	}
	updated := now.UTC().Format(time.RFC3339)

	// The timestamps live only in index.json and latest.json, and only change when the data does,
	// so a sync that finds nothing new leaves the whole directory as it was.
	latest, err := st.Latest(ctx)
	if err != nil {
		return Stats{}, err
	}
	byCurrency := map[string]map[string]rates.Rate{}
	for _, r := range latest {
		if byCurrency[r.Source] == nil {
			byCurrency[r.Source] = map[string]rates.Rate{}
		}
		byCurrency[r.Source][r.Currency] = r
		if err := w.json(filepath.Join(r.Source, r.Currency, "latest.json"), r); err != nil {
			return Stats{}, err
		}
	}

	dataChanged := false
	for _, s := range series {
		changed, err := w.series(ctx, st, s)
		if err != nil {
			return Stats{}, err
		}
		dataChanged = dataChanged || changed
	}

	if !dataChanged {
		if prev, ok := readUpdatedAt(filepath.Join(dir, "index.json")); ok {
			updated = prev
		}
	}
	if err := w.json("latest.json", map[string]any{"updated_at": updated, "rates": byCurrency}); err != nil {
		return Stats{}, err
	}
	if err := w.json("index.json", map[string]any{
		"updated_at": updated,
		"sources":    SourceInfo,
		"series":     series,
		"project":    "https://github.com/jmendozaf/cotizapy",
	}); err != nil {
		return Stats{}, err
	}
	if err := w.page(series, byCurrency, updated, true); err != nil {
		return Stats{}, err
	}
	if err := w.write(".nojekyll", nil); err != nil {
		return Stats{}, err
	}
	return w.stats, nil
}

// Page writes only index.html, the table of latest rates, without the JSON and CSV files behind it.
func Page(ctx context.Context, st *store.Store, dir string, now time.Time) (Stats, error) {
	w := &writer{dir: dir}
	series, err := st.Series(ctx)
	if err != nil {
		return Stats{}, err
	}
	latest, err := st.Latest(ctx)
	if err != nil {
		return Stats{}, err
	}
	byCurrency := map[string]map[string]rates.Rate{}
	for _, r := range latest {
		if byCurrency[r.Source] == nil {
			byCurrency[r.Source] = map[string]rates.Rate{}
		}
		byCurrency[r.Source][r.Currency] = r
	}
	if err := w.page(series, byCurrency, now.UTC().Format(time.RFC3339), false); err != nil {
		return Stats{}, err
	}
	if err := w.write(".nojekyll", nil); err != nil {
		return Stats{}, err
	}
	return w.stats, nil
}

func readUpdatedAt(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var v struct {
		UpdatedAt string `json:"updated_at"`
	}
	if json.Unmarshal(b, &v) != nil || v.UpdatedAt == "" {
		return "", false
	}
	return v.UpdatedAt, true
}

// day is one entry of a yearly file, keyed by date.
type day struct {
	Buy       rates.Decimal `json:"buy,omitempty"`
	Sell      rates.Decimal `json:"sell,omitempty"`
	Reference rates.Decimal `json:"reference,omitempty"`
}

// series writes the yearly files and the full CSV of one currency, reporting whether any of them changed.
func (w *writer) series(ctx context.Context, st *store.Store, s store.Series) (bool, error) {
	list, err := st.Range(ctx, s.Source, s.Currency, s.First, s.Last)
	if err != nil {
		return false, err
	}
	before := w.stats.Written
	base := filepath.Join(s.Source, s.Currency)

	byYear := map[string][]rates.Rate{}
	var years []string
	for _, r := range list {
		y := r.Date[:4]
		if _, ok := byYear[y]; !ok {
			years = append(years, y)
		}
		byYear[y] = append(byYear[y], r)
	}
	for _, y := range years {
		days := map[string]day{}
		for _, r := range byYear[y] {
			days[r.Date] = day{Buy: r.Buy, Sell: r.Sell, Reference: r.Reference}
		}
		if err := w.json(filepath.Join(base, y+".json"), map[string]any{
			"source": s.Source, "currency": s.Currency, "year": y, "rates": days,
		}); err != nil {
			return false, err
		}
		if err := w.csv(filepath.Join(base, y+".csv"), s.Source, byYear[y]); err != nil {
			return false, err
		}
	}
	if err := w.csv(base+".csv", s.Source, list); err != nil {
		return false, err
	}
	return w.stats.Written > before, nil
}

type writer struct {
	dir   string
	stats Stats
}

func (w *writer) json(rel string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return w.write(rel, buf.Bytes())
}

func (w *writer) csv(rel, source string, list []rates.Rate) error {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	if source == rates.SET {
		cw.Write([]string{"date", "buy", "sell"})
	} else {
		cw.Write([]string{"date", "reference"})
	}
	for _, r := range list {
		if source == rates.SET {
			cw.Write([]string{r.Date, string(r.Buy), string(r.Sell)})
		} else {
			cw.Write([]string{r.Date, string(r.Reference)})
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return w.write(rel, buf.Bytes())
}

func (w *writer) write(rel string, content []byte) error {
	path := filepath.Join(w.dir, rel)
	w.stats.Files++
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("export: %w", err)
	}
	w.stats.Written++
	return nil
}

//go:embed index.html.tmpl
var pageTemplate string

var page = template.Must(template.New("index").Funcs(template.FuncMap{
	"lower": strings.ToLower,
	"gs":    formatGuaranies,
}).Parse(pageTemplate))

type pageRow struct {
	Currency string
	Name     string
	SET      *rates.Rate
	BCP      *rates.Rate
}

// page writes index.html; withFiles links each rate to its CSV and explains the files next to it.
func (w *writer) page(series []store.Series, latest map[string]map[string]rates.Rate, updated string, withFiles bool) error {
	var rows []pageRow
	// Only the DNIT currencies; the rest of the BCP ones stay in the JSON and CSV files.
	for _, s := range series {
		if s.Source != rates.SET {
			continue
		}
		row := pageRow{Currency: s.Currency, Name: rates.Names[s.Currency]}
		if r, ok := latest[rates.SET][s.Currency]; ok {
			row.SET = &r
		}
		if r, ok := latest[rates.BCP][s.Currency]; ok {
			row.BCP = &r
		}
		rows = append(rows, row)
	}
	slices.SortStableFunc(rows, func(a, b pageRow) int { return cmp.Compare(rank(a.Currency), rank(b.Currency)) })
	var buf bytes.Buffer
	if err := page.Execute(&buf, map[string]any{"Rows": rows, "UpdatedAt": updated, "Files": withFiles}); err != nil {
		return err
	}
	return w.write("index.html", buf.Bytes())
}

// rank puts the most used currencies at the top of the page.
func rank(code string) int {
	if i := slices.Index([]string{"USD", "EUR", "BRL", "ARS"}, code); i >= 0 {
		return i
	}
	return 99
}

// formatGuaranies prints "5694.47" as "5.694,47".
func formatGuaranies(d rates.Decimal) string {
	s := string(d)
	if s == "" {
		return "—"
	}
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if frac != "" {
		b.WriteByte(',')
		b.WriteString(frac)
	}
	return b.String()
}
