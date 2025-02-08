package mapper

import (
	"fmt"
	"strings"

	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

type instanceMapper struct{}

func (instanceMapper) ResourceType() string { return "aws_instance" }

func (instanceMapper) Doc() Doc {
	return Doc{
		Service: "Amazon EC2",
		Summary: "On-demand EC2 instance with its root and attached EBS volumes.",
		Priced: []string{
			"Instance hours (Linux or Windows, shared tenancy, on-demand)",
			"root_block_device and each ebs_block_device, priced as EBS volumes",
			"Surplus CPU credits for burstable instances in unlimited mode (usage-based)",
		},
		Assumptions: []string{
			"Operating system is Linux unless usage sets operating_system: windows",
			"A root volume with unknown size is priced as 8 GB (typical AMI size)",
			"Dedicated tenancy, Reserved Instances, Savings Plans and Spot are not priced",
		},
	}
}

func (instanceMapper) UsageSchema() []usage.Field {
	return []usage.Field{
		{Key: "operating_system", Kind: usage.Enum, Enum: []string{"linux", "windows"}, Description: "AMI operating system (default linux)"},
		monthlyHoursField,
		{Key: "monthly_cpu_credit_hours", Kind: usage.Number, Example: "0", Description: "surplus vCPU-hours of CPU credits (unlimited burstable instances)"},
	}
}

func (instanceMapper) Map(r tfplan.Resource, u usage.Values) Result {
	var res Result
	os := "Linux"
	if v, ok := u.String("operating_system"); ok && v == "windows" {
		os = "Windows"
	}
	instanceType, known := r.Attrs.String("instance_type")
	switch {
	case !known || instanceType == "":
		res.Components = append(res.Components, unknownComponent("instance", "Instance usage", "hours", "instance_type is unknown"))
	case isDedicated(r.Attrs):
		res.Components = append(res.Components, unsupportedComponent("instance", "Instance usage ("+instanceType+")", "dedicated/host tenancy is not in the price snapshot"))
	default:
		res.Components = append(res.Components, ready("instance",
			fmt.Sprintf("Instance usage (%s, on-demand, %s)", os, instanceType), "hours", monthlyHours(u),
			query("AmazonEC2", r.Region, "Compute Instance", "instanceType", instanceType, "operatingSystem", os)))
		if family, burst := burstableFamily(instanceType); burst && cpuCreditsUnlimited(r.Attrs, family) {
			credits, have := u.Number("monthly_cpu_credit_hours")
			res.Components = append(res.Components, usageComponent("cpu_credits",
				fmt.Sprintf("CPU credits (%s unlimited)", family), "vCPU-hours", credits, have, "monthly_cpu_credit_hours",
				query("AmazonEC2", r.Region, "CPU Credits", "operatingSystem", os, "usagetype", "CPUCredits:"+family)))
		}
	}

	roots := r.Attrs.Blocks("root_block_device")
	if len(roots) == 0 {
		res.Groups = append(res.Groups, Group{Key: "root_block_device", Name: "root_block_device",
			Components: ebsComponents(r.Region, defaultVolumeType, defaultRootVolumeGB, money.Zero, money.Zero, false)})
		res.Notes = append(res.Notes, "root volume not configured; priced as 8 GB gp2")
	}
	for _, b := range roots {
		res.Groups = append(res.Groups, Group{Key: "root_block_device", Name: "root_block_device",
			Components: blockDeviceComponents(r.Region, b, defaultRootVolumeGB)})
	}
	for i, b := range r.Attrs.Blocks("ebs_block_device") {
		name := b.StringOr("device_name", fmt.Sprintf("#%d", i))
		res.Groups = append(res.Groups, Group{Key: "ebs_block_device:" + name, Name: "ebs_block_device (" + name + ")",
			Components: blockDeviceComponents(r.Region, b, money.Zero)})
	}
	return res
}

func blockDeviceComponents(region string, b tfplan.Attrs, defaultSize money.Decimal) []Component {
	volType := b.StringOr("volume_type", defaultVolumeType)
	size := b.NumberOr("volume_size", defaultSize)
	if size.IsZero() {
		return []Component{unknownComponent("storage", "Storage ("+volType+")", "GB-months", "volume_size is unknown (taken from the snapshot)")}
	}
	return ebsComponents(region, volType, size, b.NumberOr("iops", money.Zero), b.NumberOr("throughput", money.Zero), b.Unknown("iops"))
}

func isDedicated(a tfplan.Attrs) bool {
	t, _ := a.String("tenancy")
	return t == "dedicated" || t == "host"
}

// burstableFamily returns the instance family ("t3") for T-class types.
func burstableFamily(instanceType string) (string, bool) {
	family, _, _ := strings.Cut(instanceType, ".")
	switch family {
	case "t2", "t3", "t3a", "t4g":
		return family, true
	}
	return family, false
}

// cpuCreditsUnlimited applies the credit_specification block, falling back
// to the AWS launch default: unlimited for t3/t3a/t4g, standard for t2.
func cpuCreditsUnlimited(a tfplan.Attrs, family string) bool {
	for _, cs := range a.Blocks("credit_specification") {
		if v, ok := cs.String("cpu_credits"); ok && v != "" {
			return v == "unlimited"
		}
	}
	return family != "t2"
}
