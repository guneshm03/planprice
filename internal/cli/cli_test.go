package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guneshm03/planprice/internal/mapper"
	"github.com/guneshm03/planprice/internal/usage"
)

var (
	basePlan  = filepath.Join("..", "..", "testdata", "plans", "base.json")
	headPlan  = filepath.Join("..", "..", "testdata", "plans", "head.json")
	multiPlan = filepath.Join("..", "..", "testdata", "plans", "multi-region.json")
	usageFile = filepath.Join("..", "..", "examples", "usage.yml")
	synthetic = filepath.Join("..", "tfplan", "testdata", "synthetic-v5.json")
)

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = Run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestTopLevel(t *testing.T) {
	if code, _, errOut := runCLI(t); code != ExitUsage || !strings.Contains(errOut, "Usage:") {
		t.Errorf("no args: %d %s", code, errOut)
	}
	if code, _, errOut := runCLI(t, "frobnicate"); code != ExitUsage || !strings.Contains(errOut, "unknown command") {
		t.Errorf("unknown: %d", code)
	}
	if code, out, _ := runCLI(t, "version"); code != ExitOK || out != "planprice dev\n" {
		t.Errorf("version: %d %q", code, out)
	}
	if code, out, _ := runCLI(t, "help"); code != ExitOK || !strings.Contains(out, "breakdown") {
		t.Errorf("help: %d", code)
	}
	if code, _, _ := runCLI(t, "breakdown", "-h"); code != ExitOK {
		t.Errorf("-h: %d", code)
	}
}

func TestBreakdown(t *testing.T) {
	code, out, errOut := runCLI(t, "breakdown", "--plan", headPlan, "--usage", usageFile)
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "OVERALL TOTAL") || !strings.Contains(out, "$3,704.82") {
		t.Errorf("unexpected output:\n%s", out)
	}
	code, out, _ = runCLI(t, "breakdown", "--plan", headPlan, "--format", "json")
	if code != ExitOK || !strings.HasPrefix(out, "{") {
		t.Errorf("json: %d", code)
	}
	dir := t.TempDir()
	outFile := filepath.Join(dir, "nested", "cost.md")
	if code, _, _ := runCLI(t, "breakdown", "--plan", basePlan, "--format", "markdown", "--out", outFile); code != ExitOK {
		t.Fatalf("--out exit %d", code)
	}
	if b, err := os.ReadFile(outFile); err != nil || !strings.Contains(string(b), "### planprice") {
		t.Errorf("--out file: %v", err)
	}
	// A created-only plan has nothing in its before view.
	if code, out, _ := runCLI(t, "breakdown", "--plan", headPlan, "--view", "before"); code != ExitOK || !strings.Contains(out, "OVERALL TOTAL") || !strings.Contains(out, "$0.00") {
		t.Errorf("before view: %d\n%s", code, out)
	}
}

func TestBreakdownErrors(t *testing.T) {
	cases := []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"breakdown"}, ExitUsage, "--plan is required"},
		{[]string{"breakdown", "--plan", headPlan, "--format", "xml"}, ExitUsage, "unknown format"},
		{[]string{"breakdown", "--plan", headPlan, "--view", "sideways"}, ExitUsage, "--view"},
		{[]string{"breakdown", "--plan", headPlan, "extra"}, ExitUsage, "unexpected arguments"},
		{[]string{"breakdown", "--bogus"}, ExitUsage, "flag provided but not defined"},
		{[]string{"breakdown", "--plan", "missing.json"}, ExitError, "missing.json"},
		{[]string{"breakdown", "--plan", headPlan, "--snapshot", "missing.json"}, ExitError, "missing.json"},
		{[]string{"breakdown", "--plan", headPlan, "--usage", "missing.yml"}, ExitError, "missing.yml"},
	}
	for _, c := range cases {
		code, _, errOut := runCLI(t, c.args...)
		if code != c.code || !strings.Contains(errOut, c.msg) {
			t.Errorf("%v: exit %d, stderr %q", c.args, code, errOut)
		}
	}
}

func TestInvalidUsageFileIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u.yml")
	content := "version: 1\nresource_usage:\n  aws_lambda_function.thumbnailer:\n    monthly_requets: 5\n  aws_vpc.main:\n    x: 1\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, "breakdown", "--plan", headPlan, "--usage", path)
	if code != ExitError || !strings.Contains(errOut, `unknown key "monthly_requets"`) || !strings.Contains(errOut, "no usage-based costs") {
		t.Errorf("exit %d, stderr %s", code, errOut)
	}
	content = "version: 1\nresource_usage:\n  aws_lambda_function.renamed:\n    monthly_requests: 5\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runCLI(t, "breakdown", "--plan", headPlan, "--usage", path)
	if code != ExitOK || !strings.Contains(errOut, "matches no resource") {
		t.Errorf("unused entry: exit %d, stderr %s", code, errOut)
	}
}

func TestDiff(t *testing.T) {
	code, out, errOut := runCLI(t, "diff", "--base", basePlan, "--head", headPlan, "--usage", usageFile, "--format", "markdown")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "<!-- planprice:cost-diff -->") || !strings.Contains(out, "+$3,193.07") {
		t.Errorf("markdown diff:\n%s", out)
	}
	code, out, _ = runCLI(t, "diff", "--plan", synthetic, "--region", "eu-west-1")
	if code != ExitOK || !strings.Contains(out, "(before) -> ") || !strings.Contains(out, "aws_ebs_volume.old") {
		t.Errorf("single-plan diff: %d\n%s", code, out)
	}
	code, _, errOut = runCLI(t, "diff", "--base", basePlan, "--head", headPlan, "--fail-above-increase", "1000")
	if code != ExitThreshold || !strings.Contains(errOut, "exceeds threshold") {
		t.Errorf("threshold: %d %s", code, errOut)
	}
	if code, _, _ := runCLI(t, "diff", "--base", basePlan, "--head", headPlan, "--fail-above-increase", "5000", "--fail-above-percent", "5000"); code != ExitOK {
		t.Errorf("under threshold: %d", code)
	}
	if code, _, _ := runCLI(t, "diff", "--base", basePlan, "--head", headPlan, "--fail-above-percent", "10"); code != ExitThreshold {
		t.Errorf("percent threshold: %d", code)
	}
	if code, out, _ := runCLI(t, "diff", "--base", headPlan, "--head", basePlan, "--fail-above-increase", "0", "--format", "json"); code != ExitOK || !strings.Contains(out, `"delta_monthly_cost": -`) {
		t.Errorf("decrease should pass a zero threshold: %d", code)
	}
}

func TestDiffErrors(t *testing.T) {
	cases := [][]string{
		{"diff"},
		{"diff", "--base", basePlan},
		{"diff", "--plan", basePlan, "--head", headPlan},
		{"diff", "--base", basePlan, "--head", headPlan, "--fail-above-increase", "lots"},
		{"diff", "--base", basePlan, "--head", headPlan, "--fail-above-percent", "-1"},
		{"diff", "--base", basePlan, "--head", headPlan, "--format", "pdf"},
	}
	for _, args := range cases {
		if code, _, _ := runCLI(t, args...); code != ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	for _, args := range [][]string{
		{"diff", "--base", "missing.json", "--head", headPlan},
		{"diff", "--base", basePlan, "--head", "missing.json"},
		{"diff", "--plan", "missing.json"},
		{"diff", "--base", basePlan, "--head", headPlan, "--snapshot", "missing.json"},
	} {
		if code, _, _ := runCLI(t, args...); code != ExitError {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestUsageTemplateIsValidUsage(t *testing.T) {
	code, out, _ := runCLI(t, "usage-template", "--plan", headPlan)
	if code != ExitOK || !strings.Contains(out, "aws_lambda_function.workers[*]:") || !strings.Contains(out, "module.web.aws_nat_gateway.egress[*]:") {
		t.Fatalf("template: %d\n%s", code, out)
	}
	f, err := usage.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Validate(mapper.NewRegistry().UsageSchema); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runCLI(t, "usage-template"); code != ExitUsage {
		t.Error("missing --plan accepted")
	}
	if code, _, _ := runCLI(t, "usage-template", "--plan", "missing.json"); code != ExitError {
		t.Error("missing plan accepted")
	}
	outFile := filepath.Join(t.TempDir(), "usage.yml")
	if code, _, _ := runCLI(t, "usage-template", "--plan", multiPlan, "--out", outFile); code != ExitOK {
		t.Error("--out failed")
	}
}

// docs/RESOURCES.md is generated; this keeps it in sync with the mappers.
func TestResourcesDocIsUpToDate(t *testing.T) {
	code, out, _ := runCLI(t, "resources", "--format", "markdown")
	if code != ExitOK {
		t.Fatal(code)
	}
	path := filepath.Join("..", "..", "docs", "RESOURCES.md")
	if os.Getenv("UPDATE_DOCS") == "1" {
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != out {
		t.Fatal("docs/RESOURCES.md is stale: run `UPDATE_DOCS=1 go test ./internal/cli -run TestResourcesDocIsUpToDate`")
	}
	code, out, _ = runCLI(t, "resources")
	if code != ExitOK || !strings.Contains(out, "aws_lambda_function") || !strings.Contains(out, "usage keys: monthly_requests") {
		t.Errorf("table: %s", out)
	}
	if code, _, _ := runCLI(t, "resources", "--format", "yaml"); code != ExitUsage {
		t.Error("bad format accepted")
	}
}

func TestSnapshotCommands(t *testing.T) {
	code, out, _ := runCLI(t, "snapshot", "info")
	if code != ExitOK || !strings.Contains(out, "eu-west-1, us-east-1") || !strings.Contains(out, "AmazonEC2") {
		t.Errorf("info: %s", out)
	}
	if code, out, _ := runCLI(t, "snapshot", "verify"); code != ExitOK || !strings.Contains(out, "embedded snapshot OK") {
		t.Errorf("verify: %d %s", code, out)
	}
	code, out, _ = runCLI(t, "snapshot", "lookup", "--service", "AWSLambda", "--family", "Serverless",
		"--attr", "group=AWS-Lambda-Duration", "--quantity", "7000000000")
	if code != ExitOK || !strings.Contains(out, "[6000000000, 15000000000): $0.000015") || !strings.Contains(out, "$115,000.2000") {
		t.Errorf("lookup: %s", out)
	}
	if code, out, _ := runCLI(t, "snapshot", "diff", "embedded", "embedded"); code != ExitOK || !strings.Contains(out, "0 added, 0 removed, 0 changed") {
		t.Errorf("diff: %s", out)
	}
	for _, args := range [][]string{
		{"snapshot"},
		{"snapshot", "explode"},
		{"snapshot", "diff", "embedded"},
		{"snapshot", "lookup", "--family", "x"},
		{"snapshot", "lookup", "--service", "x"},
		{"snapshot", "lookup", "--service", "AmazonEC2", "--family", "Storage", "--attr", "novalue"},
		{"snapshot", "lookup", "--service", "AmazonEC2", "--family", "Storage", "--attr", "volumeApiName=gp3", "--quantity", "x"},
		{"snapshot", "verify", "extra"},
		{"snapshot", "info", "--bogus"},
	} {
		if code, _, _ := runCLI(t, args...); code != ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	for _, args := range [][]string{
		{"snapshot", "lookup", "--service", "AmazonEC2", "--family", "Storage", "--attr", "volumeApiName=gp9"},
		{"snapshot", "info", "--snapshot", "missing.json"},
		{"snapshot", "diff", "missing.json", "embedded"},
		{"snapshot", "diff", "embedded", "missing.json"},
		{"snapshot", "lookup", "--snapshot", "missing.json", "--service", "a", "--family", "b"},
	} {
		if code, _, _ := runCLI(t, args...); code != ExitError {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}
