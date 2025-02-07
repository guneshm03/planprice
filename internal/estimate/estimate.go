// Package estimate prices the resources of a Terraform plan view against a
// price snapshot and computes cost differences between two estimates.
package estimate

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/guneshm03/planprice/internal/mapper"
	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/pricing"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

// Options configures an estimate run.
type Options struct {
	Snapshot *pricing.Snapshot
	Registry *mapper.Registry
	Usage    *usage.File
	Logger   *slog.Logger
}

// Estimate is the monthly cost of one set of resources.
type Estimate struct {
	Label     string          `json:"label"`
	Currency  string          `json:"currency"`
	Monthly   money.Decimal   `json:"monthly_cost"`
	Resources []ResourceCost  `json:"resources"`
	Free      []ResourceRef   `json:"free_resources"`
	Skipped   []SkippedRef    `json:"unsupported_resources"`
	Summary   Summary         `json:"summary"`
	Warnings  []string        `json:"warnings"`
	Snapshot  SnapshotSummary `json:"price_snapshot"`
}

// SnapshotSummary identifies the price data an estimate used.
type SnapshotSummary struct {
	GeneratedAt string `json:"generated_at"`
	Source      string `json:"source"`
}

// ResourceRef names a resource that has no cost.
type ResourceRef struct {
	Address string `json:"address"`
	Type    string `json:"type"`
}

// SkippedRef names a resource planprice cannot price, and why.
type SkippedRef struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Reason  string `json:"reason"`
}

// Summary counts components by outcome.
type Summary struct {
	SupportedResources   int `json:"supported_resources"`
	FreeResources        int `json:"free_resources"`
	UnsupportedResources int `json:"unsupported_resources"`
	PricedComponents     int `json:"priced_components"`
	UsageMissing         int `json:"components_missing_usage"`
	Unpriced             int `json:"components_not_priced"`
}

// ResourceCost is the breakdown for one resource instance.
type ResourceCost struct {
	Address      string          `json:"address"`
	Type         string          `json:"type"`
	Region       string          `json:"region"`
	RegionSource string          `json:"region_source"`
	Action       tfplan.Action   `json:"action"`
	Monthly      money.Decimal   `json:"monthly_cost"`
	Components   []ComponentCost `json:"components"`
	Groups       []GroupCost     `json:"groups,omitempty"`
	Notes        []string        `json:"notes,omitempty"`
	UsageSources []string        `json:"usage_sources,omitempty"`
}

// GroupCost is a priced mapper.Group.
type GroupCost struct {
	Key        string          `json:"key"`
	Name       string          `json:"name"`
	Monthly    money.Decimal   `json:"monthly_cost"`
	Components []ComponentCost `json:"components"`
}

// ComponentCost is a priced (or explicitly unpriced) cost component.
type ComponentCost struct {
	Key        string         `json:"key"`
	Name       string         `json:"name"`
	Unit       string         `json:"unit"`
	Quantity   money.Decimal  `json:"monthly_quantity"`
	UnitPrice  *money.Decimal `json:"unit_price,omitempty"`
	Monthly    *money.Decimal `json:"monthly_cost,omitempty"`
	Tiered     bool           `json:"tiered,omitempty"`
	UsageBased bool           `json:"usage_based,omitempty"`
	Status     mapper.Status  `json:"status"`
	Reason     string         `json:"reason,omitempty"`
	SKU        string         `json:"sku,omitempty"`
}

// Run prices every resource. Unsupported and free resources are listed, not
// priced. Errors are returned only for invalid input (e.g. a usage file that
// fails validation); a missing price is a per-component status, not an error.
func Run(label string, resources []tfplan.Resource, opts Options) (*Estimate, error) {
	if opts.Snapshot == nil || opts.Registry == nil {
		return nil, errors.New("estimate: snapshot and registry are required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	est := &Estimate{
		Label: label, Currency: opts.Snapshot.Currency,
		Snapshot:  SnapshotSummary{GeneratedAt: opts.Snapshot.GeneratedAt, Source: opts.Snapshot.Source},
		Resources: []ResourceCost{}, Free: []ResourceRef{}, Skipped: []SkippedRef{}, Warnings: []string{},
	}
	regionsWarned := map[string]bool{}
	for _, r := range resources {
		m, ok := opts.Registry.Lookup(r.Type)
		switch {
		case ok:
		case mapper.IsFree(r.Type):
			est.Free = append(est.Free, ResourceRef{Address: r.Address, Type: r.Type})
			continue
		case !r.IsAWS():
			est.Skipped = append(est.Skipped, SkippedRef{Address: r.Address, Type: r.Type, Reason: "not an AWS resource"})
			continue
		default:
			est.Skipped = append(est.Skipped, SkippedRef{Address: r.Address, Type: r.Type, Reason: "resource type not supported"})
			continue
		}
		if !opts.Snapshot.HasRegion(r.Region) && !regionsWarned[r.Region] {
			regionsWarned[r.Region] = true
			est.Warnings = append(est.Warnings, fmt.Sprintf("region %s is not in the price snapshot (covered: %v); its resources are not priced", r.Region, opts.Snapshot.Regions()))
		}
		u := opts.Usage.For(r.Address, r.Type)
		result := m.Map(r, u)
		rc := ResourceCost{
			Address: r.Address, Type: r.Type, Region: r.Region, RegionSource: string(r.RegionSource),
			Action: r.Action, Notes: result.Notes, UsageSources: u.Sources(),
		}
		for _, c := range result.Components {
			cc := price(opts.Snapshot, c, log)
			rc.Components = append(rc.Components, cc)
			rc.Monthly = rc.Monthly.Add(costOf(cc))
		}
		for _, g := range result.Groups {
			gc := GroupCost{Key: g.Key, Name: g.Name}
			for _, c := range g.Components {
				cc := price(opts.Snapshot, c, log)
				gc.Components = append(gc.Components, cc)
				gc.Monthly = gc.Monthly.Add(costOf(cc))
			}
			rc.Groups = append(rc.Groups, gc)
			rc.Monthly = rc.Monthly.Add(gc.Monthly)
		}
		est.Resources = append(est.Resources, rc)
		est.Monthly = est.Monthly.Add(rc.Monthly)
	}
	sort.SliceStable(est.Resources, func(i, j int) bool { return est.Resources[i].Address < est.Resources[j].Address })
	est.Summary = summarize(est)
	return est, nil
}

// price looks up the component's price and computes its monthly cost.
func price(snap *pricing.Snapshot, c mapper.Component, log *slog.Logger) ComponentCost {
	cc := ComponentCost{
		Key: c.Key, Name: c.Name, Unit: c.Unit, Quantity: c.Quantity, UsageBased: c.UsageBased,
		Status: c.Status, Reason: c.Reason,
	}
	if c.Status == mapper.StatusUsageMissing {
		// Still show the rate, so users can see what the missing usage costs.
		if p, err := snap.Lookup(c.Query); err == nil {
			rate := p.UnitPrice().Mul(c.UnitScale())
			cc.UnitPrice, cc.Tiered, cc.SKU = &rate, p.Tiered(), p.SKU
		}
		return cc
	}
	if c.Status != mapper.StatusReady {
		return cc
	}
	p, err := snap.Lookup(c.Query)
	if err != nil {
		cc.Status = mapper.StatusPriceNotFound
		cc.Reason = err.Error()
		log.Warn("no price for component", "component", c.Name, "error", err)
		return cc
	}
	qty := c.PricedQuantity()
	cost := p.Cost(qty)
	rate := p.EffectiveRate(qty).Mul(c.UnitScale())
	cc.Status = mapper.StatusPriced
	cc.Monthly = &cost
	cc.UnitPrice = &rate
	cc.Tiered = p.Tiered()
	cc.SKU = p.SKU
	return cc
}

func costOf(c ComponentCost) money.Decimal {
	if c.Monthly == nil {
		return money.Zero
	}
	return *c.Monthly
}

func summarize(est *Estimate) Summary {
	s := Summary{
		SupportedResources: len(est.Resources), FreeResources: len(est.Free), UnsupportedResources: len(est.Skipped),
	}
	count := func(c ComponentCost) {
		switch c.Status {
		case mapper.StatusPriced:
			s.PricedComponents++
		case mapper.StatusUsageMissing:
			s.UsageMissing++
		default:
			s.Unpriced++
		}
	}
	for _, r := range est.Resources {
		for _, c := range r.AllComponents() {
			count(c)
		}
	}
	return s
}

// AllComponents returns the resource's components followed by those of its
// groups, in display order.
func (r ResourceCost) AllComponents() []ComponentCost {
	out := append([]ComponentCost(nil), r.Components...)
	for _, g := range r.Groups {
		out = append(out, g.Components...)
	}
	return out
}
