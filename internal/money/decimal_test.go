package money

import (
	"encoding/json"
	"testing"
	"testing/quick"
)

func TestParseAndString(t *testing.T) {
	cases := map[string]string{
		"0":            "0",
		"12":           "12",
		"-0.0104":      "-0.0104",
		"0.0000002000": "0.0000002",
		"1e-7":         "0.0000001",
		"40.96":        "40.96",
		"-0":           "0",
	}
	for in, want := range cases {
		d, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if got := d.String(); got != want {
			t.Errorf("Parse(%q).String() = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "abc", "1/3", "1.2.3"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestArithmeticIsExact(t *testing.T) {
	// 0.1 + 0.2 is exactly 0.3, unlike float64.
	if got := MustParse("0.1").Add(MustParse("0.2")); got.Cmp(MustParse("0.3")) != 0 {
		t.Fatalf("0.1+0.2 = %s", got)
	}
	// 4,000,000 Lambda requests at $0.0000002 = $0.80 exactly.
	if got := FromInt(4_000_000).Mul(MustParse("0.0000002")); got.String() != "0.8" {
		t.Fatalf("got %s", got)
	}
	q, err := FromInt(1).Div(FromInt(3))
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Mul(FromInt(3)); got.Cmp(FromInt(1)) != 0 {
		t.Fatalf("1/3*3 = %s", got)
	}
	if _, err := FromInt(1).Div(Zero); err == nil {
		t.Fatal("division by zero did not error")
	}
	if got := FromInt(5).Sub(FromInt(7)).Neg(); got.Cmp(FromInt(2)) != 0 {
		t.Fatalf("got %s", got)
	}
	if FromInt(3).Min(FromInt(2)).Cmp(FromInt(2)) != 0 || FromInt(3).Max(FromInt(2)).Cmp(FromInt(3)) != 0 {
		t.Fatal("min/max wrong")
	}
	if Sum(FromInt(1), FromInt(2), MustParse("0.5")).String() != "3.5" {
		t.Fatal("sum wrong")
	}
	if MustParse("-7.9").Int64() != -7 || MustParse("7.9").Int64() != 7 {
		t.Fatal("Int64 should truncate toward zero")
	}
}

func TestRoundHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		in     string
		places int
		want   string
	}{
		{"16.425", 2, "16.43"},
		{"-16.425", 2, "-16.43"},
		{"30.368", 2, "30.37"},
		{"0.004", 2, "0"},
		{"2.5", 0, "3"},
	}
	for _, c := range cases {
		if got := MustParse(c.in).Round(c.places).String(); got != c.want {
			t.Errorf("Round(%s, %d) = %s, want %s", c.in, c.places, got, c.want)
		}
	}
}

func TestFromFloatUsesShortestDecimal(t *testing.T) {
	d, err := FromFloat(0.1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Cmp(MustParse("0.1")) != 0 {
		t.Fatalf("FromFloat(0.1) = %s", d)
	}
	var nan float64
	if _, err := FromFloat(nan / nan); err == nil {
		t.Fatal("NaN accepted")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	type wrapper struct {
		A Decimal `json:"a"`
		B Decimal `json:"b"`
	}
	var w wrapper
	if err := json.Unmarshal([]byte(`{"a": 0.0000166667, "b": "40.9600000000"}`), &w); err != nil {
		t.Fatal(err)
	}
	if w.A.String() != "0.0000166667" || w.B.String() != "40.96" {
		t.Fatalf("decoded %s %s", w.A, w.B)
	}
	out, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"a":0.0000166667,"b":40.96}` {
		t.Fatalf("encoded %s", out)
	}
	if err := json.Unmarshal([]byte(`{"a": "x"}`), &w); err == nil {
		t.Fatal("bad decimal accepted")
	}
	if err := json.Unmarshal([]byte(`{"a": "1`), &w); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

func TestFormatting(t *testing.T) {
	cases := []struct{ got, want string }{
		{FormatUSD(MustParse("3320.425"), 2), "$3,320.43"},
		{FormatUSD(MustParse("-1234567.891"), 2), "-$1,234,567.89"},
		{FormatUSD(MustParse("-0.001"), 2), "$0.00"},
		{FormatUSD(MustParse("12"), 0), "$12"},
		{FormatSignedUSD(MustParse("6"), 2), "+$6.00"},
		{FormatSignedUSD(MustParse("-4"), 2), "-$4.00"},
		{FormatSignedUSD(Zero, 2), "$0.00"},
		{FormatQuantity(MustParse("32000"), 4), "32,000"},
		{FormatQuantity(MustParse("0.12207031"), 4), "0.1221"},
		{FormatQuantity(MustParse("-1500.5"), 4), "-1,500.5"},
		{FormatUnitPrice(MustParse("0.0000002")), "$0.0000002"},
		{FormatUnitPrice(MustParse("0.1")), "$0.10"},
		{FormatUnitPrice(MustParse("1234.5")), "$1,234.50"},
		{FormatUnitPrice(MustParse("-0.5")), "-$0.50"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %q, want %q", i, c.got, c.want)
		}
	}
}

// Property: (a + b) - b == a and a*b == b*a for arbitrary int64 cents.
func TestArithmeticProperties(t *testing.T) {
	prop := func(a, b int64) bool {
		x, _ := FromInt(a).Div(FromInt(100))
		y, _ := FromInt(b).Div(FromInt(100))
		return x.Add(y).Sub(y).Cmp(x) == 0 && x.Mul(y).Cmp(y.Mul(x)) == 0
	}
	if err := quick.Check(prop, nil); err != nil {
		t.Fatal(err)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"0", "1.5", "-2e3", "0.0000002", "abc", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := Parse(s)
		if err != nil {
			return
		}
		// Every parsed value must survive a String/Parse round trip when it
		// is a terminating decimal of at most 12 places.
		back, err := Parse(d.String())
		if err != nil {
			t.Fatalf("String() of %q produced unparsable %q", s, d.String())
		}
		if d.Round(12).Cmp(back) != 0 {
			t.Fatalf("round trip of %q: %s != %s", s, d.Round(12), back)
		}
	})
}
