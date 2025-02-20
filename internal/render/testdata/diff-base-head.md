<!-- planprice:cost-diff -->
### planprice: monthly cost will increase by **+$3,193.07** (+624.0%)

| | Monthly cost |
|---|---:|
| Base `testdata/plans/base.json` | $511.74 |
| Head `testdata/plans/head.json` | $3,704.82 |
| **Change** | **+$3,193.07 (+624.0%)** |

| Resource | Change | Base | Head | Diff |
|---|---|---:|---:|---:|
| `aws_ebs_volume.journal` | added | - | $2,506.50 | +$2,506.50 |
| `aws_ebs_volume.scratch` | changed | $20.00 | $26.00 | +$6.00 |
| `aws_lambda_function.thumbnailer` | changed | $6.80 | $10.40 | +$3.60 |
| `aws_lambda_function.workers["ocr"]` | added | - | $16.08 | +$16.08 |
| `aws_lambda_function.workers["resize"]` | added | - | $16.08 | +$16.08 |
| `aws_lb.ingest` | added | - | $25.19 | +$25.19 |
| `module.data.aws_db_instance.main` | changed | $78.31 | $481.39 | +$403.08 |
| `module.web.aws_instance.web[0]` | changed | $32.77 | $74.08 | +$41.31 |
| `module.web.aws_instance.web[1]` | changed | $32.77 | $74.08 | +$41.31 |
| `module.web.aws_instance.web[2]` | added | - | $74.08 | +$74.08 |
| `module.web.aws_nat_gateway.egress[1]` | added | - | $59.85 | +$59.85 |

<details><summary>Component changes</summary>

| Resource | Component | Base | Head | Diff |
|---|---|---:|---:|---:|
| `aws_ebs_volume.journal` | Storage (io2) | $0.00 | $62.50 | +$62.50 |
| `aws_ebs_volume.journal` | Provisioned IOPS 0-32,000 (io2) | $0.00 | $2,080.00 | +$2,080.00 |
| `aws_ebs_volume.journal` | Provisioned IOPS 32,001-64,000 (io2) | $0.00 | $364.00 | +$364.00 |
| `aws_ebs_volume.scratch` | Storage (gp2) -> Storage (gp3) | $20.00 | $16.00 | -$4.00 |
| `aws_ebs_volume.scratch` | Provisioned IOPS above 3,000 (gp3) | $0.00 | $5.00 | +$5.00 |
| `aws_ebs_volume.scratch` | Throughput above 125 MiB/s (gp3) | $0.00 | $5.00 | +$5.00 |
| `aws_lambda_function.thumbnailer` | Duration (512 MB, x86_64) -> Duration (1024 MB, arm64) | $6.00 | $9.60 | +$3.60 |
| `aws_lambda_function.workers["ocr"]` | Requests | $0.00 | $0.05 | +$0.05 |
| `aws_lambda_function.workers["ocr"]` | Duration (2048 MB, arm64) | $0.00 | $16.00 | +$16.00 |
| `aws_lambda_function.workers["ocr"]` | Ephemeral storage above 512 MB (2048 MB) | $0.00 | $0.03 | +$0.03 |
| `aws_lambda_function.workers["resize"]` | Requests | $0.00 | $0.05 | +$0.05 |
| `aws_lambda_function.workers["resize"]` | Duration (2048 MB, arm64) | $0.00 | $16.00 | +$16.00 |
| `aws_lambda_function.workers["resize"]` | Ephemeral storage above 512 MB (2048 MB) | $0.00 | $0.03 | +$0.03 |
| `aws_lb.ingest` | Network load balancer | $0.00 | $16.43 | +$16.43 |
| `aws_lb.ingest` | Capacity units (NLCU-hours) | $0.00 | $8.76 | +$8.76 |
| `module.data.aws_db_instance.main` | Database instance (PostgreSQL, Single-AZ, db.t3.medium) -> Database instance (PostgreSQL, Multi-AZ, db.m6g.large) | $52.56 | $232.14 | +$179.58 |
| `module.data.aws_db_instance.main` | Storage (gp3) | $11.50 | $115.00 | +$103.50 |
| `module.data.aws_db_instance.main` | Provisioned IOPS above 12,000 (gp3) | $0.00 | $120.00 | +$120.00 |
| `module.web.aws_instance.web[0]` | Instance usage (Linux, on-demand, t3.medium) -> Instance usage (Linux, on-demand, m6i.large) | $30.37 | $70.08 | +$39.71 |
| `module.web.aws_instance.web[0]` | root_block_device / Storage (gp3) | $2.40 | $4.00 | +$1.60 |
| `module.web.aws_instance.web[1]` | Instance usage (Linux, on-demand, t3.medium) -> Instance usage (Linux, on-demand, m6i.large) | $30.37 | $70.08 | +$39.71 |
| `module.web.aws_instance.web[1]` | root_block_device / Storage (gp3) | $2.40 | $4.00 | +$1.60 |
| `module.web.aws_instance.web[2]` | Instance usage (Linux, on-demand, m6i.large) | $0.00 | $70.08 | +$70.08 |
| `module.web.aws_instance.web[2]` | root_block_device / Storage (gp3) | $0.00 | $4.00 | +$4.00 |
| `module.web.aws_nat_gateway.egress[1]` | NAT gateway | $0.00 | $32.85 | +$32.85 |
| `module.web.aws_nat_gateway.egress[1]` | Data processed | $0.00 | $27.00 | +$27.00 |

</details>

<details><summary>Cost components</summary>

| Resource | Component | Quantity | Unit price | Monthly |
|---|---|---:|---:|---:|
| `aws_ebs_volume.journal` | Storage (io2) | 500 GB-months | $0.125 | $62.50 |
| `aws_ebs_volume.journal` | Provisioned IOPS 0-32,000 (io2) | 32,000 IOPS-months | $0.065 | $2,080.00 |
| `aws_ebs_volume.journal` | Provisioned IOPS 32,001-64,000 (io2) | 8,000 IOPS-months | $0.0455 | $364.00 |
| `aws_ebs_volume.scratch` | Storage (gp3) | 200 GB-months | $0.08 | $16.00 |
| `aws_ebs_volume.scratch` | Provisioned IOPS above 3,000 (gp3) | 1,000 IOPS-months | $0.005 | $5.00 |
| `aws_ebs_volume.scratch` | Throughput above 125 MiB/s (gp3) | 125 MiBps-months | $0.04 | $5.00 |
| `aws_lambda_function.thumbnailer` | Requests | 4 million requests | $0.20 | $0.80 |
| `aws_lambda_function.thumbnailer` | Duration (1024 MB, arm64) | 720,000 GB-seconds | $0.0000133334* | $9.60 |
| `aws_lambda_function.workers["ocr"]` | Requests | 0.25 million requests | $0.20 | $0.05 |
| `aws_lambda_function.workers["ocr"]` | Duration (2048 MB, arm64) | 1,200,000 GB-seconds | $0.0000133334* | $16.00 |
| `aws_lambda_function.workers["ocr"]` | Ephemeral storage above 512 MB (2048 MB) | 900,000 GB-seconds | $0.0000000309 | $0.03 |
| `aws_lambda_function.workers["resize"]` | Requests | 0.25 million requests | $0.20 | $0.05 |
| `aws_lambda_function.workers["resize"]` | Duration (2048 MB, arm64) | 1,200,000 GB-seconds | $0.0000133334* | $16.00 |
| `aws_lambda_function.workers["resize"]` | Ephemeral storage above 512 MB (2048 MB) | 900,000 GB-seconds | $0.0000000309 | $0.03 |
| `aws_lb.ingest` | Network load balancer | 730 hours | $0.0225 | $16.43 |
| `aws_lb.ingest` | Capacity units (NLCU-hours) | 1,460 NLCU-hours | $0.006 | $8.76 |
| `module.data.aws_db_instance.main` | Database instance (PostgreSQL, Multi-AZ, db.m6g.large) | 730 hours | $0.318 | $232.14 |
| `module.data.aws_db_instance.main` | Storage (gp3) | 500 GB-months | $0.23 | $115.00 |
| `module.data.aws_db_instance.main` | Provisioned IOPS above 12,000 (gp3) | 3,000 IOPS-months | $0.04 | $120.00 |
| `module.data.aws_db_instance.main` | Additional backup storage | 150 GB-months | $0.095 | $14.25 |
| `module.data.aws_s3_bucket.assets` | standard / Storage | 2,500 GB-months | $0.023* | $57.50 |
| `module.data.aws_s3_bucket.assets` | standard / PUT, COPY, POST, LIST requests | 400 thousand requests | $0.005 | $2.00 |
| `module.data.aws_s3_bucket.assets` | standard / GET and other requests | 12,000 thousand requests | $0.0004 | $4.80 |
| `module.data.aws_s3_bucket.assets` | standard_infrequent_access / Storage | 8,000 GB-months | $0.0125 | $100.00 |
| `module.data.aws_s3_bucket.assets` | standard_infrequent_access / Data retrieval | 300 GB | $0.01 | $3.00 |
| `module.data.aws_s3_bucket.logs` | glacier_instant_retrieval / Storage | 20,000 GB-months | $0.004 | $80.00 |
| `module.web.aws_instance.web[0]` | Instance usage (Linux, on-demand, m6i.large) | 730 hours | $0.096 | $70.08 |
| `module.web.aws_instance.web[0]` | root_block_device / Storage (gp3) | 50 GB-months | $0.08 | $4.00 |
| `module.web.aws_instance.web[1]` | Instance usage (Linux, on-demand, m6i.large) | 730 hours | $0.096 | $70.08 |
| `module.web.aws_instance.web[1]` | root_block_device / Storage (gp3) | 50 GB-months | $0.08 | $4.00 |
| `module.web.aws_instance.web[2]` | Instance usage (Linux, on-demand, m6i.large) | 730 hours | $0.096 | $70.08 |
| `module.web.aws_instance.web[2]` | root_block_device / Storage (gp3) | 50 GB-months | $0.08 | $4.00 |
| `module.web.aws_lb.app` | Application load balancer | 730 hours | $0.0225 | $16.43 |
| `module.web.aws_lb.app` | Capacity units (LCU-hours) | 2,190 LCU-hours | $0.008 | $17.52 |
| `module.web.aws_nat_gateway.egress[0]` | NAT gateway | 730 hours | $0.045 | $32.85 |
| `module.web.aws_nat_gateway.egress[0]` | Data processed | 600 GB | $0.045 | $27.00 |
| `module.web.aws_nat_gateway.egress[1]` | NAT gateway | 730 hours | $0.045 | $32.85 |
| `module.web.aws_nat_gateway.egress[1]` | Data processed | 600 GB | $0.045 | $27.00 |

\* tiered price; the blended rate for the quantity is shown.

</details>

**Unsupported resource types (not included):** aws_eip (2)

<sub>Prices: AWS Price List Bulk API (public, on-demand terms only), snapshot 2026-10-25; 730 hours/month. Generated by planprice.</sub>
