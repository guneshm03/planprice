#!/usr/bin/env bash
# Regenerate testdata/plans/*.json from the Terraform configs in examples/.
#
# Needs: terraform >= 1.6 and network access to registry.terraform.io (to
# download the pinned hashicorp/aws provider). Needs NO AWS credentials: the
# example provider blocks use static fake keys and skip every credential and
# account check, and the configs use no data sources, so `plan` never calls an
# AWS API. Provider binaries and .terraform state live in a temp dir that is
# deleted on exit, so nothing large is left in the repo.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/testdata/plans"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

export TF_PLUGIN_CACHE_DIR="$work/plugin-cache"
export TF_IN_AUTOMATION=1
export CHECKPOINT_DISABLE=1
mkdir -p "$TF_PLUGIN_CACHE_DIR" "$out"

for name in base head multi-region; do
  dir="$root/examples/$name"
  export TF_DATA_DIR="$work/$name/.terraform"
  echo ">> $name: init" >&2
  terraform -chdir="$dir" init -input=false -no-color >/dev/null
  echo ">> $name: plan" >&2
  terraform -chdir="$dir" plan -input=false -no-color -lock=false -out="$work/$name.tfplan" >/dev/null
  # jq sorts nothing; it only pretty-prints so plan changes diff line by line.
  terraform -chdir="$dir" show -json "$work/$name.tfplan" | jq . >"$out/$name.json"
  echo ">> $name: wrote testdata/plans/$name.json" >&2
done
