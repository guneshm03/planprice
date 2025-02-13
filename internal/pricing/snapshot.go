// Package pricing holds the offline AWS price snapshot and answers price
// lookups for cost components.
//
// A snapshot is a curated subset of the public AWS Price List Bulk API: only
// the on-demand prices that planprice's resource mappers need, for a fixed set
// of regions, with each product reduced to the handful of attributes that
// identify it. The default snapshot is embedded in the binary (see embed.go),
// so estimates are deterministic and need no network or credentials.
package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/guneshm03/planprice/internal/money"
)

// SchemaVersion is the snapshot file format version this package reads.
const SchemaVersion = 1

// Snapshot is a set of prices plus provenance for each source offer file.
type Snapshot struct {
	Schema      int     `json:"schema"`
	Currency    string  `json:"currency"`
	Source      string  `json:"source"`
	GeneratedAt string  `json:"generated_at"`
	Offers      []Offer `json:"offers"`
	Prices      []Price `json:"prices"`

	once  sync.Once
	index map[indexKey][]*Price

	mu      sync.Mutex
	byAttrs map[string]map[string][]*Price
}

// Offer records which AWS offer file (service + region + version) prices came
// from, so a reviewer can trace any number back to its source.
type Offer struct {
	Service         string `json:"service"`
	Region          string `json:"region"`
	Version         string `json:"version"`
	PublicationDate string `json:"publication_date"`
	URL             string `json:"url"`
	Products        int    `json:"products"`
}

// Price is one on-demand price: a product identified by service, region,
// product family and a few attributes, with one or more price tiers.
type Price struct {
	Service string            `json:"service"`
	Region  string            `json:"region"`
	Family  string            `json:"family"`
	Attrs   map[string]string `json:"attrs"`
	Unit    string            `json:"unit"`
	Tiers   []Tier            `json:"tiers"`
	SKU     string            `json:"sku"`
}

// Tier is a price band. End is nil for the open-ended last band ("Inf").
type Tier struct {
	Start money.Decimal  `json:"start"`
	End   *money.Decimal `json:"end,omitempty"`
	USD   money.Decimal  `json:"usd"`
}

// Query selects exactly one Price. Every attribute in Attrs must match.
type Query struct {
	Service string
	Region  string
	Family  string
	Attrs   map[string]string
}

func (q Query) String() string {
	keys := slices.Sorted(maps.Keys(q.Attrs))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+q.Attrs[k])
	}
	return fmt.Sprintf("%s/%s/%s{%s}", q.Service, q.Region, q.Family, strings.Join(parts, ","))
}

// ErrNotFound means no price matched a query.
var ErrNotFound = errors.New("price not found in snapshot")

// ErrAmbiguous means more than one price matched a query.
var ErrAmbiguous = errors.New("price query is ambiguous")

// indexKey groups prices by service/region/family; lookups then filter the
// (small) group by attributes.
type indexKey struct{ service, region, family string }

// Load decodes and validates a snapshot.
func Load(r io.Reader) (*Snapshot, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var s Snapshot
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("decode price snapshot: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Validate checks structural invariants: supported schema, non-empty
// identity fields, ordered and contiguous tiers, and unique products.
func (s *Snapshot) Validate() error {
	if s.Schema != SchemaVersion {
		return fmt.Errorf("price snapshot schema %d is not supported (want %d)", s.Schema, SchemaVersion)
	}
	if s.Currency != "USD" {
		return fmt.Errorf("price snapshot currency %q is not supported (want USD)", s.Currency)
	}
	seen := make(map[string]string, len(s.Prices))
	for i := range s.Prices {
		p := &s.Prices[i]
		if p.Service == "" || p.Region == "" || p.Family == "" || p.Unit == "" {
			return fmt.Errorf("price %d (sku %s): service, region, family and unit are required", i, p.SKU)
		}
		if err := validateTiers(p.Tiers); err != nil {
			return fmt.Errorf("price %d (sku %s): %w", i, p.SKU, err)
		}
		id := p.Identity()
		if other, dup := seen[id]; dup {
			return fmt.Errorf("duplicate price %s (skus %s and %s)", id, other, p.SKU)
		}
		seen[id] = p.SKU
	}
	return nil
}

func validateTiers(tiers []Tier) error {
	if len(tiers) == 0 {
		return errors.New("no price tiers")
	}
	if !tiers[0].Start.IsZero() {
		return errors.New("first tier must start at 0")
	}
	for i, t := range tiers {
		if t.USD.Sign() < 0 {
			return fmt.Errorf("tier %d has a negative price", i)
		}
		last := i == len(tiers)-1
		if t.End == nil {
			if !last {
				return fmt.Errorf("tier %d is open-ended but not last", i)
			}
			continue
		}
		if t.End.Cmp(t.Start) <= 0 {
			return fmt.Errorf("tier %d ends before it starts", i)
		}
		if !last && tiers[i+1].Start.Cmp(*t.End) != 0 {
			return fmt.Errorf("tier %d is not contiguous with tier %d", i, i+1)
		}
	}
	return nil
}

// Identity is a stable string that uniquely identifies a product in a
// snapshot: service, region, family and sorted attributes.
func (p *Price) Identity() string {
	return Query{Service: p.Service, Region: p.Region, Family: p.Family, Attrs: p.Attrs}.String()
}

func (s *Snapshot) buildIndex() {
	s.index = make(map[indexKey][]*Price)
	for i := range s.Prices {
		p := &s.Prices[i]
		k := indexKey{p.Service, p.Region, p.Family}
		s.index[k] = append(s.index[k], p)
	}
}

// Lookup returns the single price matching q. Attributes not named in q are
// ignored, so a query must name enough attributes to be unambiguous.
//
// The first lookup for a given (service, region, family, attribute-key set)
// builds a hash index over that group in O(g) for g prices in the group;
// every later lookup with the same key set is O(k) for k query attributes.
func (s *Snapshot) Lookup(q Query) (*Price, error) {
	s.once.Do(s.buildIndex)
	keys := slices.Sorted(maps.Keys(q.Attrs))
	idx := s.attrIndex(indexKey{q.Service, q.Region, q.Family}, keys)
	matches := idx[valueKey(q.Attrs, keys)]
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, q)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("%w: %s matches skus %s and %s", ErrAmbiguous, q, matches[0].SKU, matches[1].SKU)
	}
}

func (s *Snapshot) attrIndex(group indexKey, keys []string) map[string][]*Price {
	cacheKey := group.service + "\x00" + group.region + "\x00" + group.family + "\x00" + strings.Join(keys, "\x00")
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx, ok := s.byAttrs[cacheKey]; ok {
		return idx
	}
	idx := make(map[string][]*Price)
	for _, p := range s.index[group] {
		if !hasAll(p.Attrs, keys) {
			continue
		}
		vk := valueKey(p.Attrs, keys)
		idx[vk] = append(idx[vk], p)
	}
	if s.byAttrs == nil {
		s.byAttrs = make(map[string]map[string][]*Price)
	}
	s.byAttrs[cacheKey] = idx
	return idx
}

func hasAll(attrs map[string]string, keys []string) bool {
	for _, k := range keys {
		if _, ok := attrs[k]; !ok {
			return false
		}
	}
	return true
}

func valueKey(attrs map[string]string, keys []string) string {
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(attrs[k])
		b.WriteByte(0)
	}
	return b.String()
}

// Regions lists the regions covered by the snapshot, sorted.
func (s *Snapshot) Regions() []string {
	set := map[string]bool{}
	for _, o := range s.Offers {
		set[o.Region] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// HasRegion reports whether the snapshot has prices for region.
func (s *Snapshot) HasRegion(region string) bool {
	return slices.Contains(s.Regions(), region)
}

// AttrValues lists the distinct values of attribute key among prices in a
// service/region/family, sorted. Mappers use it for "did you mean" hints.
func (s *Snapshot) AttrValues(service, region, family, key string) []string {
	s.once.Do(s.buildIndex)
	set := map[string]bool{}
	for _, p := range s.index[indexKey{service, region, family}] {
		if v, ok := p.Attrs[key]; ok {
			set[v] = true
		}
	}
	out := slices.Collect(maps.Keys(set))
	sort.Strings(out)
	return out
}
