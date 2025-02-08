package mapper

import (
	"fmt"

	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

// RDS gp3 baseline performance for MySQL, MariaDB and PostgreSQL: volumes
// under 400 GB include 3,000 IOPS and 125 MiB/s; 400 GB and larger include
// 12,000 IOPS and 500 MiB/s. Only the excess is billed.
var (
	rdsGP3LargeVolumeGB    = money.FromInt(400)
	rdsGP3SmallIOPS        = money.FromInt(3000)
	rdsGP3SmallThroughput  = money.FromInt(125)
	rdsGP3LargeIOPS        = money.FromInt(12000)
	rdsGP3LargeThroughput  = money.FromInt(500)
	rdsEngineToPriceEngine = map[string]string{
		"postgres": "PostgreSQL",
		"mysql":    "MySQL",
		"mariadb":  "MariaDB",
	}
)

type dbInstanceMapper struct{}

func (dbInstanceMapper) ResourceType() string { return "aws_db_instance" }

func (dbInstanceMapper) Doc() Doc {
	return Doc{
		Service: "Amazon RDS",
		Summary: "RDS DB instance for MySQL, MariaDB or PostgreSQL.",
		Priced: []string{
			"Instance hours, Single-AZ or Multi-AZ (one standby)",
			"Storage per GB-month for gp2, gp3, io1, io2 and magnetic",
			"io1/io2 provisioned IOPS; gp3 IOPS and throughput above the baseline",
			"Backup storage beyond the free allocation (usage-based)",
		},
		Assumptions: []string{
			"gp3 baseline is 3,000 IOPS / 125 MiB/s under 400 GB, 12,000 IOPS / 500 MiB/s from 400 GB",
			"An unset or unknown storage_type is priced as gp2 (io1 when iops is set), matching the RDS API default",
			"Aurora, Oracle, SQL Server, Multi-AZ DB clusters and Reserved Instances are not priced",
		},
	}
}

func (dbInstanceMapper) UsageSchema() []usage.Field {
	return []usage.Field{
		{Key: "additional_backup_storage_gb", Kind: usage.Number, Example: "0", Description: "backup storage above 100% of allocated storage, GB-months"},
	}
}

func (dbInstanceMapper) Map(r tfplan.Resource, u usage.Values) Result {
	var res Result
	engineAttr := r.Attrs.StringOr("engine", "")
	engine, supported := rdsEngineToPriceEngine[engineAttr]
	multiAZ, _ := r.Attrs.Bool("multi_az")
	deployment := "Single-AZ"
	prefix := "RDS:"
	if multiAZ {
		deployment = "Multi-AZ"
		prefix = "RDS:Multi-AZ-"
	}

	class, known := r.Attrs.String("instance_class")
	switch {
	case !supported:
		res.Components = append(res.Components, unsupportedComponent("instance", "Database instance", fmt.Sprintf("engine %q is not priced (supported: mysql, mariadb, postgres)", engineAttr)))
	case !known || class == "":
		res.Components = append(res.Components, unknownComponent("instance", "Database instance", "hours", "instance_class is unknown"))
	default:
		res.Components = append(res.Components, ready("instance",
			fmt.Sprintf("Database instance (%s, %s, %s)", engine, deployment, class), "hours", hoursPerMonth,
			query("AmazonRDS", r.Region, "Database Instance", "instanceType", class, "databaseEngine", engine, "deploymentOption", deployment)))
	}

	size, ok := r.Attrs.Number("allocated_storage")
	if !ok || size.IsZero() {
		res.Components = append(res.Components, unknownComponent("storage", "Storage", "GB-months", "allocated_storage is unknown"))
		return res
	}
	iops := r.Attrs.NumberOr("iops", money.Zero)
	storageType := r.Attrs.StringOr("storage_type", "")
	if storageType == "" {
		storageType = "gp2"
		if !iops.IsZero() {
			storageType = "io1"
		}
	}
	res.Components = append(res.Components, rdsStorageComponents(r, prefix, storageType, size, iops)...)

	backup, have := u.Number("additional_backup_storage_gb")
	if have {
		res.Components = append(res.Components, usageComponent("backup", "Additional backup storage", "GB-months", backup, true, "additional_backup_storage_gb",
			query("AmazonRDS", r.Region, "Storage Snapshot", "usagetype", "RDS:ChargedBackupUsage")))
	}
	return res
}

func rdsStorageComponents(r tfplan.Resource, prefix, storageType string, size, iops money.Decimal) []Component {
	storageUsage := map[string]string{
		"gp2": "GP2-Storage", "gp3": "GP3-Storage", "io1": "PIOPS-Storage", "io2": "PIOPS-Storage-IO2", "standard": "StorageUsage",
	}
	ut, ok := storageUsage[storageType]
	if !ok {
		return []Component{unsupportedComponent("storage", "Storage ("+storageType+")", "storage type "+storageType+" is not priced")}
	}
	storage := ready("storage", fmt.Sprintf("Storage (%s)", storageType), "GB-months", size,
		query("AmazonRDS", r.Region, "Database Storage", "usagetype", prefix+ut))
	out := []Component{storage}
	switch storageType {
	case "io1", "io2":
		if iops.IsZero() {
			return append(out, unknownComponent("iops", "Provisioned IOPS ("+storageType+")", "IOPS-months", iopsReason(r.Attrs.Unknown("iops"))))
		}
		iopsUsage := "PIOPS"
		if storageType == "io2" {
			iopsUsage = "IO2-PIOPS"
		}
		out = append(out, ready("iops", fmt.Sprintf("Provisioned IOPS (%s)", storageType), "IOPS-months", iops,
			query("AmazonRDS", r.Region, "Provisioned IOPS", "usagetype", prefix+iopsUsage)))
	case "gp3":
		baseIOPS, baseThroughput := rdsGP3SmallIOPS, rdsGP3SmallThroughput
		if size.Cmp(rdsGP3LargeVolumeGB) >= 0 {
			baseIOPS, baseThroughput = rdsGP3LargeIOPS, rdsGP3LargeThroughput
		}
		if extra := iops.Sub(baseIOPS); extra.Sign() > 0 {
			out = append(out, ready("iops", fmt.Sprintf("Provisioned IOPS above %s (gp3)", money.FormatQuantity(baseIOPS, 0)), "IOPS-months", extra,
				query("AmazonRDS", r.Region, "Provisioned IOPS", "usagetype", prefix+"GP3-PIOPS")))
		}
		throughput := r.Attrs.NumberOr("storage_throughput", money.Zero)
		if extra := throughput.Sub(baseThroughput); extra.Sign() > 0 {
			out = append(out, ready("throughput", fmt.Sprintf("Throughput above %s MiB/s (gp3)", baseThroughput), "MiBps-months", extra,
				query("AmazonRDS", r.Region, "Provisioned Throughput", "usagetype", prefix+"GP3-Throughput")))
		}
	}
	return out
}
