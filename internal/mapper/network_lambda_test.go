package mapper_test

import (
	"slices"
	"testing"

	"github.com/guneshm03/planprice/internal/mapper"
)

// NAT gateway $0.045/h and $0.045/GB (us-east-1). Load balancers per hour:
// ALB/NLB $0.0225, GWLB $0.0125, Classic $0.025; capacity units per hour:
// LCU $0.008, NLCU $0.006; Classic data $0.008/GB.

func TestNATGateway(t *testing.T) {
	rc := price(t, res{typ: "aws_nat_gateway", attrs: map[string]any{}, usage: "monthly_data_processed_gb: 600"})
	costIs(t, rc, "hours", "32.85") // 730 * 0.045
	costIs(t, rc, "data", "27")     // 600 * 0.045
	totalIs(t, rc, "59.85")
	partial := price(t, res{typ: "aws_nat_gateway", attrs: map[string]any{}, usage: "monthly_hours: 100"})
	costIs(t, partial, "hours", "4.5")
	statusIs(t, partial, "data", mapper.StatusUsageMissing)
	eu := price(t, res{typ: "aws_nat_gateway", region: "eu-west-1", attrs: map[string]any{}})
	costIs(t, eu, "hours", "35.04") // 730 * 0.048
}

func TestLoadBalancers(t *testing.T) {
	alb := price(t, res{typ: "aws_lb", attrs: map[string]any{}, usage: "average_capacity_units: 3"})
	costIs(t, alb, "hours", "16.425")         // 730 * 0.0225 (type defaults to application)
	costIs(t, alb, "capacity_units", "17.52") // 3 * 730 * 0.008
	nlb := price(t, res{typ: "aws_lb", attrs: map[string]any{"load_balancer_type": "network"}, usage: "average_capacity_units: 2"})
	costIs(t, nlb, "capacity_units", "8.76") // 2 * 730 * 0.006
	gwlb := price(t, res{typ: "aws_lb", attrs: map[string]any{"load_balancer_type": "gateway"}})
	costIs(t, gwlb, "hours", "9.125") // 730 * 0.0125
	statusIs(t, gwlb, "capacity_units", mapper.StatusUsageMissing)
	alias := price(t, res{typ: "aws_alb", attrs: map[string]any{"load_balancer_type": "application"}})
	costIs(t, alias, "hours", "16.425")
	bad := price(t, res{typ: "aws_lb", attrs: map[string]any{"load_balancer_type": "quantum"}})
	statusIs(t, bad, "hours", mapper.StatusUnsupported)
	classic := price(t, res{typ: "aws_elb", attrs: map[string]any{}, usage: "monthly_data_processed_gb: 100"})
	costIs(t, classic, "hours", "18.25") // 730 * 0.025
	costIs(t, classic, "data", "0.8")    // 100 * 0.008
}

// Lambda (us-east-1): requests $0.20 per 1M (x86 and arm); duration x86
// $0.0000166667 / $0.000015 / $0.0000133334 per GB-s (bands 0-6B, 6-15B,
// 15B+); arm $0.0000133334 first band; ephemeral storage $0.0000000309/GB-s.

func TestLambdaX86(t *testing.T) {
	rc := price(t, res{typ: "aws_lambda_function", attrs: map[string]any{"memory_size": 512, "architectures": []any{"x86_64"}},
		usage: "monthly_requests: 4000000\nrequest_duration_ms: 180"})
	costIs(t, rc, "requests", "0.8") // 4 * 0.20
	// 4M * 0.18 s * 0.5 GB = 360,000 GB-s * 0.0000166667
	costIs(t, rc, "duration", "6.000012")
	totalIs(t, rc, "6.800012")
	if hasComponent(rc, "ephemeral_storage") {
		t.Error("512 MB ephemeral storage is free")
	}
}

func TestLambdaArmAndEphemeralStorage(t *testing.T) {
	arm := price(t, res{typ: "aws_lambda_function", attrs: map[string]any{"memory_size": 1024, "architectures": []any{"arm64"}},
		usage: "monthly_requests: 4000000\nrequest_duration_ms: 180"})
	costIs(t, arm, "duration", "9.600048") // 720,000 GB-s * 0.0000133334
	worker := price(t, res{typ: "aws_lambda_function", attrs: map[string]any{"memory_size": 2048, "architectures": []any{"arm64"},
		"ephemeral_storage": []any{map[string]any{"size": 2048}}},
		usage: "monthly_requests: 250000\nrequest_duration_ms: 2400"})
	// 250k * 2.4 s = 600,000 s; extra 1.5 GB -> 900,000 GB-s * 0.0000000309
	costIs(t, worker, "ephemeral_storage", "0.02781")
	costIs(t, worker, "duration", "16.00008") // 600,000 s * 2 GB * 0.0000133334
	costIs(t, worker, "requests", "0.05")     // 0.25 * 0.20
}

func TestLambdaTierCrossingAndDefaults(t *testing.T) {
	// 7M requests * 1,000 s * 1 GB = 7e9 GB-s:
	// 6e9 * 0.0000166667 + 1e9 * 0.000015 = 100,000.2 + 15,000.
	rc := price(t, res{typ: "aws_lambda_function", attrs: map[string]any{"memory_size": 1024},
		usage: "monthly_requests: 7000000\nrequest_duration_ms: 1000000"})
	costIs(t, rc, "duration", "115000.2")
	defaults := price(t, res{typ: "aws_lambda_function", attrs: map[string]any{},
		usage: "monthly_requests: 1000000\nrequest_duration_ms: 1000"})
	// default 128 MB: 1M * 1 s * 0.125 GB = 125,000 GB-s * 0.0000166667
	costIs(t, defaults, "duration", "2.0833375")
	onlyRequests := price(t, res{typ: "aws_lambda_function", attrs: map[string]any{}, usage: "monthly_requests: 1000000"})
	costIs(t, onlyRequests, "requests", "0.2")
	statusIs(t, onlyRequests, "duration", mapper.StatusUsageMissing)
}

func TestRegistry(t *testing.T) {
	reg := mapper.NewRegistry()
	want := []string{"aws_alb", "aws_db_instance", "aws_ebs_volume", "aws_elb", "aws_instance", "aws_lambda_function", "aws_lb", "aws_nat_gateway", "aws_s3_bucket"}
	if got := reg.Types(); !slices.Equal(got, want) {
		t.Fatalf("types %v", got)
	}
	for _, typ := range reg.Types() {
		m, ok := reg.Lookup(typ)
		if !ok || m.ResourceType() != typ {
			t.Fatalf("lookup %s", typ)
		}
		d := m.Doc()
		if d.Service == "" || d.Summary == "" || len(d.Priced) == 0 || len(d.Assumptions) == 0 {
			t.Errorf("%s: incomplete docs", typ)
		}
		if _, ok := reg.UsageSchema(typ); !ok {
			t.Errorf("%s: no schema", typ)
		}
	}
	if _, ok := reg.UsageSchema("aws_vpc"); ok {
		t.Error("schema for unsupported type")
	}
	free := mapper.FreeTypes()
	if !slices.IsSorted(free) {
		t.Fatal("freeTypes must stay sorted: IsFree uses binary search")
	}
	if !mapper.IsFree("aws_vpc") || mapper.IsFree("aws_eip") || mapper.IsFree("aws_instance") {
		t.Error("IsFree")
	}
	for _, typ := range free {
		if _, priced := reg.Lookup(typ); priced {
			t.Errorf("%s is both free and priced", typ)
		}
	}
}
