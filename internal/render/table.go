package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/guneshm03/planprice/internal/estimate"
)

func breakdownTable(w io.Writer, est *estimate.Estimate) error {
	t := &table{align: []bool{false, true, false, true, true}}
	t.add("NAME", "MONTHLY QTY", "UNIT", "UNIT PRICE", "MONTHLY COST")
	for _, r := range est.Resources {
		t.add("", "", "", "", "")
		t.add(fmt.Sprintf("%s (%s)", r.Address, r.Region), "", "", "", usd(r.Monthly))
		writeTree(t, r)
	}
	t.add("", "", "", "", "")
	t.add("OVERALL TOTAL", "", "", "", usd(est.Monthly))
	var b strings.Builder
	fmt.Fprintf(&b, "planprice: %s\n\n", est.Label)
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	if err := t.write(w); err != nil {
		return err
	}
	return writeNotes(w, est, "")
}

func writeTree(t *table, r estimate.ResourceCost) {
	type item struct {
		label    string
		children []estimate.ComponentCost
		comp     *estimate.ComponentCost
	}
	var items []item
	for i := range r.Components {
		items = append(items, item{comp: &r.Components[i]})
	}
	for _, g := range r.Groups {
		items = append(items, item{label: g.Name, children: g.Components})
	}
	for i, it := range items {
		last := i == len(items)-1
		branch, indent := "├─ ", "│  "
		if last {
			branch, indent = "└─ ", "   "
		}
		if it.comp != nil {
			qty, rate, cost := componentCells(*it.comp)
			t.add(branch+it.comp.Name, qty, it.comp.Unit, rate, cost)
			continue
		}
		t.add(branch+it.label, "", "", "", "")
		for j, c := range it.children {
			sub := "├─ "
			if j == len(it.children)-1 {
				sub = "└─ "
			}
			qty, rate, cost := componentCells(c)
			t.add(indent+sub+c.Name, qty, c.Unit, rate, cost)
		}
	}
}

func writeNotes(w io.Writer, est *estimate.Estimate, prefix string) error {
	var b strings.Builder
	s := est.Summary
	fmt.Fprintf(&b, "\n%s%s priced, %s free, %s not supported.\n", prefix,
		plural(s.SupportedResources, "resource", "resources"), plural(s.FreeResources, "resource", "resources"),
		plural(s.UnsupportedResources, "resource", "resources"))
	if m := missingUsage(est); len(m) > 0 {
		fmt.Fprintf(&b, "%s%s excluded from the total until usage is supplied (`planprice usage-template`):\n", prefix,
			plural(len(m), "usage-based component is", "usage-based components are"))
		for _, line := range m {
			fmt.Fprintf(&b, "%s  - %s\n", prefix, line)
		}
	}
	if u := unpriced(est); len(u) > 0 {
		fmt.Fprintf(&b, "%sNot priced:\n", prefix)
		for _, line := range u {
			fmt.Fprintf(&b, "%s  - %s\n", prefix, line)
		}
	}
	if sk := skippedByType(est); len(sk) > 0 {
		fmt.Fprintf(&b, "%sUnsupported resource types: %s\n", prefix, strings.Join(sk, ", "))
	}
	for _, warn := range est.Warnings {
		fmt.Fprintf(&b, "%sWarning: %s\n", prefix, warn)
	}
	fmt.Fprintf(&b, "%s* tiered price: unit price shown is the blended rate for the quantity.\n", prefix)
	fmt.Fprintf(&b, "%s%s\n", prefix, footer(est))
	_, err := io.WriteString(w, b.String())
	return err
}

func diffTable(w io.Writer, d *estimate.Diff) error {
	var b strings.Builder
	fmt.Fprintf(&b, "planprice: cost diff %s -> %s\n\n", d.Base.Label, d.Head.Label)
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	t := &table{align: []bool{false, false, true, true, true}}
	t.add("RESOURCE", "CHANGE", "BASE", "HEAD", "DIFF")
	for _, r := range d.Resources {
		if r.Kind == estimate.Unchanged {
			continue
		}
		t.add(r.Address, string(r.Kind), usdOrDash(r, true), usdOrDash(r, false), signed(r.Delta))
		for _, c := range r.Components {
			if !c.Significant() {
				continue
			}
			t.add("  "+componentLabel(c), "", usd(c.Base), usd(c.Head), signed(c.Delta))
		}
	}
	t.add("", "", "", "", "")
	t.add("MONTHLY TOTAL", "", usd(d.BaseTotal), usd(d.HeadTotal), signed(d.Delta)+" ("+percent(d.Percent)+")")
	if err := t.write(w); err != nil {
		return err
	}
	unchanged := 0
	for _, r := range d.Resources {
		if r.Kind == estimate.Unchanged {
			unchanged++
		}
	}
	b.Reset()
	fmt.Fprintf(&b, "\n%s with unchanged cost not shown.\n", plural(unchanged, "resource", "resources"))
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	return writeNotes(w, d.Head, "")
}

func usdOrDash(r estimate.ResourceDiff, base bool) string {
	if base && r.Kind == estimate.Added {
		return "-"
	}
	if !base && r.Kind == estimate.Removed {
		return "-"
	}
	if base {
		return usd(r.Base)
	}
	return usd(r.Head)
}

// componentLabel shows "old -> new" when a component was renamed (e.g. an
// instance type change), otherwise its single name.
func componentLabel(c estimate.ComponentDiff) string {
	switch {
	case c.BaseName == "":
		return c.HeadName
	case c.HeadName == "":
		return c.BaseName
	case c.BaseName != c.HeadName:
		return c.BaseName + " -> " + c.HeadName
	default:
		return c.HeadName
	}
}
