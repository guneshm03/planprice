package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `
version: 1
defaults:
  aws_lambda_function:
    request_duration_ms: 300
    monthly_requests: 10
resource_usage:
  aws_lambda_function.workers[*]:
    monthly_requests: 500000
  aws_lambda_function.workers["ocr"]:
    request_duration_ms: 2400
  module.web.aws_nat_gateway.egress[*]:
    monthly_data_processed_gb: 600.5
  module.*.aws_x.y[*]:
    a: 1
  module.data.aws_s3_bucket.assets:
    storage_classes:
      standard:
        storage_gb: 2500
`

func parse(t *testing.T, s string) *File {
	t.Helper()
	f, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPrecedenceMerge(t *testing.T) {
	f := parse(t, sample)
	ocr := f.For(`aws_lambda_function.workers["ocr"]`, "aws_lambda_function")
	if n, _ := ocr.Number("monthly_requests"); n.String() != "500000" {
		t.Errorf("pattern should override defaults: %s", n)
	}
	if n, _ := ocr.Number("request_duration_ms"); n.String() != "2400" {
		t.Errorf("exact should override defaults: %s", n)
	}
	if got := strings.Join(ocr.Sources(), " < "); got != `defaults.aws_lambda_function < aws_lambda_function.workers[*] < aws_lambda_function.workers["ocr"]` {
		t.Errorf("sources: %s", got)
	}
	resize := f.For(`aws_lambda_function.workers["resize"]`, "aws_lambda_function")
	if n, _ := resize.Number("request_duration_ms"); n.String() != "300" {
		t.Errorf("default not applied: %s", n)
	}
	other := f.For("aws_lambda_function.api", "aws_lambda_function")
	if n, _ := other.Number("monthly_requests"); n.String() != "10" {
		t.Errorf("defaults only: %s", n)
	}
	nat := f.For("module.web.aws_nat_gateway.egress[1]", "aws_nat_gateway")
	if n, ok := nat.Number("monthly_data_processed_gb"); !ok || n.String() != "600.5" {
		t.Errorf("float usage: %s %v", n, ok)
	}
	if !f.For("aws_instance.x", "aws_instance").Empty() {
		t.Error("unrelated resource got usage")
	}
	var nilFile *File
	if !nilFile.For("a.b", "a").Empty() || nilFile.Entries() != nil {
		t.Error("nil file should yield empty usage")
	}
	s3 := f.For("module.data.aws_s3_bucket.assets", "aws_s3_bucket")
	classes, ok := s3.Group("storage_classes")
	if !ok || strings.Join(s3.GroupKeys("storage_classes"), ",") != "standard" {
		t.Fatal("group access")
	}
	std, ok := classes.Group("standard")
	if n, _ := std.Number("storage_gb"); !ok || n.String() != "2500" {
		t.Error("nested number")
	}
	if _, ok := s3.Group("missing"); ok {
		t.Error("missing group reported present")
	}
	if len(f.Entries()) != 5 {
		t.Errorf("entries: %v", f.Entries())
	}
}

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern, addr string
		want          bool
	}{
		{"aws_instance.web[*]", "aws_instance.web[0]", true},
		{"aws_instance.web[*]", `aws_instance.web["a]b"]`, true},
		{"aws_instance.web[*]", "aws_instance.web", false},
		{"aws_instance.web[*]", "aws_instance.web[0].x", false},
		{"module.m[*].aws_instance.web[*]", `module.m["eu"].aws_instance.web[3]`, true},
		{"module.m[*].aws_instance.web[*]", `module.n["eu"].aws_instance.web[3]`, false},
		{"aws_instance.web", "aws_instance.web", true},
		{"aws_instance.web", "aws_instance.web[0]", false},
		{"aws_instance.web[*]", "aws_instance.web[unterminated", false},
	}
	for _, c := range cases {
		if got := MatchPattern(c.pattern, c.addr); got != c.want {
			t.Errorf("MatchPattern(%s, %s) = %v", c.pattern, c.addr, got)
		}
	}
}

func TestResourceTypeOf(t *testing.T) {
	cases := map[string]string{
		"aws_instance.web":                          "aws_instance",
		`module.aws_thing.aws_s3_bucket.logs["x"]`:  "aws_s3_bucket",
		"module.web[*].aws_nat_gateway.egress[*]":   "aws_nat_gateway",
		"google_compute_instance.vm":                "",
		"aws_instance":                              "",
		"module.aws_instance.google_thing.resource": "",
	}
	for in, want := range cases {
		if got := ResourceTypeOf(in); got != want {
			t.Errorf("ResourceTypeOf(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"version":         "version: 2\n",
		"unknown top key": "version: 1\nresources: {}\n",
		"bad default":     "version: 1\ndefaults:\n  lambda: {a: 1}\n",
		"bad address":     "version: 1\nresource_usage:\n  not-an-address: {a: 1}\n",
		"not yaml":        "version: [\n",
	}
	for name, in := range cases {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	f, err := Parse(strings.NewReader(""))
	if err != nil || f.Version != SupportedVersion {
		t.Errorf("empty file: %v", err)
	}
	f = parse(t, "version: 1\nresource_usage:\n  aws_instance.x:\n")
	if !f.For("aws_instance.x", "aws_instance").Empty() {
		t.Error("null entry should be empty")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "u.yml")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("version: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the file: %v", err)
	}
	if _, err := Load(filepath.Join(dir, "missing.yml")); err == nil {
		t.Error("missing file accepted")
	}
}

var testSchema = map[string][]Field{
	"aws_lambda_function": {
		{Key: "monthly_requests", Kind: Number, Description: "requests"},
		{Key: "request_duration_ms", Kind: Number, Description: "ms"},
	},
	"aws_nat_gateway": {{Key: "monthly_data_processed_gb", Kind: Number, Description: "gb"}},
	"aws_instance":    {{Key: "operating_system", Kind: Enum, Enum: []string{"linux", "windows"}, Description: "os"}},
	"aws_s3_bucket": {{Key: "storage_classes", Kind: GroupMap, Enum: []string{"standard", "glacier"}, Description: "classes",
		Fields: []Field{{Key: "storage_gb", Kind: Number, Description: "gb", Example: "100"}}}},
	"aws_ebs_volume": nil,
}

func schemaFunc(t string) ([]Field, bool) {
	f, ok := testSchema[t]
	return f, ok
}

func TestValidate(t *testing.T) {
	f := parse(t, sample)
	warnings, err := f.Validate(schemaFunc)
	if err != nil {
		t.Fatalf("valid file rejected: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "aws_x") {
		t.Fatalf("warnings: %v", warnings)
	}
	bad := parse(t, `
version: 1
resource_usage:
  aws_lambda_function.a:
    monthly_requets: 5
    request_duration_ms: -1
  aws_nat_gateway.n:
    monthly_data_processed_gb: lots
  aws_instance.i:
    operating_system: plan9
  aws_s3_bucket.b:
    storage_classes:
      standard: {storage_gb: 1, bogus: 2}
      tape: {storage_gb: 1}
      glacier: 5
  aws_s3_bucket.c:
    storage_classes: [1]
`)
	_, err = bad.Validate(schemaFunc)
	if err == nil {
		t.Fatal("invalid file accepted")
	}
	for _, want := range []string{
		`unknown key "monthly_requets"`, "must not be negative", "want a number", "want one of linux, windows",
		`unknown key "bogus"`, `unknown group "tape"`, "glacier: want a map", "storage_classes: want a map of",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	var nilFile *File
	if w, err := nilFile.Validate(schemaFunc); w != nil || err != nil {
		t.Error("nil file validate")
	}
}

func TestTemplate(t *testing.T) {
	out := Template([]TemplateEntry{
		{Address: `aws_lambda_function.workers["a"]`, Type: "aws_lambda_function"},
		{Address: `aws_lambda_function.workers["b"]`, Type: "aws_lambda_function"},
		{Address: "aws_s3_bucket.logs", Type: "aws_s3_bucket"},
		{Address: "aws_ebs_volume.v", Type: "aws_ebs_volume"},
		{Address: "aws_vpc.main", Type: "aws_vpc"},
		{Address: "aws_instance.i", Type: "aws_instance"},
	}, schemaFunc)
	for _, want := range []string{
		"aws_lambda_function.workers[*]:", "# monthly_requests: 0 # requests", "aws_s3_bucket.logs:",
		"#   standard:", "#     storage_gb: 100 # gb", "# operating_system: linux # os",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("template lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "aws_lambda_function.workers[*]:") != 1 || strings.Contains(out, "aws_vpc") || strings.Contains(out, "aws_ebs_volume") {
		t.Errorf("template should collapse instances and skip types without usage:\n%s", out)
	}
	// The template must itself be a valid (no-op) usage file.
	f, err := Parse(strings.NewReader(out))
	if err != nil {
		t.Fatalf("template does not parse: %v", err)
	}
	if _, err := f.Validate(schemaFunc); err != nil {
		t.Fatalf("template does not validate: %v", err)
	}
	empty := Template(nil, schemaFunc)
	if _, err := Parse(strings.NewReader(empty)); err != nil {
		t.Fatalf("empty template does not parse: %v\n%s", err, empty)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(sample)
	f.Add("version: 1\n")
	f.Add("version: 1\nresource_usage:\n  aws_a.b[*]: {x: 1}\n")
	f.Fuzz(func(_ *testing.T, s string) {
		file, err := Parse(strings.NewReader(s))
		if err != nil {
			return
		}
		_, _ = file.Validate(schemaFunc)
		for _, e := range file.Entries() {
			_ = file.For(e, ResourceTypeOf(e))
		}
	})
}
