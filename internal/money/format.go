package money

import "strings"

// FormatUSD renders d as a dollar amount with thousands separators and the
// given number of decimal places, e.g. "$1,234.50" or "-$0.42".
func FormatUSD(d Decimal, places int) string {
	neg := d.Sign() < 0
	if neg {
		d = d.Neg()
	}
	s := d.StringFixed(places)
	intPart, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	if neg && !isAllZero(s) {
		b.WriteByte('-')
	}
	b.WriteByte('$')
	b.WriteString(groupThousands(intPart))
	if hasFrac {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// FormatSignedUSD is FormatUSD with an explicit "+" on positive values, used
// for cost deltas.
func FormatSignedUSD(d Decimal, places int) string {
	s := FormatUSD(d, places)
	if d.Round(places).Sign() > 0 {
		return "+" + s
	}
	return s
}

// FormatQuantity renders a usage quantity with thousands separators and at
// most maxPlaces decimals (trailing zeros dropped).
func FormatQuantity(d Decimal, maxPlaces int) string {
	neg := d.Sign() < 0
	if neg {
		d = d.Neg()
	}
	s := d.StringFixed(maxPlaces)
	intPart, frac, _ := strings.Cut(s, ".")
	frac = strings.TrimRight(frac, "0")
	out := groupThousands(intPart)
	if frac != "" {
		out += "." + frac
	}
	if neg && !isAllZero(s) {
		out = "-" + out
	}
	return out
}

// FormatUnitPrice renders a unit price with enough significant digits to be
// meaningful for tiny per-request prices: at least 2 and at most 10 decimals.
func FormatUnitPrice(d Decimal) string {
	s := d.StringFixed(10)
	intPart, frac, _ := strings.Cut(s, ".")
	frac = strings.TrimRight(frac, "0")
	for len(frac) < 2 {
		frac += "0"
	}
	prefix := "$"
	if strings.HasPrefix(intPart, "-") {
		prefix = "-$"
		intPart = intPart[1:]
	}
	return prefix + groupThousands(intPart) + "." + frac
}

func groupThousands(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	head := len(digits) % 3
	if head > 0 {
		b.WriteString(digits[:head])
	}
	for i := head; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

func isAllZero(s string) bool {
	for _, c := range s {
		if c != '0' && c != '.' && c != '-' {
			return false
		}
	}
	return true
}
