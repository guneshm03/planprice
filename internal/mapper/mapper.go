// Package mapper turns Terraform resources into priced cost components.
//
// Each supported resource type has a Mapper that reads the resource's planned
// attributes plus any usage-file values and returns cost Components: a
// quantity per month and a pricing.Query naming the price that applies. The
// mapper does not look prices up; the estimate package does that, so mappers
// stay pure functions of (attributes, usage) and are easy to test.
package mapper

import (
	"slices"
	"sort"

	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/pricing"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

// HoursPerMonth is the AWS billing convention for an average month
// (365 days * 24 hours / 12 months).
const HoursPerMonth = 730

var hoursPerMonth = money.FromInt(HoursPerMonth)

// Status says whether a component can be priced and, if not, why.
type Status string

// Component statuses. The estimate package sets Priced or PriceNotFound
// after lookup; mappers set the others.
const (
	// StatusReady means the component has a quantity and a query.
	StatusReady Status = "ready"
	// StatusPriced means a price was found and the cost computed.
	StatusPriced Status = "priced"
	// StatusUsageMissing means the cost depends on usage the user has not
	// supplied; it is excluded from totals and listed separately.
	StatusUsageMissing Status = "usage-missing"
	// StatusUnknown means a price-relevant attribute is unknown until apply.
	StatusUnknown Status = "unknown-attribute"
	// StatusUnsupported means the configuration is outside what planprice
	// prices (e.g. dedicated tenancy); the reason says what.
	StatusUnsupported Status = "unsupported"
	// StatusPriceNotFound means the snapshot has no matching price.
	StatusPriceNotFound Status = "price-not-found"
)

// Component is one billable dimension of a resource.
type Component struct {
	// Key is stable across plans (e.g. "instance", "storage") and is what
	// cost diffs match on; Name is for display and may include attribute
	// values such as the instance type.
	Key  string
	Name string
	// Unit names Quantity's display unit. The quantity priced against the
	// snapshot is Quantity * Scale (Scale zero means 1), which lets a
	// component show "million requests" while the price list charges per request.
	Unit       string
	Quantity   money.Decimal
	Scale      money.Decimal
	UsageBased bool
	Status     Status
	Reason     string
	Query      pricing.Query
}

// Group is a named set of components inside a resource, such as an EC2
// instance's root_block_device.
type Group struct {
	Key        string
	Name       string
	Components []Component
}

// Result is a mapper's output for one resource.
type Result struct {
	Components []Component
	Groups     []Group
	Notes      []string
}

// Doc describes a mapper for `planprice resources` and docs/RESOURCES.md.
type Doc struct {
	Service     string
	Summary     string
	Priced      []string
	Assumptions []string
}

// Mapper prices one Terraform resource type.
type Mapper interface {
	ResourceType() string
	Doc() Doc
	UsageSchema() []usage.Field
	Map(r tfplan.Resource, u usage.Values) Result
}

// Registry holds the mappers by resource type.
type Registry struct {
	byType map[string]Mapper
}

// NewRegistry returns a registry with every built-in mapper.
func NewRegistry() *Registry {
	reg := &Registry{byType: map[string]Mapper{}}
	for _, m := range []Mapper{
		instanceMapper{},
		ebsVolumeMapper{},
		dbInstanceMapper{},
		s3BucketMapper{},
		natGatewayMapper{},
		lbMapper{resourceType: "aws_lb"},
		lbMapper{resourceType: "aws_alb"},
		classicELBMapper{},
		lambdaMapper{},
	} {
		reg.byType[m.ResourceType()] = m
	}
	return reg
}

// Lookup returns the mapper for a resource type.
func (r *Registry) Lookup(resourceType string) (Mapper, bool) {
	m, ok := r.byType[resourceType]
	return m, ok
}

// Types lists supported resource types, sorted.
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.byType))
	for t := range r.byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// UsageSchema adapts the registry to usage.SchemaFunc.
func (r *Registry) UsageSchema(resourceType string) ([]usage.Field, bool) {
	m, ok := r.byType[resourceType]
	if !ok {
		return nil, false
	}
	return m.UsageSchema(), true
}

// freeTypes are AWS resource types that carry no charge of their own; they
// are reported as free rather than unsupported. The list is deliberately
// conservative: types with indirect or conditional costs (EIPs, VPC
// endpoints, log groups) are not here.
var freeTypes = []string{
	"aws_db_parameter_group",
	"aws_db_subnet_group",
	"aws_default_network_acl",
	"aws_default_route_table",
	"aws_default_security_group",
	"aws_iam_instance_profile",
	"aws_iam_policy",
	"aws_iam_role",
	"aws_iam_role_policy",
	"aws_iam_role_policy_attachment",
	"aws_internet_gateway",
	"aws_key_pair",
	"aws_lambda_permission",
	"aws_lb_listener",
	"aws_lb_listener_rule",
	"aws_lb_target_group",
	"aws_lb_target_group_attachment",
	"aws_network_acl",
	"aws_route",
	"aws_route_table",
	"aws_route_table_association",
	"aws_s3_bucket_lifecycle_configuration",
	"aws_s3_bucket_ownership_controls",
	"aws_s3_bucket_policy",
	"aws_s3_bucket_public_access_block",
	"aws_s3_bucket_server_side_encryption_configuration",
	"aws_s3_bucket_versioning",
	"aws_security_group",
	"aws_security_group_rule",
	"aws_subnet",
	"aws_volume_attachment",
	"aws_vpc",
	"aws_vpc_security_group_egress_rule",
	"aws_vpc_security_group_ingress_rule",
}

// IsFree reports whether a resource type is known to have no direct cost.
func IsFree(resourceType string) bool {
	_, found := slices.BinarySearch(freeTypes, resourceType)
	return found
}

// FreeTypes lists the known no-cost types, sorted.
func FreeTypes() []string { return slices.Clone(freeTypes) }

// ready builds a component that is ready to price.
func ready(key, name, unit string, qty money.Decimal, q pricing.Query) Component {
	return Component{Key: key, Name: name, Unit: unit, Quantity: qty, Status: StatusReady, Query: q}
}

// scaled returns c with a display scale (priced quantity = Quantity*scale).
func scaled(c Component, scale money.Decimal) Component {
	c.Scale = scale
	if !c.Quantity.IsZero() {
		c.Quantity, _ = c.Quantity.Div(scale)
	}
	return c
}

// PricedQuantity is the quantity in the snapshot price's unit.
func (c Component) PricedQuantity() money.Decimal {
	if c.Scale.IsZero() {
		return c.Quantity
	}
	return c.Quantity.Mul(c.Scale)
}

// UnitScale is the display-to-price unit factor (1 when unset).
func (c Component) UnitScale() money.Decimal {
	if c.Scale.IsZero() {
		return money.FromInt(1)
	}
	return c.Scale
}

var (
	perMillion  = money.FromInt(1_000_000)
	perThousand = money.FromInt(1000)
)

// usageComponent builds a usage-based component; when the usage value is
// absent the component is marked StatusUsageMissing with a hint naming the
// usage key to set.
func usageComponent(key, name, unit string, qty money.Decimal, have bool, usageKey string, q pricing.Query) Component {
	c := ready(key, name, unit, qty, q)
	c.UsageBased = true
	if !have {
		c.Status = StatusUsageMissing
		c.Quantity = money.Zero
		c.Reason = "set " + usageKey + " in the usage file"
	}
	return c
}

func unknownComponent(key, name, unit, reason string) Component {
	return Component{Key: key, Name: name, Unit: unit, Status: StatusUnknown, Reason: reason}
}

func unsupportedComponent(key, name, reason string) Component {
	return Component{Key: key, Name: name, Status: StatusUnsupported, Reason: reason}
}

func query(service, region, family string, attrs ...string) pricing.Query {
	m := make(map[string]string, len(attrs)/2)
	for i := 0; i+1 < len(attrs); i += 2 {
		m[attrs[i]] = attrs[i+1]
	}
	return pricing.Query{Service: service, Region: region, Family: family, Attrs: m}
}

// monthlyHours returns usage monthly_hours if set, else 730.
func monthlyHours(u usage.Values) money.Decimal {
	if h, ok := u.Number("monthly_hours"); ok {
		return h
	}
	return hoursPerMonth
}

var monthlyHoursField = usage.Field{
	Key: "monthly_hours", Kind: usage.Number, Example: "730",
	Description: "hours per month the resource runs (default 730)",
}
