package mapper

import (
	"fmt"

	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

// EBS volume baselines and io2 IOPS tier boundaries, from the EBS pricing
// page: gp3 includes 3,000 IOPS and 125 MiB/s; io2 IOPS are billed in bands
// of 0-32,000, 32,001-64,000 and above 64,000.
var (
	gp3BaselineIOPS       = money.FromInt(3000)
	gp3BaselineThroughput = money.FromInt(125)
	io2Tier1Limit         = money.FromInt(32000)
	io2Tier2Limit         = money.FromInt(64000)
	defaultRootVolumeGB   = money.FromInt(8)
	mibPerGib             = money.FromInt(1024)
)

// defaultVolumeType is what EC2 uses when a volume type is not given.
const defaultVolumeType = "gp2"

type ebsVolumeMapper struct{}

func (ebsVolumeMapper) ResourceType() string { return "aws_ebs_volume" }

func (ebsVolumeMapper) Doc() Doc {
	return Doc{
		Service: "Amazon EBS",
		Summary: "Standalone EBS volume.",
		Priced: []string{
			"Storage per GB-month for gp2, gp3, io1, io2, st1, sc1 and standard",
			"gp3 IOPS above 3,000 and throughput above 125 MiB/s",
			"io1 provisioned IOPS; io2 provisioned IOPS in three tiers",
		},
		Assumptions: []string{
			"An unset or unknown type is priced as gp2 (the EC2 API default)",
			"Snapshots and magnetic (standard) I/O requests are not priced",
		},
	}
}

func (ebsVolumeMapper) UsageSchema() []usage.Field { return nil }

func (ebsVolumeMapper) Map(r tfplan.Resource, _ usage.Values) Result {
	volType := r.Attrs.StringOr("type", defaultVolumeType)
	size, ok := r.Attrs.Number("size")
	if !ok || size.IsZero() {
		if r.Attrs.Unknown("size") || r.Attrs.Has("snapshot_id") {
			return Result{Components: []Component{unknownComponent("storage", "Storage", "GB", "size is known only after apply (e.g. taken from snapshot)")}}
		}
		return Result{Components: []Component{unknownComponent("storage", "Storage", "GB", "size not set")}}
	}
	iops := r.Attrs.NumberOr("iops", money.Zero)
	throughput := r.Attrs.NumberOr("throughput", money.Zero)
	return Result{Components: ebsComponents(r.Region, volType, size, iops, throughput, r.Attrs.Unknown("iops"))}
}

// ebsComponents prices one EBS volume (standalone or attached to an
// instance). iopsUnknown marks io1/io2 volumes whose IOPS are computed.
func ebsComponents(region, volType string, sizeGB, iops, throughputMiBps money.Decimal, iopsUnknown bool) []Component {
	storage := ready("storage", fmt.Sprintf("Storage (%s)", volType), "GB-months", sizeGB,
		query("AmazonEC2", region, "Storage", "volumeApiName", volType))
	switch volType {
	case "gp2", "st1", "sc1", "standard":
		return []Component{storage}
	case "gp3":
		out := []Component{storage}
		if extra := iops.Sub(gp3BaselineIOPS); extra.Sign() > 0 {
			out = append(out, ready("iops", "Provisioned IOPS above 3,000 (gp3)", "IOPS-months", extra,
				query("AmazonEC2", region, "System Operation", "volumeApiName", "gp3", "usagetype", "EBS:VolumeP-IOPS.gp3")))
		}
		if extra := throughputMiBps.Sub(gp3BaselineThroughput); extra.Sign() > 0 {
			// AWS lists gp3 throughput per GiBps-month; show it per MiB/s.
			perMiB, _ := money.FromInt(1).Div(mibPerGib)
			c := ready("throughput", "Throughput above 125 MiB/s (gp3)", "MiBps-months", extra,
				query("AmazonEC2", region, "Provisioned Throughput", "volumeApiName", "gp3"))
			c.Scale = perMiB
			out = append(out, c)
		}
		return out
	case "io1":
		if iops.IsZero() {
			return []Component{storage, unknownComponent("iops", "Provisioned IOPS (io1)", "IOPS-months", iopsReason(iopsUnknown))}
		}
		return []Component{storage, ready("iops", "Provisioned IOPS (io1)", "IOPS-months", iops,
			query("AmazonEC2", region, "System Operation", "volumeApiName", "io1", "usagetype", "EBS:VolumeP-IOPS.piops"))}
	case "io2":
		if iops.IsZero() {
			return []Component{storage, unknownComponent("iops", "Provisioned IOPS (io2)", "IOPS-months", iopsReason(iopsUnknown))}
		}
		return append([]Component{storage}, io2IOPSComponents(region, iops)...)
	default:
		return []Component{unsupportedComponent("storage", "Storage ("+volType+")", "volume type "+volType+" is not in the price snapshot")}
	}
}

// io2IOPSComponents splits provisioned IOPS across the three io2 bands; each
// band is a separate product in the AWS price list.
func io2IOPSComponents(region string, iops money.Decimal) []Component {
	bands := []struct {
		key, name, usageType string
		from, to             money.Decimal
		open                 bool
	}{
		{"iops_tier1", "Provisioned IOPS 0-32,000 (io2)", "EBS:VolumeP-IOPS.io2", money.Zero, io2Tier1Limit, false},
		{"iops_tier2", "Provisioned IOPS 32,001-64,000 (io2)", "EBS:VolumeP-IOPS.io2.tier2", io2Tier1Limit, io2Tier2Limit, false},
		{"iops_tier3", "Provisioned IOPS above 64,000 (io2)", "EBS:VolumeP-IOPS.io2.tier3", io2Tier2Limit, money.Zero, true},
	}
	var out []Component
	for _, b := range bands {
		upper := iops
		if !b.open {
			upper = upper.Min(b.to)
		}
		qty := upper.Sub(b.from)
		if qty.Sign() <= 0 {
			break
		}
		out = append(out, ready(b.key, b.name, "IOPS-months", qty,
			query("AmazonEC2", region, "System Operation", "volumeApiName", "io2", "usagetype", b.usageType)))
	}
	return out
}

func iopsReason(unknown bool) string {
	if unknown {
		return "iops is known only after apply"
	}
	return "iops not set"
}
