package pricing

import "github.com/guneshm03/planprice/internal/money"

// Cost prices qty units through the tier schedule: each tier charges its rate
// for the part of qty that falls inside [Start, End). For a single-tier price
// this is simply qty * rate. Runs in O(t) for t tiers.
func (p *Price) Cost(qty money.Decimal) money.Decimal {
	total := money.Zero
	for _, t := range p.Tiers {
		if qty.Cmp(t.Start) <= 0 {
			break
		}
		upper := qty
		if t.End != nil {
			upper = upper.Min(*t.End)
		}
		total = total.Add(upper.Sub(t.Start).Mul(t.USD))
	}
	return total
}

// UnitPrice is the rate of the first tier, used for display. Tiered prices
// also report EffectiveRate for the quantity actually priced.
func (p *Price) UnitPrice() money.Decimal {
	return p.Tiers[0].USD
}

// Tiered reports whether the price has more than one band.
func (p *Price) Tiered() bool { return len(p.Tiers) > 1 }

// EffectiveRate is Cost(qty)/qty, the blended per-unit rate across tiers. It
// returns the first-tier rate when qty is zero.
func (p *Price) EffectiveRate(qty money.Decimal) money.Decimal {
	if qty.IsZero() {
		return p.UnitPrice()
	}
	r, err := p.Cost(qty).Div(qty)
	if err != nil {
		return p.UnitPrice()
	}
	return r
}
