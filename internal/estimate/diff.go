package estimate

import (
	"sort"

	"github.com/guneshm03/planprice/internal/money"
)

// ChangeKind classifies a resource in a diff.
type ChangeKind string

// Diff change kinds.
const (
	Added     ChangeKind = "added"
	Removed   ChangeKind = "removed"
	Changed   ChangeKind = "changed"
	Unchanged ChangeKind = "unchanged"
)

// Diff compares two estimates resource by resource and component by
// component.
type Diff struct {
	Base      *Estimate      `json:"base"`
	Head      *Estimate      `json:"head"`
	BaseTotal money.Decimal  `json:"base_monthly_cost"`
	HeadTotal money.Decimal  `json:"head_monthly_cost"`
	Delta     money.Decimal  `json:"delta_monthly_cost"`
	Percent   *float64       `json:"delta_percent,omitempty"`
	Resources []ResourceDiff `json:"resources"`
}

// ResourceDiff is one resource's cost change.
type ResourceDiff struct {
	Address    string          `json:"address"`
	Type       string          `json:"type"`
	Kind       ChangeKind      `json:"kind"`
	Base       money.Decimal   `json:"base_monthly_cost"`
	Head       money.Decimal   `json:"head_monthly_cost"`
	Delta      money.Decimal   `json:"delta_monthly_cost"`
	Components []ComponentDiff `json:"components"`
}

// ComponentDiff is one component's cost change, matched by group and
// component key so a rename such as "t3.medium" -> "m6i.large" is a change,
// not a removal plus an addition.
type ComponentDiff struct {
	Path     string        `json:"path"`
	BaseName string        `json:"base_name,omitempty"`
	HeadName string        `json:"head_name,omitempty"`
	Base     money.Decimal `json:"base_monthly_cost"`
	Head     money.Decimal `json:"head_monthly_cost"`
	Delta    money.Decimal `json:"delta_monthly_cost"`
}

// Compare diffs base against head. Resources are matched by address. The
// result lists every resource in either estimate, sorted by address, and
// runs in O((n+m) log(n+m)) for n and m resources.
func Compare(base, head *Estimate) *Diff {
	d := &Diff{Base: base, Head: head, BaseTotal: base.Monthly, HeadTotal: head.Monthly, Resources: []ResourceDiff{}}
	d.Delta = head.Monthly.Sub(base.Monthly)
	if !base.Monthly.IsZero() {
		pct, _ := d.Delta.Div(base.Monthly)
		f := pct.Float64() * 100
		d.Percent = &f
	}
	baseBy := indexResources(base.Resources)
	headBy := indexResources(head.Resources)
	addrs := make([]string, 0, len(baseBy)+len(headBy))
	for a := range baseBy {
		addrs = append(addrs, a)
	}
	for a := range headBy {
		if _, ok := baseBy[a]; !ok {
			addrs = append(addrs, a)
		}
	}
	sort.Strings(addrs)
	for _, a := range addrs {
		b, inBase := baseBy[a]
		h, inHead := headBy[a]
		rd := ResourceDiff{Address: a}
		switch {
		case inBase && inHead:
			rd.Type, rd.Base, rd.Head = h.Type, b.Monthly, h.Monthly
		case inHead:
			rd.Type, rd.Head, rd.Kind = h.Type, h.Monthly, Added
		default:
			rd.Type, rd.Base, rd.Kind = b.Type, b.Monthly, Removed
		}
		rd.Delta = rd.Head.Sub(rd.Base)
		rd.Components = diffComponents(b, h)
		if rd.Kind == "" {
			rd.Kind = Unchanged
			for _, c := range rd.Components {
				if c.Significant() {
					rd.Kind = Changed
					break
				}
			}
		}
		d.Resources = append(d.Resources, rd)
	}
	return d
}

// Significant reports whether a component change is worth showing: its
// cost changed, or a component with a non-zero cost was renamed (for
// example t3.medium -> t3a.medium at the same price).
func (c ComponentDiff) Significant() bool {
	if !c.Delta.IsZero() {
		return true
	}
	renamed := c.BaseName != "" && c.HeadName != "" && c.BaseName != c.HeadName
	return renamed && (!c.Base.IsZero() || !c.Head.IsZero())
}

func indexResources(rs []ResourceCost) map[string]*ResourceCost {
	m := make(map[string]*ResourceCost, len(rs))
	for i := range rs {
		m[rs[i].Address] = &rs[i]
	}
	return m
}

type flatComponent struct {
	path string
	name string
	cost money.Decimal
}

func flatten(r *ResourceCost) []flatComponent {
	if r == nil {
		return nil
	}
	var out []flatComponent
	for _, c := range r.Components {
		out = append(out, flatComponent{c.Key, c.Name, costOf(c)})
	}
	for _, g := range r.Groups {
		for _, c := range g.Components {
			out = append(out, flatComponent{g.Key + "/" + c.Key, g.Name + " / " + c.Name, costOf(c)})
		}
	}
	return out
}

func diffComponents(base, head *ResourceCost) []ComponentDiff {
	bs, hs := flatten(base), flatten(head)
	byPath := map[string]*ComponentDiff{}
	var order []string
	get := func(path string) *ComponentDiff {
		if cd, ok := byPath[path]; ok {
			return cd
		}
		cd := &ComponentDiff{Path: path}
		byPath[path] = cd
		order = append(order, path)
		return cd
	}
	for _, c := range bs {
		cd := get(c.path)
		cd.BaseName, cd.Base = c.name, cd.Base.Add(c.cost)
	}
	for _, c := range hs {
		cd := get(c.path)
		cd.HeadName, cd.Head = c.name, cd.Head.Add(c.cost)
	}
	out := make([]ComponentDiff, 0, len(order))
	for _, p := range order {
		cd := byPath[p]
		cd.Delta = cd.Head.Sub(cd.Base)
		out = append(out, *cd)
	}
	return out
}

// Exceeds reports whether the diff's increase is above either threshold.
// A nil threshold is ignored. Percent thresholds apply only when the base
// cost is non-zero.
func (d *Diff) Exceeds(maxIncrease *money.Decimal, maxPercent *float64) bool {
	if maxIncrease != nil && d.Delta.Cmp(*maxIncrease) > 0 {
		return true
	}
	if maxPercent != nil && d.Percent != nil && *d.Percent > *maxPercent {
		return true
	}
	return false
}
