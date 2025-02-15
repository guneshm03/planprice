// Package usage reads usage files: YAML documents that supply the figures a
// Terraform plan cannot know, such as Lambda request volume, S3 storage or
// NAT gateway traffic.
//
//	version: 1
//	defaults:                      # by resource type, lowest precedence
//	  aws_lambda_function:
//	    request_duration_ms: 200
//	resource_usage:                # by address; [*] matches any instance key
//	  aws_lambda_function.workers[*]:
//	    monthly_requests: 500000
//	  module.data.aws_s3_bucket.assets:
//	    storage_classes:
//	      standard:
//	        storage_gb: 1500
//
// For a resource, values are merged key by key: type defaults, then the
// wildcard entry (the longest matching pattern), then the exact address.
package usage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/tfaddr"
)

// SupportedVersion is the usage-file schema version.
const SupportedVersion = 1

// File is a parsed usage file.
type File struct {
	Version  int
	defaults map[string]map[string]any
	exact    map[string]map[string]any
	patterns []pattern
}

type pattern struct {
	text   string
	values map[string]any
}

type rawFile struct {
	Version       int                       `yaml:"version"`
	Defaults      map[string]map[string]any `yaml:"defaults"`
	ResourceUsage map[string]map[string]any `yaml:"resource_usage"`
}

// Parse decodes a usage file. Unknown top-level keys are rejected.
func Parse(r io.Reader) (*File, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var raw rawFile
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return &File{Version: SupportedVersion}, nil
		}
		return nil, fmt.Errorf("decode usage file: %w", err)
	}
	if raw.Version != SupportedVersion {
		return nil, fmt.Errorf("usage file version %d is not supported (want %d)", raw.Version, SupportedVersion)
	}
	f := &File{Version: raw.Version, defaults: map[string]map[string]any{}, exact: map[string]map[string]any{}}
	for typ, vals := range raw.Defaults {
		if !strings.HasPrefix(typ, "aws_") || strings.Contains(typ, ".") {
			return nil, fmt.Errorf("defaults: %q is not a resource type", typ)
		}
		f.defaults[typ] = orEmpty(vals)
	}
	for addr, vals := range raw.ResourceUsage {
		if ResourceTypeOf(addr) == "" {
			return nil, fmt.Errorf("resource_usage: %q is not a resource address", addr)
		}
		if strings.Contains(addr, "[*]") {
			f.patterns = append(f.patterns, pattern{text: addr, values: orEmpty(vals)})
		} else {
			f.exact[addr] = orEmpty(vals)
		}
	}
	// Longest pattern first: it is the most specific.
	sort.Slice(f.patterns, func(i, j int) bool {
		if len(f.patterns[i].text) != len(f.patterns[j].text) {
			return len(f.patterns[i].text) > len(f.patterns[j].text)
		}
		return f.patterns[i].text < f.patterns[j].text
	})
	return f, nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Load reads a usage file from disk.
func Load(path string) (*File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	f, err := Parse(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// For returns the merged usage values for one resource instance. A nil File
// yields empty values.
func (f *File) For(address, resourceType string) Values {
	merged := map[string]any{}
	var sources []string
	if f == nil {
		return Values{m: merged}
	}
	if d, ok := f.defaults[resourceType]; ok {
		merge(merged, d)
		sources = append(sources, "defaults."+resourceType)
	}
	for _, p := range f.patterns {
		if MatchPattern(p.text, address) {
			merge(merged, p.values)
			sources = append(sources, p.text)
			break
		}
	}
	if e, ok := f.exact[address]; ok {
		merge(merged, e)
		sources = append(sources, address)
	}
	return Values{m: merged, sources: sources}
}

func merge(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}

// Entries lists every address and pattern key in the file, sorted.
func (f *File) Entries() []string {
	if f == nil {
		return nil
	}
	out := make([]string, 0, len(f.exact)+len(f.patterns))
	for a := range f.exact {
		out = append(out, a)
	}
	for _, p := range f.patterns {
		out = append(out, p.text)
	}
	sort.Strings(out)
	return out
}

// MatchPattern reports whether addr matches pattern, where each "[*]" in the
// pattern matches exactly one instance key ("[0]", "[\"a\"]"). Patterns
// without "[*]" must equal addr. Runs in O(len(pattern) + len(addr)).
func MatchPattern(pattern, addr string) bool {
	for {
		before, after, found := strings.Cut(pattern, "[*]")
		if !found {
			return pattern == addr
		}
		if !strings.HasPrefix(addr, before) {
			return false
		}
		rest := addr[len(before):]
		end := tfaddr.KeyEnd(rest)
		if end < 0 {
			return false
		}
		pattern, addr = after, rest[end:]
	}
}

// ResourceTypeOf extracts the resource type from an address such as
// `module.web.aws_instance.web[0]` ("aws_instance"). It returns "" when the
// address has no aws_* type segment.
func ResourceTypeOf(addr string) string {
	segs := tfaddr.Segments(addr)
	for i := len(segs) - 2; i >= 0; i-- {
		if strings.HasPrefix(segs[i], "aws_") && (i == 0 || segs[i-1] != "module") {
			return segs[i]
		}
	}
	return ""
}

// Values is the merged usage for one resource.
type Values struct {
	m       map[string]any
	sources []string
}

// NewValues wraps a map; used by tests and by group access.
func NewValues(m map[string]any) Values {
	if m == nil {
		m = map[string]any{}
	}
	return Values{m: m}
}

// Empty reports whether no usage was supplied.
func (v Values) Empty() bool { return len(v.m) == 0 }

// Sources lists the usage-file entries that contributed, lowest precedence
// first.
func (v Values) Sources() []string { return slices.Clone(v.sources) }

// Number returns a numeric usage value. ok is false if the key is absent;
// a present but non-numeric value is reported by Validate, and treated as
// absent here.
func (v Values) Number(key string) (money.Decimal, bool) {
	return toDecimal(v.m[key])
}

// String returns a string usage value.
func (v Values) String(key string) (string, bool) {
	s, ok := v.m[key].(string)
	return s, ok
}

// Group returns a nested group (such as one S3 storage class) as Values.
func (v Values) Group(key string) (Values, bool) {
	m, ok := v.m[key].(map[string]any)
	if !ok {
		return Values{}, false
	}
	return NewValues(m), true
}

// GroupKeys lists the keys of a nested map value, sorted.
func (v Values) GroupKeys(key string) []string {
	m, _ := v.m[key].(map[string]any)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func toDecimal(v any) (money.Decimal, bool) {
	switch n := v.(type) {
	case int:
		return money.FromInt(int64(n)), true
	case int64:
		return money.FromInt(n), true
	case uint64:
		d, err := money.Parse(fmt.Sprint(n))
		return d, err == nil
	case float64:
		d, err := money.FromFloat(n)
		return d, err == nil
	default:
		return money.Zero, false
	}
}
