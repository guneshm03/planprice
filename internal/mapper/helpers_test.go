package mapper_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/guneshm03/planprice/internal/estimate"
	"github.com/guneshm03/planprice/internal/mapper"
	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/pricedata"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

// res describes one resource to price in a mapper test.
type res struct {
	typ     string
	region  string
	attrs   map[string]any
	unknown map[string]any
	usage   string // YAML body for this resource's resource_usage entry
}

// price runs a single resource through its mapper and the embedded price
// snapshot, exactly as `planprice breakdown` would.
func price(t *testing.T, r res) estimate.ResourceCost {
	t.Helper()
	snap, err := pricedata.Default()
	if err != nil {
		t.Fatal(err)
	}
	region := r.region
	if region == "" {
		region = "us-east-1"
	}
	addr := r.typ + ".test"
	var uf *usage.File
	if r.usage != "" {
		var b strings.Builder
		b.WriteString("version: 1\nresource_usage:\n  " + addr + ":\n")
		for _, line := range strings.Split(strings.TrimSpace(r.usage), "\n") {
			b.WriteString("    " + line + "\n")
		}
		uf, err = usage.Parse(strings.NewReader(b.String()))
		if err != nil {
			t.Fatal(err)
		}
		reg := mapper.NewRegistry()
		if _, err := uf.Validate(reg.UsageSchema); err != nil {
			t.Fatal(err)
		}
	}
	resource := tfplan.Resource{
		Address: addr, Type: r.typ, Region: region, RegionSource: tfplan.RegionFromAttribute,
		Action: tfplan.ActionCreate, Attrs: tfplan.NewAttrs(r.attrs, r.unknown),
	}
	est, err := estimate.Run("test", []tfplan.Resource{resource}, estimate.Options{
		Snapshot: snap, Registry: mapper.NewRegistry(), Usage: uf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(est.Resources) != 1 {
		t.Fatalf("expected one priced resource, got %+v", est)
	}
	return est.Resources[0]
}

// component finds a component by "key" or "group/key".
func component(t *testing.T, rc estimate.ResourceCost, path string) estimate.ComponentCost {
	t.Helper()
	// Group keys may contain "/" (device names), so split on the last one.
	group, key, grouped := path, "", false
	if i := strings.LastIndex(path, "/"); i >= 0 {
		group, key, grouped = path[:i], path[i+1:], true
	}
	if !grouped {
		for _, c := range rc.Components {
			if c.Key == path {
				return c
			}
		}
	}
	for _, g := range rc.Groups {
		if g.Key != group {
			continue
		}
		for _, c := range g.Components {
			if c.Key == key {
				return c
			}
		}
	}
	t.Fatalf("%s: no component %q in %s", rc.Address, path, describe(rc))
	return estimate.ComponentCost{}
}

func describe(rc estimate.ResourceCost) string {
	var parts []string
	for _, c := range rc.Components {
		parts = append(parts, c.Key)
	}
	for _, g := range rc.Groups {
		for _, c := range g.Components {
			parts = append(parts, g.Key+"/"+c.Key)
		}
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func hasComponent(rc estimate.ResourceCost, key string) bool {
	return slices.ContainsFunc(rc.AllComponents(), func(c estimate.ComponentCost) bool { return c.Key == key })
}

// costIs asserts a priced component's monthly cost equals want exactly.
func costIs(t *testing.T, rc estimate.ResourceCost, path, want string) {
	t.Helper()
	c := component(t, rc, path)
	if c.Status != mapper.StatusPriced {
		t.Fatalf("%s %s: status %s (%s)", rc.Address, path, c.Status, c.Reason)
	}
	if c.Monthly.Cmp(money.MustParse(want)) != 0 {
		t.Errorf("%s %s: monthly cost %s, want %s", rc.Address, path, c.Monthly, want)
	}
}

func statusIs(t *testing.T, rc estimate.ResourceCost, path string, want mapper.Status) {
	t.Helper()
	if c := component(t, rc, path); c.Status != want {
		t.Errorf("%s %s: status %s (%s), want %s", rc.Address, path, c.Status, c.Reason, want)
	}
}

func totalIs(t *testing.T, rc estimate.ResourceCost, want string) {
	t.Helper()
	if rc.Monthly.Cmp(money.MustParse(want)) != 0 {
		t.Errorf("%s: total %s, want %s", rc.Address, rc.Monthly, want)
	}
}
