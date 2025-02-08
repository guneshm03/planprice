package mapper

import (
	"fmt"

	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

type natGatewayMapper struct{}

func (natGatewayMapper) ResourceType() string { return "aws_nat_gateway" }

func (natGatewayMapper) Doc() Doc {
	return Doc{
		Service: "Amazon VPC",
		Summary: "NAT gateway (public or private connectivity).",
		Priced:  []string{"Gateway hours", "Data processed per GB (usage-based)"},
		Assumptions: []string{
			"Data transfer out to the internet is billed separately by AWS and is not priced",
			"The public IPv4 address (aws_eip) is a separate resource and is not priced",
		},
	}
}

func (natGatewayMapper) UsageSchema() []usage.Field {
	return []usage.Field{
		monthlyHoursField,
		{Key: "monthly_data_processed_gb", Kind: usage.Number, Example: "100", Description: "GB processed by the gateway per month"},
	}
}

func (natGatewayMapper) Map(r tfplan.Resource, u usage.Values) Result {
	gb, have := u.Number("monthly_data_processed_gb")
	return Result{Components: []Component{
		ready("hours", "NAT gateway", "hours", monthlyHours(u),
			query("AmazonEC2", r.Region, "NAT Gateway", "usagetype", "NatGateway-Hours")),
		usageComponent("data", "Data processed", "GB", gb, have, "monthly_data_processed_gb",
			query("AmazonEC2", r.Region, "NAT Gateway", "usagetype", "NatGateway-Bytes")),
	}}
}

// lbFamilies maps aws_lb load_balancer_type to its price-list family and the
// name AWS uses for its capacity unit.
var lbFamilies = map[string]struct{ family, unit, label string }{
	"application": {"Load Balancer-Application", "LCU-hours", "Application load balancer"},
	"network":     {"Load Balancer-Network", "NLCU-hours", "Network load balancer"},
	"gateway":     {"Load Balancer-Gateway", "GLCU-hours", "Gateway load balancer"},
}

type lbMapper struct{ resourceType string }

func (m lbMapper) ResourceType() string { return m.resourceType }

func (m lbMapper) Doc() Doc {
	summary := "Application, Network or Gateway Load Balancer."
	if m.resourceType == "aws_alb" {
		summary = "Alias of aws_lb (legacy name); priced identically."
	}
	return Doc{
		Service: "Elastic Load Balancing",
		Summary: summary,
		Priced:  []string{"Load balancer hours", "Capacity unit hours (LCU / NLCU / GLCU, usage-based)"},
		Assumptions: []string{
			"load_balancer_type defaults to application",
			"Capacity units are an hourly average you supply; planprice does not derive them from traffic",
		},
	}
}

func (lbMapper) UsageSchema() []usage.Field {
	return []usage.Field{
		monthlyHoursField,
		{Key: "average_capacity_units", Kind: usage.Number, Example: "1", Description: "average LCUs (ALB), NLCUs (NLB) or GLCUs (GWLB) per hour"},
	}
}

func (lbMapper) Map(r tfplan.Resource, u usage.Values) Result {
	lbType := r.Attrs.StringOr("load_balancer_type", "application")
	f, ok := lbFamilies[lbType]
	if !ok {
		return Result{Components: []Component{unsupportedComponent("hours", "Load balancer", "unknown load_balancer_type "+lbType)}}
	}
	hours := monthlyHours(u)
	units, have := u.Number("average_capacity_units")
	return Result{Components: []Component{
		ready("hours", f.label, "hours", hours, query("AWSELB", r.Region, f.family, "usagetype", "LoadBalancerUsage")),
		usageComponent("capacity_units", fmt.Sprintf("Capacity units (%s)", f.unit), f.unit, units.Mul(hours), have, "average_capacity_units",
			query("AWSELB", r.Region, f.family, "usagetype", "LCUUsage")),
	}}
}

type classicELBMapper struct{}

func (classicELBMapper) ResourceType() string { return "aws_elb" }

func (classicELBMapper) Doc() Doc {
	return Doc{
		Service:     "Elastic Load Balancing",
		Summary:     "Classic Load Balancer (previous generation).",
		Priced:      []string{"Load balancer hours", "Data processed per GB (usage-based)"},
		Assumptions: []string{"Data transfer out is not priced"},
	}
}

func (classicELBMapper) UsageSchema() []usage.Field {
	return []usage.Field{
		monthlyHoursField,
		{Key: "monthly_data_processed_gb", Kind: usage.Number, Example: "100", Description: "GB processed by the load balancer per month"},
	}
}

func (classicELBMapper) Map(r tfplan.Resource, u usage.Values) Result {
	gb, have := u.Number("monthly_data_processed_gb")
	return Result{Components: []Component{
		ready("hours", "Classic load balancer", "hours", monthlyHours(u),
			query("AWSELB", r.Region, "Load Balancer", "usagetype", "LoadBalancerUsage")),
		usageComponent("data", "Data processed", "GB", gb, have, "monthly_data_processed_gb",
			query("AWSELB", r.Region, "Load Balancer", "usagetype", "DataProcessing-Bytes")),
	}}
}
