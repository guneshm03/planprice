package estimate

import (
	"bytes"
	"log/slog"
	"math/rand/v2"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/guneshm03/planprice/internal/mapper"
	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/pricedata"
	"github.com/guneshm03/planprice/internal/pricing"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

func opts(t testing.TB, u *usage.File) Options {
	t.Helper()
	snap, err := pricedata.Default()
	if err != nil {
		t.Fatal(err)
	}
	return Options{Snapshot: snap, Registry: mapper.NewRegistry(), Usage: u}
}

func planResources(t testing.TB, name string, view tfplan.View) []tfplan.Resource {
	t.Helper()
	p, err := tfplan.ParseFile(filepath.Join("..", "..", "testdata", "plans", name))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := p.Resources(view, "us-east-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func loadUsage(t testing.TB) *usage.File {
	t.Helper()
	u, err := usage.Load(filepath.Join("..", "..", "examples", "usage.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestBasePlanTotalMatchesHandCalculation(t *testing.T) {
	est, err := Run("base", planResources(t, "base.json", tfplan.After), opts(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	// 2 x (t3.medium 730*0.0416 + 30 GB gp3*0.08) = 65.536
	// + RDS db.t3.medium 52.56 + 100 GB gp3 11.5    = 64.06
	// + ALB 730*0.0225 = 16.425, NAT 730*0.045 = 32.85
	// + 200 GB gp2 scratch volume = 20                 -> 198.871
	if est.Monthly.Cmp(money.MustParse("198.871")) != 0 {
		t.Fatalf("base total %s", est.Monthly)
	}
	if est.Summary.SupportedResources != 9 || est.Summary.FreeResources != 7 || est.Summary.UnsupportedResources != 1 {
		t.Fatalf("summary %+v", est.Summary)
	}
	if est.Skipped[0].Type != "aws_eip" {
		t.Fatalf("skipped %+v", est.Skipped)
	}
	for i := 1; i < len(est.Resources); i++ {
		if est.Resources[i-1].Address > est.Resources[i].Address {
			t.Fatal("resources not sorted by address")
		}
	}
}

// The total must equal the sum of every priced component (no rounding drift).
func TestTotalsAreExactSums(t *testing.T) {
	for _, name := range []string{"base.json", "head.json", "multi-region.json"} {
		est, err := Run(name, planResources(t, name, tfplan.After), opts(t, loadUsage(t)))
		if err != nil {
			t.Fatal(err)
		}
		sum := money.Zero
		for _, r := range est.Resources {
			rsum := money.Zero
			for _, c := range r.AllComponents() {
				if c.Monthly != nil {
					rsum = rsum.Add(*c.Monthly)
				}
			}
			if rsum.Cmp(r.Monthly) != 0 {
				t.Errorf("%s %s: components %s != resource %s", name, r.Address, rsum, r.Monthly)
			}
			sum = sum.Add(rsum)
		}
		if sum.Cmp(est.Monthly) != 0 {
			t.Errorf("%s: components %s != total %s", name, sum, est.Monthly)
		}
	}
}

func TestClassificationAndWarnings(t *testing.T) {
	p, err := tfplan.ParseFile(filepath.Join("..", "tfplan", "testdata", "synthetic-v5.json"))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := p.Resources(tfplan.After, "ap-south-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	o := opts(t, nil)
	o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	est, err := Run("synthetic", rs, o)
	if err != nil {
		t.Fatal(err)
	}
	skipped := map[string]string{}
	for _, s := range est.Skipped {
		skipped[s.Address] = s.Reason
	}
	if skipped["google_compute_instance.vm"] != "not an AWS resource" {
		t.Errorf("skipped: %v", skipped)
	}
	// aws_lb.unknown_region falls back to ap-south-1, which the snapshot
	// does not cover: one warning, and its components are not priced.
	if len(est.Warnings) != 1 || !strings.Contains(est.Warnings[0], "ap-south-1") {
		t.Fatalf("warnings: %v", est.Warnings)
	}
	for _, r := range est.Resources {
		if r.Address == "aws_lb.unknown_region" {
			if r.Components[0].Status != mapper.StatusPriceNotFound || !r.Monthly.IsZero() {
				t.Errorf("unknown-region component: %+v", r.Components[0])
			}
		}
	}
	if !strings.Contains(logs.String(), "no price for component") {
		t.Errorf("missing price not logged: %s", logs.String())
	}
	if est.Summary.Unpriced == 0 {
		t.Error("summary does not count unpriced components")
	}
	if _, err := Run("x", nil, Options{}); err == nil {
		t.Error("missing snapshot accepted")
	}
}

func TestUnsupportedTypeIsListed(t *testing.T) {
	rs := []tfplan.Resource{{Address: "aws_cloudfront_distribution.cdn", Type: "aws_cloudfront_distribution", Region: "us-east-1"}}
	est, err := Run("x", rs, opts(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(est.Skipped) != 1 || est.Skipped[0].Reason != "resource type not supported" || len(est.Resources) != 0 {
		t.Fatalf("%+v", est)
	}
}

func TestDiffBaseToHead(t *testing.T) {
	u := loadUsage(t)
	base, err := Run("base", planResources(t, "base.json", tfplan.After), opts(t, u))
	if err != nil {
		t.Fatal(err)
	}
	head, err := Run("head", planResources(t, "head.json", tfplan.After), opts(t, u))
	if err != nil {
		t.Fatal(err)
	}
	d := Compare(base, head)
	if d.Delta.Cmp(head.Monthly.Sub(base.Monthly)) != 0 {
		t.Fatal("delta mismatch")
	}
	kinds := map[string]ChangeKind{}
	sum := money.Zero
	for _, r := range d.Resources {
		kinds[r.Address] = r.Kind
		sum = sum.Add(r.Delta)
	}
	if sum.Cmp(d.Delta) != 0 {
		t.Fatalf("resource deltas %s != total delta %s", sum, d.Delta)
	}
	want := map[string]ChangeKind{
		"aws_ebs_volume.journal":               Added,
		"aws_ebs_volume.scratch":               Changed,
		"module.web.aws_instance.web[2]":       Added,
		"module.web.aws_nat_gateway.egress[0]": Unchanged,
		"module.data.aws_s3_bucket.assets":     Unchanged,
		"module.data.aws_db_instance.main":     Changed,
	}
	for addr, k := range want {
		if kinds[addr] != k {
			t.Errorf("%s: %s, want %s", addr, kinds[addr], k)
		}
	}
	for _, r := range d.Resources {
		if r.Address != "module.web.aws_instance.web[0]" {
			continue
		}
		// The instance component matches across the type change by key.
		c := r.Components[0]
		if c.Path != "instance" || !strings.Contains(c.BaseName, "t3.medium") || !strings.Contains(c.HeadName, "m6i.large") {
			t.Fatalf("instance component diff: %+v", c)
		}
		if c.Delta.Cmp(money.MustParse("39.712")) != 0 { // 70.08 - 30.368
			t.Fatalf("instance delta %s", c.Delta)
		}
	}
	if d.Percent == nil || *d.Percent < 600 {
		t.Fatalf("percent %v", d.Percent)
	}
}

func TestSinglePlanBeforeAfterDiff(t *testing.T) {
	p, err := tfplan.ParseFile(filepath.Join("..", "tfplan", "testdata", "synthetic-v5.json"))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := p.Resources(tfplan.Before, "us-east-1", nil)
	after, _ := p.Resources(tfplan.After, "us-east-1", nil)
	b, err := Run("before", before, opts(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Run("after", after, opts(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	d := Compare(b, a)
	kinds := map[string]ChangeKind{}
	for _, r := range d.Resources {
		kinds[r.Address] = r.Kind
	}
	if kinds["aws_ebs_volume.old"] != Removed || kinds["aws_instance.app"] != Changed || kinds["aws_db_instance.db"] != Changed {
		t.Fatalf("kinds %v", kinds)
	}
	// The deleted volume lived in us-west-2 (provider alias), which the
	// snapshot does not cover: it is listed, unpriced, with a warning.
	if len(b.Warnings) != 1 || !strings.Contains(b.Warnings[0], "us-west-2") {
		t.Fatalf("before warnings: %v", b.Warnings)
	}
	for _, r := range d.Resources {
		if r.Address == "aws_ebs_volume.old" && !r.Base.IsZero() {
			t.Fatalf("unpriced region produced a cost: %s", r.Base)
		}
		if r.Address == "aws_instance.app" {
			// eu-west-1 (from var.region): t3.small -> t3.large, same 20 GB gp3 root.
			if r.Components[0].Path != "instance" || r.Delta.Sign() <= 0 {
				t.Fatalf("instance resize: %+v", r)
			}
		}
	}
}

func TestExceedsThresholds(t *testing.T) {
	d := &Diff{Delta: money.FromInt(150)}
	pct := 20.0
	d.Percent = &pct
	limit := money.FromInt(100)
	high := money.FromInt(200)
	lowPct, highPct := 10.0, 50.0
	cases := []struct {
		usd  *money.Decimal
		pct  *float64
		want bool
	}{
		{nil, nil, false},
		{&limit, nil, true},
		{&high, nil, false},
		{nil, &lowPct, true},
		{nil, &highPct, false},
		{&high, &lowPct, true},
	}
	for i, c := range cases {
		if got := d.Exceeds(c.usd, c.pct); got != c.want {
			t.Errorf("case %d: %v", i, got)
		}
	}
	zeroBase := Compare(&Estimate{}, &Estimate{Monthly: money.FromInt(5)})
	if zeroBase.Percent != nil || zeroBase.Exceeds(nil, &lowPct) {
		t.Error("percent threshold should not apply to a zero base")
	}
}

func TestComponentSignificance(t *testing.T) {
	cases := []struct {
		c    ComponentDiff
		want bool
	}{
		{ComponentDiff{Delta: money.FromInt(1)}, true},
		{ComponentDiff{BaseName: "a", HeadName: "b", Base: money.FromInt(3), Head: money.FromInt(3)}, true},
		{ComponentDiff{BaseName: "a", HeadName: "b"}, false},
		{ComponentDiff{BaseName: "", HeadName: "b"}, false},
		{ComponentDiff{BaseName: "a", HeadName: "a", Base: money.FromInt(3), Head: money.FromInt(3)}, false},
	}
	for i, c := range cases {
		if got := c.c.Significant(); got != c.want {
			t.Errorf("case %d: %v", i, got)
		}
	}
}

// Properties over random subsets of the head plan: a self-diff is all
// zero/unchanged, and diff(a,b) is the negation of diff(b,a).
func TestDiffProperties(t *testing.T) {
	all := planResources(t, "head.json", tfplan.After)
	o := opts(t, loadUsage(t))
	rng := rand.New(rand.NewPCG(1, 2))
	subset := func() []tfplan.Resource {
		var out []tfplan.Resource
		for _, r := range all {
			if rng.IntN(2) == 0 {
				out = append(out, r)
			}
		}
		return out
	}
	for i := 0; i < 50; i++ {
		a, err := Run("a", subset(), o)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Run("b", subset(), o)
		if err != nil {
			t.Fatal(err)
		}
		self := Compare(a, a)
		if !self.Delta.IsZero() {
			t.Fatal("self diff non-zero")
		}
		for _, r := range self.Resources {
			if r.Kind != Unchanged {
				t.Fatalf("self diff %s is %s", r.Address, r.Kind)
			}
		}
		ab, ba := Compare(a, b), Compare(b, a)
		if ab.Delta.Cmp(ba.Delta.Neg()) != 0 || len(ab.Resources) != len(ba.Resources) {
			t.Fatal("diff not antisymmetric")
		}
		for j := range ab.Resources {
			x, y := ab.Resources[j], ba.Resources[j]
			if x.Address != y.Address || x.Delta.Cmp(y.Delta.Neg()) != 0 {
				t.Fatalf("resource %s not antisymmetric", x.Address)
			}
			if (x.Kind == Added) != (y.Kind == Removed) {
				t.Fatalf("%s: %s vs %s", x.Address, x.Kind, y.Kind)
			}
		}
	}
}

func TestPriceShowsRateForUsageMissing(t *testing.T) {
	snap, _ := pricedata.Default()
	c := price(snap, mapper.Component{Key: "k", Name: "n", Status: mapper.StatusUsageMissing,
		Query: pricing.Query{Service: "AmazonEC2", Region: "us-east-1", Family: "NAT Gateway", Attrs: map[string]string{"usagetype": "NatGateway-Bytes"}}},
		slog.New(slog.DiscardHandler))
	if c.UnitPrice == nil || c.UnitPrice.String() != "0.045" || c.Monthly != nil {
		t.Fatalf("%+v", c)
	}
}

func BenchmarkEstimateHeadPlan(b *testing.B) {
	rs := planResources(b, "head.json", tfplan.After)
	o := opts(b, loadUsage(b))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Run("head", rs, o); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEstimate5000Instances prices a synthetic plan of 5,000 EC2
// instances (each with a root volume) to show the engine scales linearly:
// each component costs one hashed snapshot lookup.
func BenchmarkEstimate5000Instances(b *testing.B) {
	types := []string{"t3.micro", "t3.medium", "m6i.large", "m6i.xlarge", "c6i.large", "r6i.large", "m7g.large", "c7g.xlarge"}
	rs := make([]tfplan.Resource, 0, 5000)
	for i := 0; i < 5000; i++ {
		attrs := map[string]any{
			"instance_type":     types[i%len(types)],
			"root_block_device": []any{map[string]any{"volume_type": "gp3", "volume_size": 20 + i%100}},
		}
		rs = append(rs, tfplan.Resource{
			Address: "aws_instance.fleet[" + strconv.Itoa(i) + "]", Type: "aws_instance",
			Region: "us-east-1", Action: tfplan.ActionCreate, Attrs: tfplan.NewAttrs(attrs, nil),
		})
	}
	o := opts(b, nil)
	b.ReportAllocs()
	for b.Loop() {
		est, err := Run("fleet", rs, o)
		if err != nil {
			b.Fatal(err)
		}
		if est.Summary.Unpriced != 0 {
			b.Fatalf("unpriced components: %d", est.Summary.Unpriced)
		}
	}
}
