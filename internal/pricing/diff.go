package pricing

import (
	"sort"

	"github.com/guneshm03/planprice/internal/money"
)

// PriceChange is one difference between two snapshots.
type PriceChange struct {
	Identity string         `json:"identity"`
	Unit     string         `json:"unit"`
	Old      *money.Decimal `json:"old_usd,omitempty"`
	New      *money.Decimal `json:"new_usd,omitempty"`
}

// Kind classifies the change.
func (c PriceChange) Kind() string {
	switch {
	case c.Old == nil:
		return "added"
	case c.New == nil:
		return "removed"
	default:
		return "changed"
	}
}

// DiffSnapshots lists products added, removed or re-priced between old and
// new, comparing first-tier rates (and tier counts), sorted by identity. It
// is used to review a snapshot refresh before committing it.
func DiffSnapshots(oldSnap, newSnap *Snapshot) []PriceChange {
	oldBy := map[string]*Price{}
	for i := range oldSnap.Prices {
		oldBy[oldSnap.Prices[i].Identity()] = &oldSnap.Prices[i]
	}
	var out []PriceChange
	seen := map[string]bool{}
	for i := range newSnap.Prices {
		n := &newSnap.Prices[i]
		id := n.Identity()
		seen[id] = true
		nr := n.UnitPrice()
		o, ok := oldBy[id]
		if !ok {
			out = append(out, PriceChange{Identity: id, Unit: n.Unit, New: &nr})
			continue
		}
		or := o.UnitPrice()
		if or.Cmp(nr) != 0 || len(o.Tiers) != len(n.Tiers) {
			out = append(out, PriceChange{Identity: id, Unit: n.Unit, Old: &or, New: &nr})
		}
	}
	for id, o := range oldBy {
		if !seen[id] {
			or := o.UnitPrice()
			out = append(out, PriceChange{Identity: id, Unit: o.Unit, Old: &or})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Identity < out[j].Identity })
	return out
}
