// Package rates holds the types shared by every source: rates, decimal amounts and currencies.
package rates

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Sources.
const (
	SET = "set" // DNIT: exchange rates for tax purposes, buy and sell, every calendar day
	BCP = "bcp" // Banco Central del Paraguay: reference rate, business days only
)

// Sources lists the known sources in display order.
var Sources = []string{SET, BCP}

// Location is Paraguay's time zone; "today" is always Asunción's today.
var Location = mustLoad("America/Asuncion")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("PYT", -3*60*60)
	}
	return loc
}

// Today returns the current date in Asunción as YYYY-MM-DD.
func Today() string { return time.Now().In(Location).Format(time.DateOnly) }

// Decimal is an exact decimal amount such as "5694.47". It is encoded in JSON as a number, never as a float.
type Decimal string

// MarshalJSON writes the amount as a bare JSON number.
func (d Decimal) MarshalJSON() ([]byte, error) {
	if d == "" {
		return []byte("null"), nil
	}
	return []byte(d), nil
}

// UnmarshalJSON reads a JSON number (or null) without going through float64.
func (d *Decimal) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		*d = ""
		return nil
	}
	if !decimalPattern.MatchString(s) {
		return fmt.Errorf("rates: invalid decimal %s", s)
	}
	*d = Decimal(s)
	return nil
}

var decimalPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// Rate is one published quote: guaraníes per unit of Currency on Date.
// SET publishes Buy and Sell; BCP publishes a single Reference rate.
type Rate struct {
	Source    string  `json:"source"`
	Currency  string  `json:"currency"`
	Date      string  `json:"date"`
	Buy       Decimal `json:"buy,omitempty"`
	Sell      Decimal `json:"sell,omitempty"`
	Reference Decimal `json:"reference,omitempty"`
}

// NumberFormat is how a source wrote a number.
type NumberFormat int

const (
	// Spanish is "5.694,47": dot for thousands, comma for decimals.
	Spanish NumberFormat = iota
	// English is "5,694.47": comma for thousands, dot for decimals.
	English
)

var (
	englishHint = regexp.MustCompile(`,\d{3}\.\d|^\d+\.\d{1,2}$`)
	spanishHint = regexp.MustCompile(`,\d+$`)
)

// DetectFormat guesses the number format of a row of cells. Some months of the SET table were
// typed in English format, and "6.507" means 6507 in a Spanish row but 6.507 in an English one,
// so the decision is made for the whole row, not per cell.
func DetectFormat(cells []string) NumberFormat {
	english, spanish := 0, 0
	for _, c := range cells {
		c = strings.TrimSpace(c)
		switch {
		case englishHint.MatchString(c):
			english++
		case spanishHint.MatchString(c):
			spanish++
		}
	}
	if english > spanish {
		return English
	}
	return Spanish
}

// ErrNoValue means the cell is empty or marked as not available ("ND", "-").
var ErrNoValue = errors.New("rates: no value")

// ParseAmount converts a published amount such as "5.694,47" to a Decimal ("5694.47").
func ParseAmount(raw string, format NumberFormat) (Decimal, error) {
	s := strings.TrimSpace(strings.ReplaceAll(raw, " ", ""))
	switch strings.ToUpper(s) {
	case "", "ND", "N/D", "-", "--":
		return "", ErrNoValue
	}
	thousands, decimal := ".", ","
	if format == English {
		thousands, decimal = ",", "."
	}
	s = strings.ReplaceAll(s, thousands, "")
	s = strings.Replace(s, decimal, ".", 1)
	if !decimalPattern.MatchString(s) {
		return "", fmt.Errorf("rates: cannot read amount %q", raw)
	}
	s = strings.TrimLeft(s, "0")
	if s == "" || s[0] == '.' {
		s = "0" + s
	}
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return Decimal(s), nil
}

// Currency describes a currency code.
type Currency struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Names maps ISO codes to their Spanish names.
var Names = map[string]string{
	"USD": "Dólar estadounidense",
	"EUR": "Euro",
	"BRL": "Real brasileño",
	"ARS": "Peso argentino",
	"GBP": "Libra esterlina",
	"JPY": "Yen japonés",
	"CHF": "Franco suizo",
	"CAD": "Dólar canadiense",
	"AUD": "Dólar australiano",
	"CNY": "Yuan chino",
	"CLP": "Peso chileno",
	"MXN": "Peso mexicano",
	"UYU": "Peso uruguayo",
	"COP": "Peso colombiano",
	"BOB": "Boliviano",
	"NZD": "Dólar neozelandés",
	"ZAR": "Rand sudafricano",
	"SEK": "Corona sueca",
	"DKK": "Corona danesa",
	"NOK": "Corona noruega",
	"SDR": "Derecho especial de giro (FMI)",
	"XAU": "Oro (onza troy)",
}

// NormalizeCurrency upper-cases a code and accepts a few common aliases.
func NormalizeCurrency(code string) string {
	c := strings.ToUpper(strings.TrimSpace(code))
	switch c {
	case "DOLAR", "DÓLAR", "US$", "U$S":
		return "USD"
	case "REAL", "R$":
		return "BRL"
	case "EURO":
		return "EUR"
	}
	return c
}

// ValidDate reports whether s is a YYYY-MM-DD date.
func ValidDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}
