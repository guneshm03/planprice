# Price snapshot: contents and refresh procedure

planprice never calls a pricing API at estimate time. It prices plans against
a **snapshot**: a curated subset of the public AWS Price List Bulk API,
committed to the repository as `internal/pricedata/aws-prices.json` and
embedded in the binary. This document explains what is in it, how it is
built, and how to refresh it.

## What the snapshot contains

| | |
|---|---|
| Source | AWS Price List Bulk API, `https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/<Service>/current/<region>/index.json` (public, no credentials) |
| Services | `AmazonEC2` (instances, EBS, NAT gateway, CPU credits), `AmazonRDS`, `AmazonS3`, `AWSLambda`, `AWSELB` |
| Regions | `us-east-1`, `eu-west-1` |
| Terms | On-demand only (no Reserved Instances, Savings Plans or Spot) |
| Built | 2026-10-25 (`generated_at` field); offer versions per service are listed by `planprice snapshot info` |
| Size | 7,587 prices, about 1.8 MB of JSON, one price per line |
| Integrity | `aws-prices.json.sha256`, checked by `planprice snapshot verify` and by `go test ./internal/pricedata` |

Each price is reduced to the attributes a mapper queries by, for example:

```json
{"service":"AmazonEC2","region":"us-east-1","family":"Compute Instance","attrs":{"instanceType":"t3.micro","operatingSystem":"Linux"},"unit":"Hrs","tiers":[{"start":0,"usd":0.0104}],"sku":"..."}
```

Tiered prices (S3 Standard storage, Lambda duration) keep every band:
`start`/`end` are quantities in the price unit and the last band has no `end`.

To inspect one price, with an optional quantity priced through its tiers:

```console
$ planprice snapshot lookup --service AWSLambda --family Serverless \
    --attr group=AWS-Lambda-Duration --quantity 7000000000
AWSLambda/us-east-1/Serverless{group=AWS-Lambda-Duration}
sku TG3M4CAGBA3NYQBH, unit Lambda-GB-Second
  [0, 6000000000): $0.0000166667
  [6000000000, 15000000000): $0.000015
  [15000000000, Inf): $0.0000133334
cost of 7000000000 Lambda-GB-Second: $115,000.2000
```

## How it is built

`cmd/pricegen` builds the snapshot in two stages so that each large download
happens once and the selection rules can be changed offline.

1. **fetch** streams each offer file through a *coarse* filter
   (`internal/pricegen/fetch.go`) and writes a *slim* offer file to
   `.cache/offers/` (git-ignored). The EC2 offer file for one region is about
   450-480 MB; it is decoded token by token, product by product, and never
   held in memory or written to disk. Only products that could matter are
   kept (for EC2: Linux/Windows, shared tenancy, `capacitystatus=Used`, no
   pre-installed software, plus the EBS, NAT gateway and CPU credit
   families), which leaves about 2,000 products per region. The 2026-10-25
   run streamed 10 offer files in about 10.5 minutes with a peak RSS of
   about 100 MB.
2. **build** applies precise per-service rules (`internal/pricegen/rules.go`)
   to the slim files, strips the region prefix from usage types
   (`EU-NatGateway-Hours` becomes `NatGateway-Hours`), collapses identical
   duplicate products, **refuses** conflicting duplicates, sorts prices by
   identity and writes one price per line plus the checksum file.

Re-running `build` on the same slim files produces byte-identical output.

## Refresh procedure

Needs network access to `pricing.us-east-1.amazonaws.com` and about 15 minutes.

```sh
# 0. Keep the current snapshot for comparison.
git show HEAD:internal/pricedata/aws-prices.json > /tmp/old-prices.json

# 1. Stream the offer files and build the snapshot (both steps).
make snapshot
#    equivalent to:
#    go run ./cmd/pricegen fetch --cache .cache/offers
#    go run ./cmd/pricegen build --cache .cache/offers --out internal/pricedata/aws-prices.json

# 2. Review what changed against the committed snapshot.
go run ./cmd/planprice snapshot diff /tmp/old-prices.json internal/pricedata/aws-prices.json

# 3. Expect test failures where hand-computed expectations used old prices.
#    Update the expected values (and the price comments above them) by hand;
#    never regenerate expectations from the tool's own output.
make check

# 4. Regenerate the golden outputs and README demo, review, and commit.
go test ./internal/render -update
make demo
```

`snapshot diff` prints one line per added, removed or re-priced product
(illustrative output; the prices shown are not a real change):

```text
changed  AmazonEC2/us-east-1/Compute Instance{instanceType=t3.micro,operatingSystem=Linux}  $0.0104 -> $0.011 per Hrs
1 added, 0 removed, 1 changed
```

### Adding a region

1. Add the region's usage-type prefix to `regionUsagePrefix` in
   `internal/pricegen/rules.go` (for example `"ap-south-1": "APS3-"`; the
   prefix is visible in any usage type of that region's offer file).
2. Run `go run ./cmd/pricegen fetch --region <existing...> --region <new>`
   and the matching `build` (the Makefile target uses the default region list
   in `cmd/pricegen`; update `regions` there to make it permanent).
3. Update `TestEmbeddedSnapshotCoverage` in `internal/pricedata` and the region
   list in this document and the README.

### Adding a priced product

Mappers query prices by attributes; a query that matches nothing reports the
component as `price-not-found` rather than failing. To price something new:

1. Make sure the coarse filter keeps its product family (otherwise re-fetch).
2. Add a rule in `buildRules` that selects it and keeps the attributes the
   mapper will query by; rebuild with `pricegen build` (no download needed).
3. Add the mapper code and a test with a hand-computed expected cost.

## Why a snapshot instead of a live API

- Estimates are **reproducible**: the same plan and usage file always give
  the same number until the snapshot is deliberately refreshed in a reviewed
  commit.
- No credentials or network at estimate time; works in sandboxed CI.
- The Price List Query API needs AWS credentials; the Bulk API files are huge.
  A curated 1.8 MB subset is small enough to embed and review.

The cost: prices are as of the snapshot date. `planprice snapshot info` shows
the offer versions so a reader can judge staleness.
