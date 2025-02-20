package pricing

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"

	"github.com/guneshm03/planprice/internal/money"
)

const fixture = `{
  "schema": 1, "currency": "USD", "source": "test", "generated_at": "2026-01-01",
  "offers": [{"service":"AmazonEC2","region":"us-east-1","version":"v1","publication_date":"d","url":"u","products":3},
             {"service":"AmazonS3","region":"eu-west-1","version":"v1","publication_date":"d","url":"u","products":1}],
  "prices": [
    {"service":"AmazonEC2","region":"us-east-1","family":"Compute Instance","attrs":{"instanceType":"t3.micro","operatingSystem":"Linux"},"unit":"Hrs","tiers":[{"start":0,"usd":"0.0104"}],"sku":"A"},
    {"service":"AmazonEC2","region":"us-east-1","family":"Compute Instance","attrs":{"instanceType":"t3.micro","operatingSystem":"Windows"},"unit":"Hrs","tiers":[{"start":0,"usd":"0.0196"}],"sku":"B"},
    {"service":"AmazonEC2","region":"us-east-1","family":"Storage","attrs":{"volumeApiName":"gp3"},"unit":"GB-Mo","tiers":[{"start":0,"usd":"0.08"}],"sku":"C"},
    {"service":"AmazonS3","region":"eu-west-1","family":"Storage","attrs":{"usagetype":"TimedStorage-ByteHrs"},"unit":"GB-Mo",
     "tiers":[{"start":0,"end":51200,"usd":"0.023"},{"start":51200,"end":512000,"usd":"0.022"},{"start":512000,"usd":"0.021"}],"sku":"D"}
  ]
}`

func load(t *testing.T, s string) *Snapshot {
	t.Helper()
	snap, err := Load(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestLookup(t *testing.T) {
	snap := load(t, fixture)
	p, err := snap.Lookup(Query{Service: "AmazonEC2", Region: "us-east-1", Family: "Compute Instance",
		Attrs: map[string]string{"instanceType": "t3.micro", "operatingSystem": "Linux"}})
	if err != nil || p.SKU != "A" {
		t.Fatalf("got %+v, %v", p, err)
	}
	// Same key set again hits the cached index.
	p, err = snap.Lookup(Query{Service: "AmazonEC2", Region: "us-east-1", Family: "Compute Instance",
		Attrs: map[string]string{"instanceType": "t3.micro", "operatingSystem": "Windows"}})
	if err != nil || p.SKU != "B" {
		t.Fatalf("got %+v, %v", p, err)
	}
	_, err = snap.Lookup(Query{Service: "AmazonEC2", Region: "us-east-1", Family: "Compute Instance",
		Attrs: map[string]string{"instanceType": "t3.micro"}})
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("want ErrAmbiguous, got %v", err)
	}
	_, err = snap.Lookup(Query{Service: "AmazonEC2", Region: "eu-west-1", Family: "Storage",
		Attrs: map[string]string{"volumeApiName": "gp3"}})
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "eu-west-1") {
		t.Fatalf("want ErrNotFound naming the query, got %v", err)
	}
	if got := snap.Regions(); len(got) != 2 || got[0] != "eu-west-1" {
		t.Fatalf("regions %v", got)
	}
	if !snap.HasRegion("us-east-1") || snap.HasRegion("ap-south-1") {
		t.Fatal("HasRegion")
	}
	if got := snap.AttrValues("AmazonEC2", "us-east-1", "Compute Instance", "operatingSystem"); strings.Join(got, ",") != "Linux,Windows" {
		t.Fatalf("AttrValues %v", got)
	}
}

func TestTieredCost(t *testing.T) {
	snap := load(t, fixture)
	p, err := snap.Lookup(Query{Service: "AmazonS3", Region: "eu-west-1", Family: "Storage",
		Attrs: map[string]string{"usagetype": "TimedStorage-ByteHrs"}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ qty, want string }{
		{"0", "0"},
		{"100", "2.3"},          // 100 * 0.023
		{"51200", "1177.6"},     // exactly the first band
		{"60000", "1371.2"},     // 51200*0.023 + 8800*0.022 = 1177.6 + 193.6
		{"600000", "13163.2"},   // 1177.6 + 460800*0.022 (10137.6) + 88000*0.021 (1848)
		{"0.5", "0.0115"},       // fractional GB
		{"512000", "11315.2"},   // 1177.6 + 10137.6
		{"512001", "11315.221"}, // + 1 * 0.021
	}
	for _, c := range cases {
		if got := p.Cost(money.MustParse(c.qty)); got.Cmp(money.MustParse(c.want)) != 0 {
			t.Errorf("Cost(%s) = %s, want %s", c.qty, got, c.want)
		}
	}
	if !p.Tiered() {
		t.Error("Tiered() false")
	}
	// Blended rate for 60,000 GB is exactly 1371.2 / 60000.
	if got := p.EffectiveRate(money.FromInt(60000)); got.Mul(money.FromInt(60000)).Cmp(money.MustParse("1371.2")) != 0 {
		t.Errorf("EffectiveRate = %s", got)
	}
	if p.EffectiveRate(money.Zero).Cmp(money.MustParse("0.023")) != 0 {
		t.Error("EffectiveRate(0) should be the first-tier rate")
	}
}

// Property: tiered cost is monotonic and never exceeds qty * the highest rate.
func TestTieredCostProperties(t *testing.T) {
	snap := load(t, fixture)
	p := &snap.Prices[3]
	maxRate := money.MustParse("0.023")
	prop := func(a, b uint32) bool {
		x, y := money.FromInt(int64(a)), money.FromInt(int64(b))
		if x.Cmp(y) > 0 {
			x, y = y, x
		}
		cx, cy := p.Cost(x), p.Cost(y)
		return cx.Cmp(cy) <= 0 && cy.Cmp(y.Mul(maxRate)) <= 0 && cx.Sign() >= 0
	}
	if err := quick.Check(prop, nil); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsBadSnapshots(t *testing.T) {
	good := `{"service":"S","region":"r","family":"f","attrs":{"k":"v"},"unit":"u","tiers":[{"start":0,"usd":"1"}],"sku":"X"}`
	wrap := func(prices ...string) string {
		return `{"schema":1,"currency":"USD","source":"t","generated_at":"d","offers":[],"prices":[` + strings.Join(prices, ",") + `]}`
	}
	cases := map[string]string{
		"schema":          strings.Replace(wrap(good), `"schema":1`, `"schema":9`, 1),
		"currency":        strings.Replace(wrap(good), `"USD"`, `"EUR"`, 1),
		"duplicate":       wrap(good, strings.Replace(good, `"X"`, `"Y"`, 1)),
		"missing family":  wrap(strings.Replace(good, `"family":"f"`, `"family":""`, 1)),
		"no tiers":        wrap(strings.Replace(good, `[{"start":0,"usd":"1"}]`, `[]`, 1)),
		"nonzero start":   wrap(strings.Replace(good, `"start":0`, `"start":5`, 1)),
		"negative price":  wrap(strings.Replace(good, `"usd":"1"`, `"usd":"-1"`, 1)),
		"gap":             wrap(strings.Replace(good, `[{"start":0,"usd":"1"}]`, `[{"start":0,"end":10,"usd":"1"},{"start":11,"usd":"1"}]`, 1)),
		"open not last":   wrap(strings.Replace(good, `[{"start":0,"usd":"1"}]`, `[{"start":0,"usd":"1"},{"start":11,"usd":"1"}]`, 1)),
		"end before":      wrap(strings.Replace(good, `[{"start":0,"usd":"1"}]`, `[{"start":0,"end":0,"usd":"1"}]`, 1)),
		"unknown field":   strings.Replace(wrap(good), `"schema":1`, `"schema":1,"extra":true`, 1),
		"malformed price": wrap(strings.Replace(good, `"usd":"1"`, `"usd":"one"`, 1)),
	}
	for name, s := range cases {
		if _, err := Load(strings.NewReader(s)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Load(strings.NewReader(wrap(good))); err != nil {
		t.Fatalf("good snapshot rejected: %v", err)
	}
}

func TestChecksumAndLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err != nil {
		t.Fatalf("no checksum file should load: %v", err)
	}
	sum := Digest([]byte(fixture)) + "  s.json\n"
	if err := os.WriteFile(path+".sha256", []byte(sum), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err != nil {
		t.Fatalf("matching checksum rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(fixture, "0.0104", "0.0105", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered snapshot accepted: %v", err)
	}
	if err := VerifyChecksum([]byte("x"), []byte("\n")); err == nil {
		t.Fatal("empty checksum accepted")
	}
	if _, err := LoadFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestDiffSnapshots(t *testing.T) {
	oldSnap := load(t, fixture)
	changed := strings.Replace(fixture, `"0.0104"`, `"0.0110"`, 1)
	changed = strings.Replace(changed, `{"service":"AmazonEC2","region":"us-east-1","family":"Storage","attrs":{"volumeApiName":"gp3"},"unit":"GB-Mo","tiers":[{"start":0,"usd":"0.08"}],"sku":"C"},`,
		`{"service":"AmazonEC2","region":"us-east-1","family":"Storage","attrs":{"volumeApiName":"gp2"},"unit":"GB-Mo","tiers":[{"start":0,"usd":"0.10"}],"sku":"E"},`, 1)
	newSnap := load(t, changed)
	got := DiffSnapshots(oldSnap, newSnap)
	kinds := map[string]string{}
	for _, c := range got {
		kinds[c.Identity] = c.Kind()
	}
	want := map[string]string{
		"AmazonEC2/us-east-1/Compute Instance{instanceType=t3.micro,operatingSystem=Linux}": "changed",
		"AmazonEC2/us-east-1/Storage{volumeApiName=gp2}":                                    "added",
		"AmazonEC2/us-east-1/Storage{volumeApiName=gp3}":                                    "removed",
	}
	if len(kinds) != len(want) {
		t.Fatalf("diff = %v", kinds)
	}
	for id, k := range want {
		if kinds[id] != k {
			t.Errorf("%s: %s, want %s", id, kinds[id], k)
		}
	}
	if len(DiffSnapshots(oldSnap, oldSnap)) != 0 {
		t.Error("self diff not empty")
	}
}

func BenchmarkLookup(b *testing.B) {
	snap, err := Load(strings.NewReader(fixture))
	if err != nil {
		b.Fatal(err)
	}
	q := Query{Service: "AmazonEC2", Region: "us-east-1", Family: "Compute Instance",
		Attrs: map[string]string{"instanceType": "t3.micro", "operatingSystem": "Linux"}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := snap.Lookup(q); err != nil {
			b.Fatal(err)
		}
	}
}
