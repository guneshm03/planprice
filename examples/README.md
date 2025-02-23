# Example Terraform configurations

These configs exist to produce **real** `terraform show -json` plans for
planprice's tests and demo without an AWS account.

| Directory | What it plans |
|---|---|
| `base/` | A small web stack: VPC, 2 x t3.medium behind an ALB, one NAT gateway, PostgreSQL on db.t3.medium (gp3, 100 GB), two S3 buckets, a Lambda function and a 200 GB gp2 volume. |
| `head/` | The same stack after a hypothetical pull request: 3 x m6i.large, two NAT gateways, Multi-AZ db.m6g.large on 500 GB gp3 with 15,000 IOPS, Lambda on arm64 plus two worker functions with 2 GB ephemeral storage, gp3 scratch volume, an io2 journal volume (40,000 IOPS) and an NLB. |
| `multi-region/` | Two regions through a provider alias whose region comes from a variable; a t3 instance in unlimited credit mode with st1 data volume, a MySQL io1 replica and S3 bucket in eu-west-1, and a Classic Load Balancer. |
| `modules/web-tier`, `modules/data-tier` | Shared modules (so resources have `module.*` addresses). |

Every root config uses this provider block:

```hcl
provider "aws" {
  region                      = "us-east-1"
  access_key                  = "mock_access_key"
  secret_key                  = "mock_secret_key"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}
```

With fake keys, all account checks skipped and no data sources, `terraform
plan` for resources that do not exist yet makes no AWS API calls. AMI, subnet
and VPC ids are hard-coded placeholders for the same reason.

Regenerate the plans (needs Terraform and access to registry.terraform.io to
download the pinned provider; takes a few minutes, mostly the download):

```sh
make plans   # runs scripts/gen-plans.sh, writes testdata/plans/*.json
```

`usage.yml` and `usage-multi-region.yml` hold illustrative usage figures for
the demo.
