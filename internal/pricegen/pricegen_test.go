package pricegen

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guneshm03/planprice/internal/pricing"
)

func readFixture(t *testing.T, keep KeepFunc) *Offer {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "AmazonEC2-eu-west-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	o, err := ReadOffer(f, keep)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestReadOfferStreamsAndFilters(t *testing.T) {
	keep, err := CoarseFilter("AmazonEC2")
	if err != nil {
		t.Fatal(err)
	}
	o := readFixture(t, keep)
	if o.OfferCode != "AmazonEC2" || o.Version != "20260101000000" || o.FormatVersion != "v1.0" {
		t.Fatalf("metadata: %+v", o)
	}
	// Dedicated tenancy, Local Zone and data transfer are dropped by the
	// coarse filter; Reserved terms are skipped.
	if got := strings.Join(o.SKUs(), ","); got != "SKU1,SKU3,SKU4,SKU5" {
		t.Fatalf("kept %s", got)
	}
	if len(o.OnDemand) != 4 || o.OnDemand["SKU2"] != nil {
		t.Fatalf("on-demand terms for %d skus", len(o.OnDemand))
	}
}

func TestReadOfferRejectsMalformed(t *testing.T) {
	all := func(Product) bool { return true }
	cases := map[string]string{
		"not object":     `[]`,
		"terms first":    `{"offerCode":"X","terms":{"OnDemand":{}},"products":{}}`,
		"no offer code":  `{"products":{},"terms":{}}`,
		"bad product":    `{"offerCode":"X","products":{"A":"nope"}}`,
		"bad terms":      `{"offerCode":"X","products":{"A":{"sku":"A","attributes":{}}},"terms":{"OnDemand":{"A":[1]}}}`,
		"truncated":      `{"offerCode":"X","products":{`,
		"bad key token":  `{"offerCode":"X","products":{"A":{"sku":"A"},]}}`,
		"bad skip array": `{"offerCode":"X","attributesList":[1,}`,
	}
	for name, in := range cases {
		if _, err := ReadOffer(strings.NewReader(in), all); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Unknown top-level keys of any JSON type are skipped.
	o, err := ReadOffer(strings.NewReader(`{"offerCode":"X","n":1,"arr":[{"a":[1,2]}],"obj":{"k":{"x":1}},"products":{},"terms":{}}`), all)
	if err != nil || o.OfferCode != "X" {
		t.Fatalf("skip failed: %v", err)
	}
}

func TestBuildAppliesRulesAndNormalizesRegionPrefix(t *testing.T) {
	keep, _ := CoarseFilter("AmazonEC2")
	o := readFixture(t, keep)
	snap, err := Build([]RegionOffer{{Region: "eu-west-1", Offer: o}}, "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, p := range snap.Prices {
		ids[p.Identity()] = p.UnitPrice().String()
	}
	want := map[string]string{
		"AmazonEC2/eu-west-1/Compute Instance{instanceType=t3.micro,operatingSystem=Linux}": "0.0114",
		"AmazonEC2/eu-west-1/Storage{volumeApiName=gp3}":                                    "0.088",
		// "EU-NatGateway-Hours" is stored region-independently; the identical
		// duplicate SKU5 collapses into SKU4.
		"AmazonEC2/eu-west-1/NAT Gateway{usagetype=NatGateway-Hours}": "0.048",
	}
	if len(ids) != len(want) {
		t.Fatalf("prices: %v", ids)
	}
	for id, price := range want {
		if ids[id] != price {
			t.Errorf("%s = %q, want %s", id, ids[id], price)
		}
	}
	if len(snap.Offers) != 1 || snap.Offers[0].Products != 3 || snap.GeneratedAt != "2026-01-02" {
		t.Fatalf("offers: %+v", snap.Offers)
	}
}

func TestBuildDetectsConflictsAndBadInput(t *testing.T) {
	keep, _ := CoarseFilter("AmazonEC2")
	o := readFixture(t, keep)
	// Make the duplicate NAT price disagree: the build must refuse to guess.
	for k, term := range o.OnDemand["SKU5"] {
		for dk, d := range term.PriceDimensions {
			d.PricePerUnit = map[string]string{"USD": "0.05"}
			term.PriceDimensions[dk] = d
		}
		o.OnDemand["SKU5"][k] = term
	}
	if _, err := Build([]RegionOffer{{Region: "eu-west-1", Offer: o}}, "d"); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflict not detected: %v", err)
	}
	if _, err := Build([]RegionOffer{{Region: "mars-1", Offer: o}}, "d"); err == nil {
		t.Fatal("unknown region accepted")
	}
	if _, err := Build([]RegionOffer{{Region: "eu-west-1", Offer: &Offer{OfferCode: "AmazonFoo"}}}, "d"); err == nil {
		t.Fatal("unknown service accepted")
	}
	o2 := readFixture(t, keep)
	delete(o2.OnDemand, "SKU3")
	if _, err := Build([]RegionOffer{{Region: "eu-west-1", Offer: o2}}, "d"); err == nil {
		t.Fatal("product without on-demand term accepted")
	}
}

func TestConvertDimensionsTiers(t *testing.T) {
	unit, tiers, err := convertDimensions(map[string]PriceDimension{
		"b": {BeginRange: "6000000000", EndRange: "15000000000", Unit: "GB-s", PricePerUnit: map[string]string{"USD": "0.000015"}},
		"a": {BeginRange: "0", EndRange: "6000000000", Unit: "GB-s", PricePerUnit: map[string]string{"USD": "0.0000166667"}},
		"c": {BeginRange: "15000000000", EndRange: "Inf", Unit: "GB-s", PricePerUnit: map[string]string{"USD": "0.0000133334"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unit != "GB-s" || len(tiers) != 3 || tiers[2].End != nil || tiers[0].End.String() != "6000000000" {
		t.Fatalf("tiers %+v", tiers)
	}
	bad := []map[string]PriceDimension{
		{},
		{"a": {BeginRange: "x", EndRange: "Inf", Unit: "u", PricePerUnit: map[string]string{"USD": "1"}}},
		{"a": {BeginRange: "0", EndRange: "y", Unit: "u", PricePerUnit: map[string]string{"USD": "1"}}},
		{"a": {BeginRange: "0", EndRange: "Inf", Unit: "u", PricePerUnit: map[string]string{"USD": "?"}}},
		{"a": {BeginRange: "0", EndRange: "5", Unit: "u", PricePerUnit: map[string]string{"USD": "1"}},
			"b": {BeginRange: "5", EndRange: "Inf", Unit: "v", PricePerUnit: map[string]string{"USD": "1"}}},
	}
	for i, dims := range bad {
		if _, _, err := convertDimensions(dims); err == nil {
			t.Errorf("bad dims %d accepted", i)
		}
	}
}

func TestWriteSnapshotRoundTripsAndIsStable(t *testing.T) {
	keep, _ := CoarseFilter("AmazonEC2")
	snap, err := Build([]RegionOffer{{Region: "eu-west-1", Offer: readFixture(t, keep)}}, "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}
	var a, b bytes.Buffer
	if err := WriteSnapshot(&a, snap); err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(&b, snap); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatal("output not deterministic")
	}
	back, err := pricing.Load(bytes.NewReader(a.Bytes()))
	if err != nil {
		t.Fatalf("written snapshot does not load: %v\n%s", err, a.String())
	}
	if len(back.Prices) != len(snap.Prices) {
		t.Fatal("price count changed in round trip")
	}
	// One price per line keeps refresh diffs reviewable.
	if strings.Count(a.String(), `{"service":`) != strings.Count(a.String(), "\n    {\"service\":") {
		t.Fatal("prices are not one per line")
	}
}

func TestSlimOfferRoundTripAndBuildFromDir(t *testing.T) {
	dir := t.TempDir()
	keep, _ := CoarseFilter("AmazonEC2")
	o := readFixture(t, keep)
	var buf bytes.Buffer
	if err := WriteOffer(&buf, o); err != nil {
		t.Fatal(err)
	}
	back, err := ReadSlim(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Products) != len(o.Products) || len(back.OnDemand) != len(o.OnDemand) {
		t.Fatal("slim round trip lost data")
	}
	if _, err := BuildFromDir(dir, []string{"eu-west-1"}, "d"); err == nil {
		t.Fatal("missing slim files accepted")
	}
	// Minimal slim files for every service let BuildFromDir run end to end.
	for _, svc := range Services {
		content := buf.Bytes()
		if svc != "AmazonEC2" {
			content = []byte(`{"formatVersion":"v1.0","offerCode":"` + svc + `","version":"1","publicationDate":"p","products":{},"terms":{"OnDemand":{}}}`)
		}
		if err := os.WriteFile(filepath.Join(dir, SlimFileName(svc, "eu-west-1")), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := BuildFromDir(dir, []string{"eu-west-1"}, "d")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Offers) != len(Services) || len(snap.Prices) != 3 {
		t.Fatalf("offers %d prices %d", len(snap.Offers), len(snap.Prices))
	}
	out := filepath.Join(dir, "snap.json")
	var w bytes.Buffer
	if err := WriteSnapshot(&w, snap); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, w.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := WriteChecksum(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pricing.LoadFile(out); err != nil || len(sum) != 64 {
		t.Fatalf("checksummed snapshot did not load: %v", err)
	}
}

func TestFetchStreamsOverHTTP(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "AmazonEC2-eu-west-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	o, err := Fetch(context.Background(), srv.Client(), "AmazonEC2", srv.URL+"/offer")
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Products) != 4 {
		t.Fatalf("kept %d products", len(o.Products))
	}
	if _, err := Fetch(context.Background(), srv.Client(), "AmazonEC2", srv.URL+"/missing"); err == nil {
		t.Fatal("HTTP 404 accepted")
	}
	if _, err := Fetch(context.Background(), srv.Client(), "AmazonFoo", srv.URL); err == nil {
		t.Fatal("unknown service accepted")
	}
	if !strings.Contains(OfferURL("AmazonS3", "eu-west-1"), "/AmazonS3/current/eu-west-1/index.json") {
		t.Fatal("OfferURL")
	}
	if NewHTTPClient().Timeout == 0 {
		t.Fatal("client has no timeout")
	}
}

func TestCoarseFiltersAndNormalization(t *testing.T) {
	for _, svc := range Services {
		if _, err := CoarseFilter(svc); err != nil {
			t.Errorf("%s: %v", svc, err)
		}
	}
	rds, _ := CoarseFilter("AmazonRDS")
	region := map[string]string{"locationType": "AWS Region"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range region {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	if !rds(Product{ProductFamily: "Database Instance", Attributes: with(map[string]string{"licenseModel": "No license required", "databaseEngine": "PostgreSQL"})}) {
		t.Error("RDS postgres dropped")
	}
	if rds(Product{ProductFamily: "Database Instance", Attributes: with(map[string]string{"licenseModel": "License included", "databaseEngine": "Oracle"})}) {
		t.Error("RDS oracle kept")
	}
	if !rds(Product{ProductFamily: "Database Storage", Attributes: region}) {
		t.Error("RDS storage dropped")
	}
	if NormalizeUsageType("eu-west-1", "EU-NatGateway-Hours") != "NatGateway-Hours" ||
		NormalizeUsageType("us-east-1", "NatGateway-Hours") != "NatGateway-Hours" ||
		NormalizeUsageType("us-east-1", "USE1-RegionalNatGateway-Hours") != "RegionalNatGateway-Hours" {
		t.Error("NormalizeUsageType")
	}
	if !SupportedRegion("us-west-2") || SupportedRegion("mars-1") {
		t.Error("SupportedRegion")
	}
}

func TestRulesSelectExpectedProducts(t *testing.T) {
	cases := []struct {
		svc, family, usage string
		attrs              map[string]string
		want               bool
	}{
		{"AWSLambda", "Serverless", "Global-Request", map[string]string{"group": "AWS-Lambda-Requests"}, false},
		{"AWSLambda", "Serverless", "Request", map[string]string{"group": "AWS-Lambda-Requests"}, true},
		{"AWSLambda", "Serverless", "Lambda-Edge-Request", map[string]string{"group": "AWS-Lambda-Edge-Requests"}, false},
		{"AmazonRDS", "Database Instance", "InstanceUsage:db.t3.micro", map[string]string{"deploymentOption": "Single-AZ"}, true},
		{"AmazonRDS", "Database Instance", "UnusedBox:db.t3.micro", map[string]string{"deploymentOption": "Single-AZ"}, false},
		{"AmazonRDS", "Database Storage", "RDS:GP3-Storage", map[string]string{"databaseEngine": "Any", "deploymentOption": "Single-AZ"}, true},
		{"AmazonRDS", "Database Storage", "RDS:GP3-Storage", map[string]string{"databaseEngine": "MySQL", "deploymentOption": "Single-AZ"}, false},
		{"AmazonRDS", "Storage Snapshot", "RDS:ChargedBackupUsage", map[string]string{"databaseEngine": "Any", "deploymentOption": "Single-AZ"}, true},
		{"AmazonS3", "Storage", "TimedStorage-XZ-ByteHrs", nil, false},
		{"AmazonS3", "API Request", "Requests-GIR-Tier2", nil, true},
		{"AWSELB", "Load Balancer-Application", "TS-LoadBalancerUsage", nil, false},
		{"AmazonEC2", "CPU Credits", "CPUCredits:t3", map[string]string{"operatingSystem": "Linux"}, true},
		{"AmazonEC2", "Provisioned Throughput", "EBS:VolumeP-Throughput.gp3", map[string]string{"volumeApiName": "gp3"}, true},
	}
	for _, c := range cases {
		got := false
		for _, r := range buildRules[c.svc] {
			if r.family == c.family && r.match(c.attrs, c.usage) {
				got = true
				break
			}
		}
		if got != c.want {
			t.Errorf("%s %s %s: selected=%v, want %v", c.svc, c.family, c.usage, got, c.want)
		}
	}
}
