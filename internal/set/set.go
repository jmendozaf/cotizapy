// Package set reads the exchange rates the DNIT publishes for tax purposes.
// A single page carries every month since 2010, one table per month, with buy and sell rates
// for six currencies on every calendar day (weekends and holidays repeat the last business day).
package set

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jmendozaf/cotizapy/internal/htmltable"
	"github.com/jmendozaf/cotizapy/internal/rates"
)

// DefaultURL is the official page ("Cotizaciones").
const DefaultURL = "https://www.dnit.gov.py/web/portal-institucional/cotizaciones"

const userAgent = "cotizapy (+https://github.com/jmendozaf/cotizapy)"

// Client talks to the DNIT site.
type Client struct {
	HTTP *http.Client
	URL  string
}

// NewClient returns a client for the official page.
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 2 * time.Minute}, URL: DefaultURL}
}

// Fetch downloads the page and returns every rate on it.
func (c *Client) Fetch(ctx context.Context) ([]rates.Rate, []string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("set: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("set: %s returned %s", c.URL, resp.Status)
	}
	return Parse(resp.Body)
}

var (
	monthTitle = regexp.MustCompile(`(?i)mes de\s+(\p{L}+)\s+(\d{4})`)
	dayCell    = regexp.MustCompile(`^\d{1,2}$`)
)

var months = map[string]time.Month{
	"enero": time.January, "febrero": time.February, "marzo": time.March, "abril": time.April,
	"mayo": time.May, "junio": time.June, "julio": time.July, "agosto": time.August,
	"septiembre": time.September, "setiembre": time.September, "octubre": time.October,
	"noviembre": time.November, "diciembre": time.December,
}

// headerCurrencies maps the column titles used on the page to ISO codes.
var headerCurrencies = map[string]string{
	"DÓLAR": "USD", "DOLAR": "USD", "REAL": "BRL", "PESO ARG.": "ARS", "PESO ARG": "ARS",
	"YEN": "JPY", "EURO": "EUR", "LIBRA": "GBP",
}

// Parse reads the page. Besides the rates it returns warnings for rows it had to skip,
// so a change in the page shows up instead of silently losing data.
func Parse(r io.Reader) ([]rates.Rate, []string, error) {
	tables, err := htmltable.Parse(r)
	if err != nil {
		return nil, nil, err
	}
	var (
		out      []rates.Rate
		warnings []string
		found    int
	)
	for _, t := range tables {
		m := monthTitle.FindStringSubmatch(t.Heading)
		if m == nil {
			continue
		}
		month, ok := months[strings.ToLower(m[1])]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%q: unknown month", t.Heading))
			continue
		}
		year, _ := strconv.Atoi(m[2])
		found++
		parsed, warns, err := parseMonth(t, year, month)
		if err != nil {
			return nil, warnings, fmt.Errorf("set: %s: %w", t.Heading, err)
		}
		out = append(out, parsed...)
		warnings = append(warnings, warns...)
	}
	if found == 0 {
		return nil, warnings, fmt.Errorf("set: no monthly tables found (did the page change?)")
	}
	return out, warnings, nil
}

func parseMonth(t htmltable.Table, year int, month time.Month) ([]rates.Rate, []string, error) {
	if len(t.Rows) < 3 {
		return nil, nil, nil
	}
	var currencies []string
	for _, h := range t.Rows[0] {
		if h == "" {
			continue
		}
		code, ok := headerCurrencies[strings.ToUpper(h)]
		if !ok {
			return nil, nil, fmt.Errorf("unknown currency column %q", h)
		}
		currencies = append(currencies, code)
	}

	var (
		out      []rates.Rate
		warnings []string
		// Some months repeat a few days (Abril 2021 lists 24–26 twice); the later rows are the corrected ones.
		index = map[string]int{}
	)
	add := func(r rates.Rate) {
		key := r.Currency + r.Date
		if i, ok := index[key]; ok {
			if r.Currency == currencies[0] {
				warnings = append(warnings, fmt.Sprintf("%s: day listed twice, kept the later row", r.Date))
			}
			out[i] = r
			return
		}
		index[key] = len(out)
		out = append(out, r)
	}
	for _, row := range t.Rows[2:] {
		if len(row) == 0 || (len(row) == 1 && row[0] == "") {
			continue
		}
		if !dayCell.MatchString(row[0]) || len(row) < 1+2*len(currencies) {
			warnings = append(warnings, fmt.Sprintf("%d-%02d: skipped row %q", year, month, row))
			continue
		}
		day, _ := strconv.Atoi(row[0])
		date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		if date.Month() != month {
			warnings = append(warnings, fmt.Sprintf("%d-%02d: invalid day %d", year, month, day))
			continue
		}
		format := rates.DetectFormat(row[1:])
		for i, code := range currencies {
			buy, errBuy := rates.ParseAmount(row[1+2*i], format)
			sell, errSell := rates.ParseAmount(row[2+2*i], format)
			for _, err := range []error{errBuy, errSell} {
				if err != nil && !errors.Is(err, rates.ErrNoValue) {
					warnings = append(warnings, fmt.Sprintf("%s %s: %v", date.Format(time.DateOnly), code, err))
				}
			}
			if errBuy != nil && errSell != nil {
				continue
			}
			add(rates.Rate{
				Source:   rates.SET,
				Currency: code,
				Date:     date.Format(time.DateOnly),
				Buy:      buy,
				Sell:     sell,
			})
		}
	}
	return out, warnings, nil
}
