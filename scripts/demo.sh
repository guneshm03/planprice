#!/usr/bin/env bash
# Offline end-to-end demo: prices the bundled Terraform plans (generated from
# examples/ by scripts/gen-plans.sh) with the embedded price snapshot.
# No network, no AWS credentials, no Terraform needed.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
bin=./bin/planprice
[ -x "$bin" ] || go build -o "$bin" ./cmd/planprice
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

step() { printf '\n==> %s\n$ %s\n\n' "$1" "$2"; }

step "Verify the embedded price snapshot" "planprice snapshot verify"
$bin snapshot verify

step "Monthly cost of the head plan, with usage assumptions" \
  "planprice breakdown --plan testdata/plans/head.json --usage examples/usage.yml"
$bin breakdown --plan testdata/plans/head.json --usage examples/usage.yml

step "Cost diff base -> head as a pull-request comment (Markdown)" \
  "planprice diff --base testdata/plans/base.json --head testdata/plans/head.json --usage examples/usage.yml --format markdown"
$bin diff --base testdata/plans/base.json --head testdata/plans/head.json \
  --usage examples/usage.yml --format markdown --out "$out/comment.md"
# Print the summary part of the comment; the full file includes collapsible details.
sed -n '1,/^<details><summary>Component changes/p' "$out/comment.md"
echo "... ($(wc -l <"$out/comment.md" | tr -d ' ') lines in total)"

step "Two regions via a provider alias resolved from a variable" \
  "planprice breakdown --plan testdata/plans/multi-region.json --usage examples/usage-multi-region.yml"
$bin breakdown --plan testdata/plans/multi-region.json --usage examples/usage-multi-region.yml

step "CI gate: fail when the monthly increase exceeds \$500" \
  "planprice diff --base testdata/plans/base.json --head testdata/plans/head.json --format json --fail-above-increase 500"
set +e
$bin diff --base testdata/plans/base.json --head testdata/plans/head.json --format json \
  --fail-above-increase 500 >"$out/diff.json"
code=$?
set -e
echo "exit code: $code (3 = threshold exceeded)"

step "Usage-file skeleton for the head plan (first 16 lines)" \
  "planprice usage-template --plan testdata/plans/head.json"
$bin usage-template --plan testdata/plans/head.json | head -16
