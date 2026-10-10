package rates

import (
	"encoding/json"
	"testing"
)

func TestParseAmount(t *testing.T) {
	cases := []struct {
		raw    string
		format NumberFormat
		want   Decimal
	}{
		{"5.694,47", Spanish, "5694.47"},
		{"6.507", Spanish, "6507"},
		{"96,6", Spanish, "96.6"},
		{"1,0000", Spanish, "1"},
		{"0,8310", Spanish, "0.831"},
		{"7,299.26", English, "7299.26"},
		{"8.58", English, "8.58"},
	}
	for _, c := range cases {
		got, err := ParseAmount(c.raw, c.format)
		if err != nil || got != c.want {
			t.Errorf("ParseAmount(%q) = %q, %v; want %q", c.raw, got, err, c.want)
		}
	}
	for _, raw := range []string{"ND", "", " - "} {
		if _, err := ParseAmount(raw, Spanish); err != ErrNoValue {
			t.Errorf("ParseAmount(%q) err = %v; want ErrNoValue", raw, err)
		}
	}
	if _, err := ParseAmount("abc", Spanish); err == nil {
		t.Error("ParseAmount(abc) should fail")
	}
}

func TestDetectFormat(t *testing.T) {
	// Real rows from the SET page: an English one (Marzo 2024) and a Spanish one where "6.507" means 6507.
	english := []string{"7,299.26", "7,306.19", "1,463.28", "1,464.99", "8.58", "8.59", "48.99", "49.03"}
	spanish := []string{"6.507", "6.512,59", "1.444,17", "1.445,67", "104,59", "104,7", "60,08", "60,14"}
	if DetectFormat(english) != English {
		t.Error("english row detected as spanish")
	}
	if DetectFormat(spanish) != Spanish {
		t.Error("spanish row detected as english")
	}
}

func TestDecimalJSON(t *testing.T) {
	b, err := json.Marshal(Rate{Source: SET, Currency: "USD", Date: "2026-10-09", Buy: "5678.11", Sell: "5698.34"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"source":"set","currency":"USD","date":"2026-10-09","buy":5678.11,"sell":5698.34}`
	if string(b) != want {
		t.Errorf("got %s\nwant %s", b, want)
	}
	var r Rate
	if err := json.Unmarshal(b, &r); err != nil || r.Buy != "5678.11" || r.Reference != "" {
		t.Errorf("round trip: %+v %v", r, err)
	}
}
