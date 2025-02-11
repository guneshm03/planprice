package pricegen

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/pricing"
)

// SlimFileName is the cache file name for a service's slim offer in a region.
func SlimFileName(service, region string) string {
	return service + "-" + region + ".json"
}

// RegionOffer is a slim offer together with the region it covers.
type RegionOffer struct {
	Region string
	Offer  *Offer
}

// BuildFromDir reads every service's slim offer for each region from dir and
// builds a snapshot.
func BuildFromDir(dir string, regions []string, generatedAt string) (*pricing.Snapshot, error) {
	var offers []RegionOffer
	for _, region := range regions {
		for _, svc := range Services {
			path := filepath.Join(dir, SlimFileName(svc, region))
			o, err := readSlimFile(path)
			if err != nil {
				return nil, err
			}
			offers = append(offers, RegionOffer{Region: region, Offer: o})
		}
	}
	return Build(offers, generatedAt)
}

func readSlimFile(path string) (*Offer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open slim offer (run `pricegen fetch` first): %w", err)
	}
	defer func() { _ = f.Close() }()
	o, err := ReadSlim(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return o, nil
}

// Build applies buildRules to the offers and returns a validated snapshot
// with prices sorted by identity, so identical inputs give identical bytes.
func Build(offers []RegionOffer, generatedAt string) (*pricing.Snapshot, error) {
	snap := &pricing.Snapshot{
		Schema:      pricing.SchemaVersion,
		Currency:    "USD",
		Source:      "AWS Price List Bulk API (public, on-demand terms only)",
		GeneratedAt: generatedAt,
	}
	byID := map[string]pricing.Price{}
	for _, ro := range offers {
		if !SupportedRegion(ro.Region) {
			return nil, fmt.Errorf("region %s has no usage-type prefix entry in pricegen", ro.Region)
		}
		svc := ro.Offer.OfferCode
		rules, ok := buildRules[svc]
		if !ok {
			return nil, fmt.Errorf("no build rules for service %q", svc)
		}
		kept := 0
		for _, sku := range ro.Offer.SKUs() {
			p := ro.Offer.Products[sku]
			price, ok, err := applyRules(rules, ro.Region, svc, p, ro.Offer.OnDemand[sku])
			if err != nil {
				return nil, fmt.Errorf("%s %s sku %s: %w", svc, ro.Region, sku, err)
			}
			if !ok {
				continue
			}
			id := price.Identity()
			if prev, dup := byID[id]; dup {
				if !sameTiers(prev.Tiers, price.Tiers) {
					return nil, fmt.Errorf("conflicting prices for %s (skus %s and %s)", id, prev.SKU, price.SKU)
				}
				continue // identical duplicate; keep the lower SKU (SKUs are iterated sorted)
			}
			byID[id] = price
			kept++
		}
		snap.Offers = append(snap.Offers, pricing.Offer{
			Service: svc, Region: ro.Region, Version: ro.Offer.Version,
			PublicationDate: ro.Offer.PublicationDate, URL: OfferURL(svc, ro.Region), Products: kept,
		})
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		snap.Prices = append(snap.Prices, byID[id])
	}
	sort.Slice(snap.Offers, func(i, j int) bool {
		a, b := snap.Offers[i], snap.Offers[j]
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		return a.Service < b.Service
	})
	if err := snap.Validate(); err != nil {
		return nil, err
	}
	return snap, nil
}

func applyRules(rules []rule, region, svc string, p Product, terms map[string]Term) (pricing.Price, bool, error) {
	usage := NormalizeUsageType(region, p.Attributes["usagetype"])
	for _, r := range rules {
		if r.family != p.ProductFamily || !r.match(p.Attributes, usage) {
			continue
		}
		if len(terms) != 1 {
			return pricing.Price{}, false, fmt.Errorf("expected exactly one on-demand term, found %d", len(terms))
		}
		var term Term
		for _, t := range terms {
			term = t
		}
		unit, tiers, err := convertDimensions(term.PriceDimensions)
		if err != nil {
			return pricing.Price{}, false, err
		}
		return pricing.Price{
			Service: svc, Region: region, Family: p.ProductFamily,
			Attrs: r.attrs(p.Attributes, usage), Unit: unit, Tiers: tiers, SKU: p.SKU,
		}, true, nil
	}
	return pricing.Price{}, false, nil
}

func convertDimensions(dims map[string]PriceDimension) (string, []pricing.Tier, error) {
	if len(dims) == 0 {
		return "", nil, fmt.Errorf("term has no price dimensions")
	}
	type band struct {
		start money.Decimal
		dim   PriceDimension
	}
	bands := make([]band, 0, len(dims))
	for _, d := range dims {
		start, err := money.Parse(d.BeginRange)
		if err != nil {
			return "", nil, fmt.Errorf("beginRange: %w", err)
		}
		bands = append(bands, band{start, d})
	}
	sort.Slice(bands, func(i, j int) bool { return bands[i].start.Cmp(bands[j].start) < 0 })
	unit := bands[0].dim.Unit
	tiers := make([]pricing.Tier, 0, len(bands))
	for _, b := range bands {
		if b.dim.Unit != unit {
			return "", nil, fmt.Errorf("mixed units %q and %q in one term", unit, b.dim.Unit)
		}
		usd, err := money.Parse(b.dim.PricePerUnit["USD"])
		if err != nil {
			return "", nil, fmt.Errorf("pricePerUnit: %w", err)
		}
		t := pricing.Tier{Start: b.start, USD: usd}
		if b.dim.EndRange != "Inf" {
			end, err := money.Parse(b.dim.EndRange)
			if err != nil {
				return "", nil, fmt.Errorf("endRange: %w", err)
			}
			t.End = &end
		}
		tiers = append(tiers, t)
	}
	return unit, tiers, nil
}

func sameTiers(a, b []pricing.Tier) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Start.Cmp(b[i].Start) != 0 || a[i].USD.Cmp(b[i].USD) != 0 || (a[i].End == nil) != (b[i].End == nil) {
			return false
		}
		if a[i].End != nil && a[i].End.Cmp(*b[i].End) != 0 {
			return false
		}
	}
	return true
}

// WriteSnapshot writes snap as JSON with one price per line, so a refresh
// shows up in code review as a readable line-by-line diff.
func WriteSnapshot(w io.Writer, snap *pricing.Snapshot) error {
	bw := bufio.NewWriter(w)
	header := struct {
		Schema      int             `json:"schema"`
		Currency    string          `json:"currency"`
		Source      string          `json:"source"`
		GeneratedAt string          `json:"generated_at"`
		Offers      []pricing.Offer `json:"offers"`
	}{snap.Schema, snap.Currency, snap.Source, snap.GeneratedAt, snap.Offers}
	hb, err := json.MarshalIndent(header, "", "  ")
	if err != nil {
		return err
	}
	// Re-open the header object to append the prices array.
	hb = hb[:len(hb)-2]
	if _, err := bw.Write(hb); err != nil {
		return err
	}
	if _, err := bw.WriteString(",\n  \"prices\": [\n"); err != nil {
		return err
	}
	for i, p := range snap.Prices {
		line, err := json.Marshal(p)
		if err != nil {
			return err
		}
		sep := ",\n"
		if i == len(snap.Prices)-1 {
			sep = "\n"
		}
		if _, err := bw.WriteString("    " + string(line) + sep); err != nil {
			return err
		}
	}
	if _, err := bw.WriteString("  ]\n}\n"); err != nil {
		return err
	}
	return bw.Flush()
}

// WriteChecksum writes "<sha256>  <basename>\n" to path+".sha256" and returns
// the hex digest. The pricing package verifies the embedded snapshot against
// this file in its tests, which catches hand edits to the price data.
func WriteChecksum(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	hexSum := hex.EncodeToString(sum[:])
	line := hexSum + "  " + filepath.Base(path) + "\n"
	return hexSum, os.WriteFile(path+".sha256", []byte(line), 0o644)
}
