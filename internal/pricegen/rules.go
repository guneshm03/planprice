package pricegen

import (
	"slices"
	"strings"
)

// regionUsagePrefix maps a region to the prefix AWS puts on usage types in
// that region's offer files (us-east-1 mostly has none). Stripping it gives
// region-independent usage types, so mappers can query "NatGateway-Hours"
// in every region. Add a row here when adding a region to the snapshot.
var regionUsagePrefix = map[string]string{
	"us-east-1":      "USE1-",
	"us-east-2":      "USE2-",
	"us-west-1":      "USW1-",
	"us-west-2":      "USW2-",
	"eu-west-1":      "EU-",
	"eu-west-2":      "EUW2-",
	"eu-central-1":   "EUC1-",
	"ap-south-1":     "APS3-",
	"ap-southeast-1": "APS1-",
	"ap-southeast-2": "APS2-",
	"ap-northeast-1": "APN1-",
}

// SupportedRegion reports whether pricegen knows the region's usage prefix.
func SupportedRegion(region string) bool {
	_, ok := regionUsagePrefix[region]
	return ok
}

// NormalizeUsageType strips the region's usage-type prefix, e.g.
// "EU-NatGateway-Hours" -> "NatGateway-Hours" for eu-west-1.
func NormalizeUsageType(region, usageType string) string {
	return strings.TrimPrefix(usageType, regionUsagePrefix[region])
}

// rule selects products of one family and reduces each to the attributes a
// mapper queries by. Rules are tried in order; the first whose family and
// match accept a product wins. Products no rule accepts are dropped.
type rule struct {
	family string
	match  func(a map[string]string, usage string) bool
	attrs  func(a map[string]string, usage string) map[string]string
}

func pick(keys ...string) func(map[string]string, string) map[string]string {
	return func(a map[string]string, _ string) map[string]string {
		out := make(map[string]string, len(keys))
		for _, k := range keys {
			out[k] = a[k]
		}
		return out
	}
}

func usageOnly(_ map[string]string, usage string) map[string]string {
	return map[string]string{"usagetype": usage}
}

func usagePrefixed(prefixes ...string) func(map[string]string, string) bool {
	return func(_ map[string]string, usage string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(usage, p) {
				return true
			}
		}
		return false
	}
}

func usageIn(values ...string) func(map[string]string, string) bool {
	return func(_ map[string]string, usage string) bool { return slices.Contains(values, usage) }
}

// buildRules is the precise selection applied to slim offers. It documents,
// per service, exactly which products enter the snapshot.
var buildRules = map[string][]rule{
	"AmazonEC2": {
		{
			// On-demand Linux and Windows instance hours, shared tenancy.
			family: "Compute Instance",
			match: func(a map[string]string, usage string) bool {
				return strings.HasPrefix(usage, "BoxUsage:") &&
					(a["operation"] == "RunInstances" || a["operation"] == "RunInstances:0002")
			},
			attrs: pick("instanceType", "operatingSystem"),
		},
		{
			// EBS volume storage, GB-month, keyed by API volume type (gp3, io2, ...).
			family: "Storage",
			match:  usagePrefixed("EBS:VolumeUsage"),
			attrs:  pick("volumeApiName"),
		},
		{
			// Provisioned IOPS (io1/io2 incl. io2 tiers, gp3 above baseline).
			family: "System Operation",
			match:  usagePrefixed("EBS:VolumeP-IOPS"),
			attrs: func(a map[string]string, usage string) map[string]string {
				return map[string]string{"volumeApiName": a["volumeApiName"], "usagetype": usage}
			},
		},
		{
			// gp3 throughput above baseline.
			family: "Provisioned Throughput",
			match:  usagePrefixed("EBS:VolumeP-Throughput"),
			attrs:  pick("volumeApiName"),
		},
		{
			family: "NAT Gateway",
			match:  usageIn("NatGateway-Hours", "NatGateway-Bytes"),
			attrs:  usageOnly,
		},
		{
			// Surplus CPU credits for burstable instances in unlimited mode.
			family: "CPU Credits",
			match:  usagePrefixed("CPUCredits:"),
			attrs: func(a map[string]string, usage string) map[string]string {
				return map[string]string{"operatingSystem": a["operatingSystem"], "usagetype": usage}
			},
		},
	},
	"AmazonRDS": {
		{
			// Instance hours for open-source engines, Single-AZ and Multi-AZ
			// (one standby). Multi-AZ DB clusters are out of scope.
			family: "Database Instance",
			match: func(a map[string]string, usage string) bool {
				return (strings.HasPrefix(usage, "InstanceUsage:") || strings.HasPrefix(usage, "Multi-AZUsage:")) &&
					(a["deploymentOption"] == "Single-AZ" || a["deploymentOption"] == "Multi-AZ")
			},
			attrs: pick("instanceType", "databaseEngine", "deploymentOption"),
		},
		// Storage-side prices are identical across engines; AWS publishes an
		// engine-agnostic "Any" row for each, which is the one we keep.
		{family: "Database Storage", match: rdsAnyEngine, attrs: usageOnly},
		{family: "Provisioned IOPS", match: rdsAnyEngine, attrs: usageOnly},
		{family: "Provisioned Throughput", match: rdsAnyEngine, attrs: usageOnly},
		{family: "Storage Snapshot", match: func(a map[string]string, usage string) bool {
			return rdsAnyEngine(a, usage) && usage == "RDS:ChargedBackupUsage"
		}, attrs: usageOnly},
	},
	"AmazonS3": {
		// Storage, request and retrieval prices for the storage classes the
		// S3 mapper models. (Deep Archive storage is published in a separate
		// offer, AmazonS3GlacierDeepArchive, and is not included.)
		{family: "Storage", match: usageIn(
			"TimedStorage-ByteHrs", "TimedStorage-INT-FA-ByteHrs", "TimedStorage-SIA-ByteHrs",
			"TimedStorage-ZIA-ByteHrs", "TimedStorage-GIR-ByteHrs", "TimedStorage-GlacierByteHrs",
		), attrs: usageOnly},
		{family: "API Request", match: usageIn(
			"Requests-Tier1", "Requests-Tier2",
			"Requests-SIA-Tier1", "Requests-SIA-Tier2", "Retrieval-SIA",
			"Requests-ZIA-Tier1", "Requests-ZIA-Tier2", "Retrieval-ZIA",
			"Requests-GIR-Tier1", "Requests-GIR-Tier2", "Retrieval-GIR",
		), attrs: usageOnly},
	},
	"AWSLambda": {
		{
			// Paid (non free-tier) request, duration and ephemeral-storage
			// prices for x86_64 and arm64.
			family: "Serverless",
			match: func(a map[string]string, usage string) bool {
				return !strings.HasPrefix(usage, "Global-") && slices.Contains([]string{
					"AWS-Lambda-Requests", "AWS-Lambda-Requests-ARM",
					"AWS-Lambda-Duration", "AWS-Lambda-Duration-ARM",
					"AWS-Lambda-Storage-Duration", "AWS-Lambda-Storage-Duration-ARM",
				}, a["group"])
			},
			attrs: pick("group"),
		},
	},
	"AWSELB": {
		{family: "Load Balancer", match: usageIn("LoadBalancerUsage", "DataProcessing-Bytes"), attrs: usageOnly},
		{family: "Load Balancer-Application", match: usageIn("LoadBalancerUsage", "LCUUsage"), attrs: usageOnly},
		{family: "Load Balancer-Network", match: usageIn("LoadBalancerUsage", "LCUUsage"), attrs: usageOnly},
		{family: "Load Balancer-Gateway", match: usageIn("LoadBalancerUsage", "LCUUsage"), attrs: usageOnly},
	},
}

func rdsAnyEngine(a map[string]string, usage string) bool {
	return a["databaseEngine"] == "Any" &&
		(a["deploymentOption"] == "Single-AZ" || a["deploymentOption"] == "Multi-AZ") &&
		strings.HasPrefix(usage, "RDS:")
}
