package tfplan

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/guneshm03/planprice/internal/tfaddr"
)

// Resource is one managed resource instance in a before or after view.
type Resource struct {
	Address       string
	ModuleAddress string
	Type          string
	Name          string
	ProviderName  string
	Action        Action
	Region        string
	RegionSource  RegionSource
	Attrs         Attrs
}

// RegionSource says how a resource's region was determined.
type RegionSource string

// Region sources, from most to least specific.
const (
	RegionFromAttribute RegionSource = "resource attribute"
	RegionFromProvider  RegionSource = "provider configuration"
	RegionFromVariable  RegionSource = "provider configuration via variable"
	RegionFromDefault   RegionSource = "default"
)

// View selects which side of each change to read.
type View int

// Views of a plan.
const (
	// After is the infrastructure the plan would leave behind.
	After View = iota
	// Before is the infrastructure that exists before the plan.
	Before
)

func (v View) String() string {
	if v == Before {
		return "before"
	}
	return "after"
}

// Resources returns the managed AWS-provider resources present in the given
// view, in plan order, with regions resolved. Data sources are excluded.
// defaultRegion is used when neither the resource nor its provider block
// determines a region.
func (p *Plan) Resources(view View, defaultRegion string, log *slog.Logger) ([]Resource, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	providers := p.providerKeys()
	var out []Resource
	for _, rc := range p.ResourceChanges {
		if rc.Mode != "" && rc.Mode != "managed" {
			continue
		}
		action, err := ParseAction(rc.Change.Actions)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rc.Address, err)
		}
		var attrs Attrs
		switch view {
		case After:
			if rc.Change.After == nil {
				continue // deleted (or forgotten) by this plan
			}
			attrs = NewAttrs(rc.Change.After, rc.Change.AfterUnknown)
		case Before:
			if rc.Change.Before == nil {
				continue // created by this plan
			}
			attrs = NewAttrs(rc.Change.Before, nil)
		}
		res := Resource{
			Address: rc.Address, ModuleAddress: rc.ModuleAddress, Type: rc.Type, Name: rc.Name,
			ProviderName: rc.ProviderName, Action: action, Attrs: attrs,
		}
		res.Region, res.RegionSource = p.resolveRegion(rc, attrs, providers, defaultRegion)
		if res.RegionSource == RegionFromDefault {
			log.Warn("region not determinable from plan; using default", "address", rc.Address, "region", defaultRegion)
		}
		out = append(out, res)
	}
	return out, nil
}

// IsAWS reports whether the resource belongs to the hashicorp/aws provider
// (or a fork published under another namespace with the same type prefix).
func (r Resource) IsAWS() bool {
	return strings.HasPrefix(r.Type, "aws_")
}

// providerKeys maps a configuration-level resource address (module path +
// type.name, no instance keys) to its provider_config_key.
func (p *Plan) providerKeys() map[string]string {
	out := map[string]string{}
	var walk func(prefix string, m ModuleConfig)
	walk = func(prefix string, m ModuleConfig) {
		for _, r := range m.Resources {
			out[prefix+r.Address] = r.ProviderConfigKey
		}
		for name, call := range m.ModuleCalls {
			walk(prefix+"module."+name+".", call.Module)
		}
	}
	walk("", p.Configuration.RootModule)
	return out
}

// resolveRegion applies, in order: the resource's own "region" attribute
// (AWS provider v6 sets it on every resource at plan time), the region
// argument of the provider block the resource uses (a constant, or a
// reference to a root variable), then defaultRegion.
func (p *Plan) resolveRegion(rc ResourceChange, attrs Attrs, providers map[string]string, defaultRegion string) (string, RegionSource) {
	if r, ok := attrs.String("region"); ok && r != "" {
		return r, RegionFromAttribute
	}
	key := providers[tfaddr.StripKeys(rc.Address)]
	if key == "" {
		key = "aws"
	}
	pc, ok := p.lookupProvider(key, rc.ModuleAddress)
	if ok {
		if expr, ok := pc.Expressions["region"]; ok {
			if s, ok := expr.ConstantValue.(string); ok && s != "" {
				return s, RegionFromProvider
			}
			for _, ref := range expr.References {
				name, isVar := strings.CutPrefix(ref, "var.")
				if !isVar {
					continue
				}
				if v, ok := p.Variables[name]; ok {
					if s, ok := v.Value.(string); ok && s != "" {
						return s, RegionFromVariable
					}
				}
			}
		}
	}
	return defaultRegion, RegionFromDefault
}

// lookupProvider finds a provider config by key. Terraform gives providers
// declared inside a module keys prefixed with the module path
// ("module.x:aws"), and resources reference them by that full key; a module
// that inherits its caller's provider references the caller's key ("aws").
// As a fallback for hand-written plans, an unprefixed key is also tried with
// the resource's module path.
func (p *Plan) lookupProvider(key, moduleAddr string) (ProviderConfig, bool) {
	if moduleAddr != "" && !strings.Contains(key, ":") {
		if pc, ok := p.Configuration.ProviderConfig[tfaddr.StripKeys(moduleAddr)+":"+key]; ok {
			return pc, true
		}
	}
	pc, ok := p.Configuration.ProviderConfig[key]
	return pc, ok
}
