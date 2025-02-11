package pricegen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"
)

// Services lists the AWS offer codes planprice prices from.
var Services = []string{"AmazonEC2", "AmazonRDS", "AmazonS3", "AWSLambda", "AWSELB"}

// OfferURL is the public, unauthenticated location of a region's current
// offer file in the AWS Price List Bulk API.
func OfferURL(service, region string) string {
	return fmt.Sprintf("https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/%s/current/%s/index.json", service, region)
}

// CoarseFilter returns the streaming filter used when fetching a service's
// offer file. It is deliberately looser than the build rules (it keeps whole
// product families) so rules can change without re-downloading, but tight
// enough that slim files stay small: for EC2 it drops the operating systems,
// tenancies and capacity states planprice never prices, which is >95% of SKUs.
func CoarseFilter(service string) (KeepFunc, error) {
	inRegion := func(p Product) bool { return p.Attributes["locationType"] == "AWS Region" }
	families := func(names ...string) func(Product) bool {
		return func(p Product) bool { return slices.Contains(names, p.ProductFamily) }
	}
	switch service {
	case "AmazonEC2":
		fam := families("Storage", "System Operation", "Provisioned Throughput", "NAT Gateway", "CPU Credits")
		return func(p Product) bool {
			if !inRegion(p) {
				return false
			}
			if p.ProductFamily == "Compute Instance" {
				a := p.Attributes
				return a["tenancy"] == "Shared" && a["capacitystatus"] == "Used" &&
					a["preInstalledSw"] == "NA" && a["licenseModel"] == "No License required" &&
					(a["operatingSystem"] == "Linux" || a["operatingSystem"] == "Windows")
			}
			return fam(p)
		}, nil
	case "AmazonRDS":
		fam := families("Database Storage", "Provisioned IOPS", "Provisioned Throughput", "Storage Snapshot")
		return func(p Product) bool {
			if !inRegion(p) {
				return false
			}
			if p.ProductFamily == "Database Instance" {
				a := p.Attributes
				return a["licenseModel"] == "No license required" &&
					slices.Contains([]string{"MySQL", "PostgreSQL", "MariaDB"}, a["databaseEngine"])
			}
			return fam(p)
		}, nil
	case "AmazonS3":
		fam := families("Storage", "API Request")
		return func(p Product) bool { return inRegion(p) && fam(p) }, nil
	case "AWSLambda":
		fam := families("Serverless")
		return func(p Product) bool { return inRegion(p) && fam(p) }, nil
	case "AWSELB":
		return inRegion, nil
	default:
		return nil, fmt.Errorf("no coarse filter for service %q", service)
	}
}

// Fetch streams one offer file from url through the service's coarse filter.
func Fetch(ctx context.Context, client *http.Client, service, url string) (*Offer, error) {
	keep, err := CoarseFilter(service)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	return ReadOffer(resp.Body, keep)
}

// NewHTTPClient returns a client with a generous overall timeout; the EC2
// offer file takes minutes to stream on a typical connection.
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Minute}
}

// ReadSlim reads a slim offer file written by WriteOffer, keeping everything.
func ReadSlim(r io.Reader) (*Offer, error) {
	return ReadOffer(r, func(Product) bool { return true })
}
