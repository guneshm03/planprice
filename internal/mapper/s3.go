package mapper

import (
	"fmt"

	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

// s3Class maps a usage-file storage class to the usage types AWS uses for
// its storage, request and retrieval prices. Empty fields mean "no such
// charge for this class" in planprice's model.
type s3Class struct {
	name      string
	storage   string
	tier1     string
	tier2     string
	retrieval string
}

var s3Classes = []s3Class{
	{"standard", "TimedStorage-ByteHrs", "Requests-Tier1", "Requests-Tier2", ""},
	{"intelligent_tiering", "TimedStorage-INT-FA-ByteHrs", "Requests-Tier1", "Requests-Tier2", ""},
	{"standard_infrequent_access", "TimedStorage-SIA-ByteHrs", "Requests-SIA-Tier1", "Requests-SIA-Tier2", "Retrieval-SIA"},
	{"one_zone_infrequent_access", "TimedStorage-ZIA-ByteHrs", "Requests-ZIA-Tier1", "Requests-ZIA-Tier2", "Retrieval-ZIA"},
	{"glacier_instant_retrieval", "TimedStorage-GIR-ByteHrs", "Requests-GIR-Tier1", "Requests-GIR-Tier2", "Retrieval-GIR"},
	{"glacier_flexible_retrieval", "TimedStorage-GlacierByteHrs", "", "", ""},
}

func s3ClassNames() []string {
	out := make([]string, len(s3Classes))
	for i, c := range s3Classes {
		out[i] = c.name
	}
	return out
}

type s3BucketMapper struct{}

func (s3BucketMapper) ResourceType() string { return "aws_s3_bucket" }

func (s3BucketMapper) Doc() Doc {
	return Doc{
		Service: "Amazon S3",
		Summary: "S3 bucket; every S3 charge depends on usage.",
		Priced: []string{
			"Storage per GB-month for each storage class in the usage file (S3 Standard is tiered)",
			"Tier 1 (PUT/COPY/POST/LIST) and Tier 2 (GET and other) requests",
			"Retrieval per GB for Infrequent Access and Glacier Instant Retrieval",
		},
		Assumptions: []string{
			"Volume tiers are applied per bucket; AWS applies them per account",
			"Intelligent-Tiering storage is priced at the Frequent Access tier; monitoring fees are not priced",
			"Glacier Flexible Retrieval requests and restores, Glacier Deep Archive (a separate AWS offer) and data transfer are not priced",
		},
	}
}

func (s3BucketMapper) UsageSchema() []usage.Field {
	return []usage.Field{{
		Key: "storage_classes", Kind: usage.GroupMap, Enum: s3ClassNames(),
		Description: "usage per storage class",
		Fields: []usage.Field{
			{Key: "storage_gb", Kind: usage.Number, Description: "average stored GB over the month"},
			{Key: "monthly_tier_1_requests", Kind: usage.Number, Description: "PUT, COPY, POST and LIST requests"},
			{Key: "monthly_tier_2_requests", Kind: usage.Number, Description: "GET and all other requests"},
			{Key: "monthly_retrieval_gb", Kind: usage.Number, Description: "GB retrieved (IA and Glacier Instant Retrieval)"},
		},
	}}
}

func (s3BucketMapper) Map(r tfplan.Resource, u usage.Values) Result {
	classes := u.GroupKeys("storage_classes")
	if len(classes) == 0 {
		return Result{Components: []Component{usageComponent("standard.storage", "Storage (standard)", "GB-months", money.Zero, false,
			"storage_classes.standard.storage_gb", query("AmazonS3", r.Region, "Storage", "usagetype", "TimedStorage-ByteHrs"))}}
	}
	var res Result
	group, _ := u.Group("storage_classes")
	for _, c := range s3Classes {
		cu, ok := group.Group(c.name)
		if !ok {
			continue
		}
		var comps []Component
		add := func(key, label, unit, usageKey, usageType, family string, scale money.Decimal) {
			if usageType == "" {
				return
			}
			qty, have := cu.Number(usageKey)
			if !have && key != "storage" {
				return // optional dimensions are listed only when supplied
			}
			comp := usageComponent(key, label, unit, qty, have,
				fmt.Sprintf("storage_classes.%s.%s", c.name, usageKey), query("AmazonS3", r.Region, family, "usagetype", usageType))
			if !scale.IsZero() {
				comp = scaled(comp, scale)
			}
			comps = append(comps, comp)
		}
		add("storage", "Storage", "GB-months", "storage_gb", c.storage, "Storage", money.Zero)
		add("tier1", "PUT, COPY, POST, LIST requests", "thousand requests", "monthly_tier_1_requests", c.tier1, "API Request", perThousand)
		add("tier2", "GET and other requests", "thousand requests", "monthly_tier_2_requests", c.tier2, "API Request", perThousand)
		add("retrieval", "Data retrieval", "GB", "monthly_retrieval_gb", c.retrieval, "API Request", money.Zero)
		res.Groups = append(res.Groups, Group{Key: c.name, Name: c.name, Components: comps})
	}
	return res
}
