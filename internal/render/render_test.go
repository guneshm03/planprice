package render

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guneshm03/planprice/internal/estimate"
	"github.com/guneshm03/planprice/internal/mapper"
	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/pricedata"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

var update = flag.Bool("update", false, "rewrite golden files")

func run(t *testing.T, planName, usageName string, view tfplan.View) *estimate.Estimate {
	t.Helper()
	snap, err := pricedata.Default()
	if err != nil {
		t.Fatal(err)
	}
	var u *usage.File
	if usageName != "" {
		u, err = usage.Load(filepath.Join("..", "..", "examples", usageName))
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := tfplan.ParseFile(filepath.Join("..", "..", "testdata", "plans", planName))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := p.Resources(view, "us-east-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	est, err := estimate.Run("testdata/plans/"+planName, rs, estimate.Options{Snapshot: snap, Registry: mapper.NewRegistry(), Usage: u})
	if err != nil {
		t.Fatal(err)
	}
	return est
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/render -update` to create)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden file; run `go test ./internal/render -update` and review the diff.\n--- got ---\n%s", name, got)
	}
}

func TestGoldenOutputs(t *testing.T) {
	head := run(t, "head.json", "usage.yml", tfplan.After)
	base := run(t, "base.json", "usage.yml", tfplan.After)
	headNoUsage := run(t, "head.json", "", tfplan.After)
	multi := run(t, "multi-region.json", "usage-multi-region.yml", tfplan.After)
	diff := estimate.Compare(base, head)

	cases := []struct {
		name  string
		write func(*bytes.Buffer) error
	}{
		{"breakdown-head.txt", func(b *bytes.Buffer) error { return Breakdown(b, head, Table) }},
		{"breakdown-head-no-usage.txt", func(b *bytes.Buffer) error { return Breakdown(b, headNoUsage, Table) }},
		{"breakdown-head.md", func(b *bytes.Buffer) error { return Breakdown(b, head, Markdown) }},
		{"breakdown-multi-region.json", func(b *bytes.Buffer) error { return Breakdown(b, multi, JSON) }},
		{"diff-base-head.txt", func(b *bytes.Buffer) error { return DiffReport(b, diff, Table) }},
		{"diff-base-head.md", func(b *bytes.Buffer) error { return DiffReport(b, diff, Markdown) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := c.write(&b); err != nil {
				t.Fatal(err)
			}
			golden(t, c.name, b.Bytes())
		})
	}
}

func TestJSONDiffIsMachineReadable(t *testing.T) {
	base := run(t, "base.json", "usage.yml", tfplan.After)
	head := run(t, "head.json", "usage.yml", tfplan.After)
	var b bytes.Buffer
	if err := DiffReport(&b, estimate.Compare(base, head), JSON); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		BaseMonthly json.Number `json:"base_monthly_cost"`
		HeadMonthly json.Number `json:"head_monthly_cost"`
		Delta       json.Number `json:"delta_monthly_cost"`
		Resources   []struct {
			Address string `json:"address"`
			Kind    string `json:"kind"`
		} `json:"resources"`
	}
	dec := json.NewDecoder(&b)
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	baseD, _ := money.Parse(decoded.BaseMonthly.String())
	headD, _ := money.Parse(decoded.HeadMonthly.String())
	delta, _ := money.Parse(decoded.Delta.String())
	if headD.Sub(baseD).Cmp(delta) != 0 || len(decoded.Resources) == 0 {
		t.Fatalf("decoded %+v", decoded)
	}
}

func TestMarkdownDiffDirections(t *testing.T) {
	base := run(t, "base.json", "", tfplan.After)
	head := run(t, "head.json", "", tfplan.After)
	cases := []struct {
		d    *estimate.Diff
		want string
	}{
		{estimate.Compare(base, head), "will increase by"},
		{estimate.Compare(head, base), "will decrease by"},
		{estimate.Compare(base, base), "no change in monthly cost"},
	}
	for _, c := range cases {
		var b bytes.Buffer
		if err := DiffReport(&b, c.d, Markdown); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.HasPrefix(out, CommentMarker+"\n") || !strings.Contains(out, c.want) {
			t.Errorf("want %q in:\n%s", c.want, out)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for _, f := range []string{"table", "markdown", "json"} {
		if _, err := ParseFormat(f); err != nil {
			t.Error(err)
		}
	}
	if _, err := ParseFormat("html"); err == nil {
		t.Error("html accepted")
	}
}

func TestCellsForEveryStatus(t *testing.T) {
	rate := money.MustParse("0.5")
	cost := money.FromInt(2)
	cases := []struct {
		c    estimate.ComponentCost
		cost string
	}{
		{estimate.ComponentCost{Status: mapper.StatusPriced, Quantity: money.FromInt(4), UnitPrice: &rate, Monthly: &cost, Tiered: true}, "$2.00"},
		{estimate.ComponentCost{Status: mapper.StatusUsageMissing, UnitPrice: &rate}, "usage needed"},
		{estimate.ComponentCost{Status: mapper.StatusUnknown}, "unknown"},
		{estimate.ComponentCost{Status: mapper.StatusPriceNotFound, Quantity: money.FromInt(1)}, "no price"},
		{estimate.ComponentCost{Status: mapper.StatusUnsupported}, "not priced"},
	}
	for _, c := range cases {
		_, r, got := componentCells(c.c)
		if got != c.cost {
			t.Errorf("%s: %s", c.c.Status, got)
		}
		if c.c.Tiered && r != "$0.50*" {
			t.Errorf("tiered marker missing: %s", r)
		}
	}
	if mdEscape("a|b\nc") != `a\|b c` || code("x`y") != "`x'y`" {
		t.Error("markdown escaping")
	}
	if percent(nil) != "n/a" {
		t.Error("percent(nil)")
	}
}
