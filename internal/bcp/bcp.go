// Package bcp reads the reference exchange rates of the Banco Central del Paraguay
// ("Cotización Referencial Histórica"): one page per year and currency, a day-by-month grid,
// "ND" on days without a quote (weekends and holidays).
package bcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jmendozaf/cotizapy/internal/htmltable"
	"github.com/jmendozaf/cotizapy/internal/rates"
)

// DefaultURL is the yearly history page.
const DefaultURL = "https://www.bcp.gov.py/webapps/web/cotizacion/monedas-historica"

const userAgent = "cotizapy (+https://github.com/jmendozaf/cotizapy)"

// Client talks to the BCP site.
type Client struct {
	HTTP *http.Client
	URL  string
}

// NewClient returns a client for the official page.
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: time.Minute}, URL: DefaultURL}
}

// Form lists the years and currencies the history page offers.
type Form struct {
	Years      []int
	Currencies []string
}

// Form reads the selectors of the history page.
func (c *Client) Form(ctx context.Context) (Form, error) {
	body, err := c.do(ctx, http.MethodGet, nil)
	if err != nil {
		return Form{}, err
	}
	return ParseForm(body)
}

// ParseForm extracts the year and currency options of the history page.
func ParseForm(page []byte) (Form, error) {
	yearOptions, err := htmltable.Options(bytes.NewReader(page), "anho")
	if err != nil {
		return Form{}, err
	}
	currencies, err := htmltable.Options(bytes.NewReader(page), "moneda")
	if err != nil {
		return Form{}, err
	}
	var f Form
	for _, y := range yearOptions {
		if n, err := strconv.Atoi(strings.TrimSpace(y)); err == nil {
			f.Years = append(f.Years, n)
		}
	}
	slices.Sort(f.Years)
	for _, c := range currencies {
		if c = strings.ToUpper(strings.TrimSpace(c)); c != "" {
			f.Currencies = append(f.Currencies, c)
		}
	}
	if len(f.Years) == 0 || len(f.Currencies) == 0 {
		return Form{}, errors.New("bcp: year or currency selector not found (did the page change?)")
	}
	return f, nil
}

// Year downloads one currency's reference rates for a whole year.
func (c *Client) Year(ctx context.Context, year int, currency string) ([]rates.Rate, error) {
	form := url.Values{"anho": {strconv.Itoa(year)}, "moneda": {currency}}
	body, err := c.do(ctx, http.MethodPost, form)
	if err != nil {
		return nil, err
	}
	return ParseYear(bytes.NewReader(body), year, currency)
}

func (c *Client) do(ctx context.Context, method string, form url.Values) ([]byte, error) {
	var payload io.Reader
	if form != nil {
		payload = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bcp: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bcp: %s returned %s", c.URL, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

var monthHeaders = []string{"ENE", "FEB", "MAR", "ABR", "MAY", "JUN", "JUL", "AGO", "SEP", "OCT", "NOV", "DIC"}

var dayCell = regexp.MustCompile(`^\d{1,2}$`)

// ParseYear reads the day-by-month grid of a yearly history page.
func ParseYear(r io.Reader, year int, currency string) ([]rates.Rate, error) {
	tables, err := htmltable.Parse(r)
	if err != nil {
		return nil, err
	}
	var grid [][]string
	for _, t := range tables {
		if len(t.Rows) > 0 && len(t.Rows[0]) == 13 && slices.Equal(upper(t.Rows[0][1:]), monthHeaders) {
			grid = t.Rows[1:]
			break
		}
		// The title table says which year the page is for; a mismatch means the form was ignored.
		if len(t.Rows) == 1 && len(t.Rows[0]) == 1 && strings.Contains(t.Rows[0][0], "AÑO") &&
			!strings.Contains(t.Rows[0][0], strconv.Itoa(year)) {
			return nil, fmt.Errorf("bcp: asked for %d but the page shows %q", year, t.Rows[0][0])
		}
	}
	if grid == nil {
		return nil, fmt.Errorf("bcp: %d %s: day-by-month table not found (did the page change?)", year, currency)
	}

	var out []rates.Rate
	for _, row := range grid {
		if len(row) != 13 || !dayCell.MatchString(row[0]) {
			continue
		}
		day, _ := strconv.Atoi(row[0])
		format := rates.DetectFormat(row[1:])
		for m := 1; m <= 12; m++ {
			value, err := rates.ParseAmount(row[m], format)
			if errors.Is(err, rates.ErrNoValue) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("bcp: %d-%02d-%02d %s: %w", year, m, day, currency, err)
			}
			date := time.Date(year, time.Month(m), day, 0, 0, 0, 0, time.UTC)
			if date.Day() != day {
				continue // 31 de febrero and friends
			}
			out = append(out, rates.Rate{
				Source:    rates.BCP,
				Currency:  currency,
				Date:      date.Format(time.DateOnly),
				Reference: value,
			})
		}
	}
	slices.SortFunc(out, func(a, b rates.Rate) int { return strings.Compare(a.Date, b.Date) })
	return out, nil
}

func upper(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = strings.ToUpper(strings.TrimSpace(c))
	}
	return out
}
