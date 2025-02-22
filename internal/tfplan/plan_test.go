package tfplan

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guneshm03/planprice/internal/money"
)

func repoPlan(t *testing.T, name string) *Plan {
	t.Helper()
	p, err := ParseFile(filepath.Join("..", "..", "testdata", "plans", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func byAddress(rs []Resource) map[string]Resource {
	m := map[string]Resource{}
	for _, r := range rs {
		m[r.Address] = r
	}
	return m
}

func TestParseRealTerraformPlans(t *testing.T) {
	for _, name := range []string{"base.json", "head.json", "multi-region.json"} {
		p := repoPlan(t, name)
		if p.FormatVersion == "" || len(p.ResourceChanges) == 0 {
			t.Fatalf("%s: empty plan", name)
		}
	}
	head := repoPlan(t, "head.json")
	rs, err := head.Resources(After, "us-east-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := byAddress(rs)
	web := m["module.web.aws_instance.web[2]"]
	if web.Type != "aws_instance" || web.ModuleAddress != "module.web" || web.Action != ActionCreate {
		t.Fatalf("unexpected web[2]: %+v", web)
	}
	if it, _ := web.Attrs.String("instance_type"); it != "m6i.large" {
		t.Fatalf("instance_type = %q", it)
	}
	if web.Region != "us-east-1" || web.RegionSource != RegionFromAttribute {
		t.Fatalf("region %s via %s", web.Region, web.RegionSource)
	}
	roots := web.Attrs.Blocks("root_block_device")
	if len(roots) != 1 || !roots[0].Unknown("iops") {
		t.Fatalf("root block device / unknown mask not decoded: %+v", roots)
	}
	if size, _ := roots[0].Number("volume_size"); size.Cmp(money.FromInt(50)) != 0 {
		t.Fatalf("volume_size = %s", size)
	}
	if _, ok := m[`aws_lambda_function.workers["ocr"]`]; !ok {
		t.Fatal("for_each instance missing")
	}
	// Created-only plan: the before view is empty.
	before, err := head.Resources(Before, "us-east-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("before view has %d resources", len(before))
	}
}

func TestMultiRegionPlanUsesPerResourceRegion(t *testing.T) {
	rs, err := repoPlan(t, "multi-region.json").Resources(After, "us-east-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := byAddress(rs)
	if got := m["aws_instance.standby"].Region; got != "eu-west-1" {
		t.Fatalf("standby region %s", got)
	}
	if got := m["aws_instance.primary"].Region; got != "us-east-1" {
		t.Fatalf("primary region %s", got)
	}
}

func TestRegionResolutionWithoutResourceAttribute(t *testing.T) {
	p, err := ParseFile(filepath.Join("testdata", "synthetic-v5.json"))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	rs, err := p.Resources(After, "ap-south-1", log)
	if err != nil {
		t.Fatal(err)
	}
	m := byAddress(rs)
	cases := []struct {
		addr   string
		region string
		source RegionSource
	}{
		{"aws_instance.app", "eu-west-1", RegionFromVariable},
		{"aws_db_instance.db", "eu-west-1", RegionFromVariable},
		{`module.edge["eu"].aws_nat_gateway.this[0]`, "us-east-1", RegionFromProvider},
		{`module.edge["eu"].aws_s3_bucket.inner`, "us-east-1", RegionFromProvider},
		{"aws_lb.unknown_region", "ap-south-1", RegionFromDefault},
	}
	for _, c := range cases {
		r, ok := m[c.addr]
		if !ok {
			t.Fatalf("%s missing from after view", c.addr)
		}
		if r.Region != c.region || r.RegionSource != c.source {
			t.Errorf("%s: region %s via %q, want %s via %q", c.addr, r.Region, r.RegionSource, c.region, c.source)
		}
	}
	if !strings.Contains(logs.String(), "aws_lb.unknown_region") {
		t.Errorf("default-region fallback was not logged: %s", logs.String())
	}
	if _, ok := m["data.aws_ami.ubuntu"]; ok {
		t.Error("data source included")
	}
	if _, ok := m["aws_ebs_volume.old"]; ok {
		t.Error("deleted resource in after view")
	}
	if m["google_compute_instance.vm"].IsAWS() {
		t.Error("google resource reported as AWS")
	}
}

func TestBeforeViewAndActions(t *testing.T) {
	p, err := ParseFile(filepath.Join("testdata", "synthetic-v5.json"))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := p.Resources(Before, "us-east-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := byAddress(rs)
	old, ok := m["aws_ebs_volume.old"]
	if !ok || old.Action != ActionDelete || old.Region != "us-west-2" {
		t.Fatalf("deleted volume in before view: %+v", old)
	}
	if m["aws_db_instance.db"].Action != ActionReplace {
		t.Fatalf("replace not detected: %s", m["aws_db_instance.db"].Action)
	}
	if it, _ := m["aws_instance.app"].Attrs.String("instance_type"); it != "t3.small" {
		t.Fatalf("before instance_type = %s", it)
	}
	if _, ok := m[`module.edge["eu"].aws_s3_bucket.inner`]; ok {
		t.Error("created resource in before view")
	}
	if Before.String() != "before" || After.String() != "after" {
		t.Error("view names")
	}
}

func TestParseAction(t *testing.T) {
	cases := map[string]Action{
		"no-op": ActionNoOp, "create": ActionCreate, "read": ActionRead, "update": ActionUpdate,
		"delete": ActionDelete, "delete,create": ActionReplace, "create,delete": ActionReplace, "forget": ActionDelete,
	}
	for in, want := range cases {
		got, err := ParseAction(strings.Split(in, ","))
		if err != nil || got != want {
			t.Errorf("ParseAction(%s) = %s, %v", in, got, err)
		}
	}
	if _, err := ParseAction([]string{"explode"}); err == nil {
		t.Error("unknown action accepted")
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"not json":        "{",
		"state not plan":  `{"values": {}}`,
		"future format":   `{"format_version": "2.0"}`,
		"missing type":    `{"format_version": "1.2", "resource_changes": [{"address": "x"}]}`,
		"bad action":      `{"format_version": "1.2", "resource_changes": [{"address": "a.b", "type": "a", "change": {"actions": ["boom"]}}]}`,
		"trailing object": `[]`,
	}
	for name, in := range cases {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParseFile("does-not-exist.json"); err == nil {
		t.Error("missing file accepted")
	}
}

func TestAttrsAccessors(t *testing.T) {
	p, err := Parse(strings.NewReader(`{"format_version":"1.2","resource_changes":[{"address":"aws_x.y","type":"aws_x","mode":"managed",
		"change":{"actions":["create"],"before":null,
		"after":{"s":"v","n":12.5,"z":0,"b":true,"list":["a",1,"b"],"str_num":"7","blocks":[{"k":"v"}]},
		"after_unknown":{"u":true,"blocks":[{"k2":true}]}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := p.Resources(After, "us-east-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := rs[0].Attrs
	if s, ok := a.String("s"); !ok || s != "v" {
		t.Error("String")
	}
	if a.StringOr("missing", "def") != "def" || a.StringOr("s", "def") != "v" {
		t.Error("StringOr")
	}
	if n, ok := a.Number("n"); !ok || n.String() != "12.5" {
		t.Error("Number")
	}
	if a.NumberOr("z", money.FromInt(3)).String() != "3" {
		t.Error("NumberOr should treat 0 as unset")
	}
	if n, ok := a.Number("str_num"); !ok || n.String() != "7" {
		t.Error("numeric string")
	}
	if _, ok := a.Number("s"); ok {
		t.Error("non-numeric string parsed as number")
	}
	if b, ok := a.Bool("b"); !ok || !b {
		t.Error("Bool")
	}
	if got := a.Strings("list"); len(got) != 2 {
		t.Errorf("Strings = %v", got)
	}
	if !a.Unknown("u") || a.Unknown("s") || !a.Has("s") || a.Has("missing") {
		t.Error("Unknown/Has")
	}
	blocks := a.Blocks("blocks")
	if len(blocks) != 1 || !blocks[0].Unknown("k2") {
		t.Error("Blocks unknown mask")
	}
	if a.Raw()["s"] != "v" {
		t.Error("Raw")
	}
	if Describe(nil) != "null" || Describe("x") != `"x"` || Describe(3) != "3" {
		t.Error("Describe")
	}
}

func FuzzParse(f *testing.F) {
	f.Add(`{"format_version":"1.2","resource_changes":[]}`)
	f.Add(`{"format_version":"1.2","resource_changes":[{"address":"aws_x.y","type":"aws_x","change":{"actions":["create"],"after":{"a":1}}}]}`)
	f.Add(`{`)
	f.Fuzz(func(t *testing.T, s string) {
		p, err := Parse(strings.NewReader(s))
		if err != nil {
			return
		}
		// A successfully parsed plan must always yield views without error.
		if _, err := p.Resources(After, "us-east-1", nil); err != nil {
			t.Fatalf("after view: %v", err)
		}
		if _, err := p.Resources(Before, "us-east-1", nil); err != nil {
			t.Fatalf("before view: %v", err)
		}
	})
}
