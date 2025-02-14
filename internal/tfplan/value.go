package tfplan

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/guneshm03/planprice/internal/money"
)

// Attrs is a resource's attribute object together with Terraform's
// after_unknown mask, so callers can tell "null" from "known after apply".
type Attrs struct {
	values  map[string]any
	unknown map[string]any
}

// NewAttrs wraps decoded values and an unknown mask (either may be nil).
func NewAttrs(values, unknown any) Attrs {
	v, _ := values.(map[string]any)
	u, _ := unknown.(map[string]any)
	return Attrs{values: v, unknown: u}
}

// Raw returns the underlying attribute map (read-only by convention).
func (a Attrs) Raw() map[string]any { return a.values }

// Unknown reports whether key is marked "known after apply".
func (a Attrs) Unknown(key string) bool {
	b, ok := a.unknown[key].(bool)
	return ok && b
}

// Has reports whether key is present with a non-null value.
func (a Attrs) Has(key string) bool {
	v, ok := a.values[key]
	return ok && v != nil
}

// String returns a string attribute. ok is false when the attribute is null,
// absent, unknown or not a string.
func (a Attrs) String(key string) (string, bool) {
	s, ok := a.values[key].(string)
	return s, ok
}

// StringOr returns the string attribute or def when it is not a known string.
func (a Attrs) StringOr(key, def string) string {
	if s, ok := a.String(key); ok && s != "" {
		return s
	}
	return def
}

// Bool returns a boolean attribute; ok is false if it is not a known bool.
func (a Attrs) Bool(key string) (value, ok bool) {
	b, ok := a.values[key].(bool)
	return b, ok
}

// Number returns a numeric attribute as an exact Decimal.
func (a Attrs) Number(key string) (money.Decimal, bool) {
	return toDecimal(a.values[key])
}

// NumberOr returns the numeric attribute, or def when it is null, unknown or
// non-numeric. Zero is treated as unset because the AWS provider reports
// unset optional numbers (iops, throughput) as 0.
func (a Attrs) NumberOr(key string, def money.Decimal) money.Decimal {
	if d, ok := a.Number(key); ok && !d.IsZero() {
		return d
	}
	return def
}

// Strings returns a list-of-strings attribute.
func (a Attrs) Strings(key string) []string {
	list, _ := a.values[key].([]any)
	out := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Blocks returns the nested blocks of a list/set block attribute such as
// root_block_device, each with its own slice of the unknown mask.
func (a Attrs) Blocks(key string) []Attrs {
	list, _ := a.values[key].([]any)
	unk, _ := a.unknown[key].([]any)
	out := make([]Attrs, 0, len(list))
	for i, item := range list {
		var u any
		if i < len(unk) {
			u = unk[i]
		}
		out = append(out, NewAttrs(item, u))
	}
	return out
}

func toDecimal(v any) (money.Decimal, bool) {
	switch n := v.(type) {
	case json.Number:
		d, err := money.Parse(n.String())
		return d, err == nil
	case float64:
		d, err := money.FromFloat(n)
		return d, err == nil
	case int:
		return money.FromInt(int64(n)), true
	case int64:
		return money.FromInt(n), true
	case string:
		// Terraform sometimes renders numbers as strings in maps.
		if _, err := strconv.ParseFloat(n, 64); err != nil {
			return money.Zero, false
		}
		d, err := money.Parse(n)
		return d, err == nil
	default:
		return money.Zero, false
	}
}

// Describe renders an attribute value for diagnostics.
func Describe(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(t)
	default:
		return fmt.Sprint(t)
	}
}
