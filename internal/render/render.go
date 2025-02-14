// Package render formats estimates and diffs as terminal tables, Markdown
// (for pull-request comments) and JSON.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/guneshm03/planprice/internal/estimate"
	"github.com/guneshm03/planprice/internal/mapper"
	"github.com/guneshm03/planprice/internal/money"
)

// Format is an output format.
type Format string

// Output formats.
const (
	Table    Format = "table"
	Markdown Format = "markdown"
	JSON     Format = "json"
)

// ParseFormat validates a --format value.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case Table, Markdown, JSON:
		return Format(s), nil
	}
	return "", fmt.Errorf("unknown format %q (want table, markdown or json)", s)
}

// CommentMarker is embedded in Markdown diffs so a CI job can find and update
// its previous pull-request comment instead of posting a new one.
const CommentMarker = "<!-- planprice:cost-diff -->"

// Breakdown writes one estimate in the given format.
func Breakdown(w io.Writer, est *estimate.Estimate, f Format) error {
	switch f {
	case JSON:
		return writeJSON(w, est)
	case Markdown:
		return breakdownMarkdown(w, est)
	default:
		return breakdownTable(w, est)
	}
}

// DiffReport writes a diff in the given format.
func DiffReport(w io.Writer, d *estimate.Diff, f Format) error {
	switch f {
	case JSON:
		return writeJSON(w, d)
	case Markdown:
		return diffMarkdown(w, d)
	default:
		return diffTable(w, d)
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func usd(d money.Decimal) string { return money.FormatUSD(d, 2) }

func signed(d money.Decimal) string { return money.FormatSignedUSD(d, 2) }

func percent(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%+.1f%%", *p)
}

// componentCells returns quantity, unit price and monthly cost cells.
func componentCells(c estimate.ComponentCost) (qty, rate, cost string) {
	rate = "-"
	if c.UnitPrice != nil {
		rate = money.FormatUnitPrice(*c.UnitPrice)
		if c.Tiered {
			rate += "*"
		}
	}
	switch c.Status {
	case mapper.StatusPriced:
		return money.FormatQuantity(c.Quantity, 4), rate, usd(*c.Monthly)
	case mapper.StatusUsageMissing:
		return "-", rate, "usage needed"
	case mapper.StatusUnknown:
		return "-", rate, "unknown"
	case mapper.StatusPriceNotFound:
		return money.FormatQuantity(c.Quantity, 4), rate, "no price"
	default:
		return "-", rate, "not priced"
	}
}

// table is a minimal column aligner that measures runes, not bytes, so the
// box-drawing tree characters line up.
type table struct {
	rows  [][]string
	align []bool // true = right-align
}

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *table) write(w io.Writer) error {
	widths := make([]int, len(t.align))
	for _, r := range t.rows {
		for i, c := range r {
			if n := utf8.RuneCountInString(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	var b strings.Builder
	for _, r := range t.rows {
		var line strings.Builder
		for i, c := range r {
			pad := strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c))
			if i > 0 {
				line.WriteString("  ")
			}
			if t.align[i] {
				line.WriteString(pad + c)
			} else {
				line.WriteString(c + pad)
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func footer(est *estimate.Estimate) string {
	return fmt.Sprintf("Prices: %s, snapshot %s; 730 hours/month.", est.Snapshot.Source, est.Snapshot.GeneratedAt)
}

// missingUsage lists "address: component (hint)" for usage-missing components.
func missingUsage(est *estimate.Estimate) []string {
	var out []string
	for _, r := range est.Resources {
		for _, c := range r.AllComponents() {
			if c.Status == mapper.StatusUsageMissing {
				out = append(out, fmt.Sprintf("%s: %s (%s)", r.Address, c.Name, c.Reason))
			}
		}
	}
	return out
}

// unpriced lists components that could not be priced for reasons other than
// missing usage.
func unpriced(est *estimate.Estimate) []string {
	var out []string
	for _, r := range est.Resources {
		for _, c := range r.AllComponents() {
			switch c.Status {
			case mapper.StatusPriced, mapper.StatusUsageMissing:
			default:
				out = append(out, fmt.Sprintf("%s: %s (%s: %s)", r.Address, c.Name, c.Status, c.Reason))
			}
		}
	}
	return out
}

// skippedByType groups unsupported resources as "aws_eip (3)".
func skippedByType(est *estimate.Estimate) []string {
	counts := map[string]int{}
	var order []string
	for _, s := range est.Skipped {
		if counts[s.Type] == 0 {
			order = append(order, s.Type)
		}
		counts[s.Type]++
	}
	out := make([]string, 0, len(order))
	for _, t := range order {
		out = append(out, fmt.Sprintf("%s (%d)", t, counts[t]))
	}
	return out
}
