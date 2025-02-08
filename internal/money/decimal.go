// Package money provides an exact decimal type for prices and costs.
//
// AWS publishes prices with up to ten decimal places (for example Lambda
// requests at 0.0000002 USD). Multiplying such prices by large usage figures in
// float64 accumulates representation error, so every quantity, unit price and
// cost in planprice is a Decimal backed by math/big.Rat. Arithmetic is exact;
// rounding happens only when a value is formatted for display.
package money

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Decimal is an immutable exact rational number. The zero value is 0 and is
// ready to use. Methods never modify their receiver or arguments.
type Decimal struct {
	r *big.Rat
}

// Zero is the additive identity.
var Zero = Decimal{}

func (d Decimal) rat() *big.Rat {
	if d.r == nil {
		return new(big.Rat)
	}
	return d.r
}

// Parse reads a decimal literal such as "12", "-0.0104" or "1e-7".
func Parse(s string) (Decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Zero, fmt.Errorf("money: empty decimal")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || strings.Contains(s, "/") {
		return Zero, fmt.Errorf("money: invalid decimal %q", s)
	}
	return Decimal{r: r}, nil
}

// MustParse is Parse for compile-time constants; it panics on invalid input.
func MustParse(s string) Decimal {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

// FromInt returns the Decimal value of i.
func FromInt(i int64) Decimal {
	return Decimal{r: new(big.Rat).SetInt64(i)}
}

// FromFloat converts a float64 via its shortest decimal representation. It is
// used only for values that arrive as YAML floats.
func FromFloat(f float64) (Decimal, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Zero, fmt.Errorf("money: non-finite number %v", f)
	}
	// Go through the shortest decimal representation so that 0.1 means one
	// tenth, matching what the user wrote in a YAML or JSON file.
	return Parse(strconv.FormatFloat(f, 'g', -1, 64))
}

// Add returns d + o.
func (d Decimal) Add(o Decimal) Decimal {
	return Decimal{r: new(big.Rat).Add(d.rat(), o.rat())}
}

// Sub returns d - o.
func (d Decimal) Sub(o Decimal) Decimal {
	return Decimal{r: new(big.Rat).Sub(d.rat(), o.rat())}
}

// Mul returns d * o.
func (d Decimal) Mul(o Decimal) Decimal {
	return Decimal{r: new(big.Rat).Mul(d.rat(), o.rat())}
}

// Div returns d / o. It reports an error instead of panicking on o == 0.
func (d Decimal) Div(o Decimal) (Decimal, error) {
	if o.IsZero() {
		return Zero, fmt.Errorf("money: division by zero")
	}
	return Decimal{r: new(big.Rat).Quo(d.rat(), o.rat())}, nil
}

// Neg returns -d.
func (d Decimal) Neg() Decimal {
	return Decimal{r: new(big.Rat).Neg(d.rat())}
}

// Cmp compares d and o, returning -1, 0 or +1.
func (d Decimal) Cmp(o Decimal) int { return d.rat().Cmp(o.rat()) }

// Sign returns -1, 0 or +1.
func (d Decimal) Sign() int { return d.rat().Sign() }

// IsZero reports whether d == 0.
func (d Decimal) IsZero() bool { return d.Sign() == 0 }

// Min returns the smaller of d and o.
func (d Decimal) Min(o Decimal) Decimal {
	if d.Cmp(o) <= 0 {
		return d
	}
	return o
}

// Max returns the larger of d and o.
func (d Decimal) Max(o Decimal) Decimal {
	if d.Cmp(o) >= 0 {
		return d
	}
	return o
}

// Round rounds to the given number of decimal places, halves away from zero.
func (d Decimal) Round(places int) Decimal {
	return MustParse(d.rat().FloatString(places))
}

// StringFixed formats d with exactly the given number of decimal places,
// rounding halves away from zero.
func (d Decimal) StringFixed(places int) string {
	return d.rat().FloatString(places)
}

// String formats d without trailing zeros. Values that are not terminating
// decimals are rounded to 12 places.
func (d Decimal) String() string {
	s := d.rat().FloatString(12)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

// Float64 returns the nearest float64; use only for ratios and display.
func (d Decimal) Float64() float64 {
	f, _ := d.rat().Float64()
	return f
}

// Int64 truncates d towards zero.
func (d Decimal) Int64() int64 {
	q := new(big.Int).Quo(d.rat().Num(), d.rat().Denom())
	return q.Int64()
}

// MarshalJSON encodes d as a JSON number.
func (d Decimal) MarshalJSON() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalJSON accepts a JSON number or a JSON string holding a decimal.
func (d *Decimal) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = str
	}
	v, err := Parse(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// Sum adds all values.
func Sum(values ...Decimal) Decimal {
	total := new(big.Rat)
	for _, v := range values {
		total.Add(total, v.rat())
	}
	return Decimal{r: total}
}
