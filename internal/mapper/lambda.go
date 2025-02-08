package mapper

import (
	"fmt"
	"slices"

	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

var (
	lambdaDefaultMemoryMB    = money.FromInt(128)
	lambdaFreeEphemeralMB    = money.FromInt(512)
	lambdaMillisPerSecond    = money.FromInt(1000)
	lambdaMegabytesPerGB     = money.FromInt(1024)
	lambdaDefaultEphemeralMB = lambdaFreeEphemeralMB
)

type lambdaMapper struct{}

func (lambdaMapper) ResourceType() string { return "aws_lambda_function" }

func (lambdaMapper) Doc() Doc {
	return Doc{
		Service: "AWS Lambda",
		Summary: "Lambda function (x86_64 or arm64).",
		Priced: []string{
			"Requests (usage-based)",
			"Duration in GB-seconds, tiered (usage-based)",
			"Ephemeral storage above the free 512 MB, in GB-seconds (usage-based)",
		},
		Assumptions: []string{
			"The monthly free tier (1M requests, 400,000 GB-seconds) is not deducted: it is per account",
			"Duration tiers are applied per function; AWS applies them per account and region",
			"Provisioned concurrency, SnapStart and response streaming are not priced",
		},
	}
}

func (lambdaMapper) UsageSchema() []usage.Field {
	return []usage.Field{
		{Key: "monthly_requests", Kind: usage.Number, Example: "1000000", Description: "invocations per month"},
		{Key: "request_duration_ms", Kind: usage.Number, Example: "250", Description: "average billed duration per invocation, ms"},
	}
}

func (lambdaMapper) Map(r tfplan.Resource, u usage.Values) Result {
	memMB := r.Attrs.NumberOr("memory_size", lambdaDefaultMemoryMB)
	arm := slices.Contains(r.Attrs.Strings("architectures"), "arm64")
	suffix, arch := "", "x86_64"
	if arm {
		suffix, arch = "-ARM", "arm64"
	}
	ephemeralMB := lambdaDefaultEphemeralMB
	for _, b := range r.Attrs.Blocks("ephemeral_storage") {
		ephemeralMB = b.NumberOr("size", lambdaDefaultEphemeralMB)
	}

	requests, haveRequests := u.Number("monthly_requests")
	durationMS, haveDuration := u.Number("request_duration_ms")
	// GB-seconds = requests * (ms / 1000) * (MB / 1024), computed exactly.
	seconds, _ := requests.Mul(durationMS).Div(lambdaMillisPerSecond)
	memGB, _ := memMB.Div(lambdaMegabytesPerGB)
	gbSeconds := seconds.Mul(memGB)

	comps := []Component{
		scaled(usageComponent("requests", "Requests", "million requests", requests, haveRequests, "monthly_requests",
			query("AWSLambda", r.Region, "Serverless", "group", "AWS-Lambda-Requests"+suffix)), perMillion),
		usageComponent("duration", fmt.Sprintf("Duration (%s MB, %s)", memMB, arch), "GB-seconds", gbSeconds,
			haveRequests && haveDuration, "monthly_requests and request_duration_ms",
			query("AWSLambda", r.Region, "Serverless", "group", "AWS-Lambda-Duration"+suffix)),
	}
	if extra := ephemeralMB.Sub(lambdaFreeEphemeralMB); extra.Sign() > 0 {
		extraGB, _ := extra.Div(lambdaMegabytesPerGB)
		comps = append(comps, usageComponent("ephemeral_storage", fmt.Sprintf("Ephemeral storage above 512 MB (%s MB)", ephemeralMB), "GB-seconds",
			seconds.Mul(extraGB), haveRequests && haveDuration, "monthly_requests and request_duration_ms",
			query("AWSLambda", r.Region, "Serverless", "group", "AWS-Lambda-Storage-Duration"+suffix)))
	}
	return Result{Components: comps}
}
