// Package tfplan reads the JSON form of a Terraform plan (`terraform show
// -json <planfile>`) and exposes the resources it would leave behind (the
// "after" view) and the resources that existed before it (the "before"
// view), each with a resolved AWS region.
//
// Only the parts of the plan format that cost estimation needs are decoded:
// resource_changes, variables and the provider configuration. See
// https://developer.hashicorp.com/terraform/internals/json-format.
package tfplan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Plan is the decoded subset of a plan JSON document.
type Plan struct {
	FormatVersion    string              `json:"format_version"`
	TerraformVersion string              `json:"terraform_version"`
	Variables        map[string]Variable `json:"variables"`
	ResourceChanges  []ResourceChange    `json:"resource_changes"`
	Configuration    Configuration       `json:"configuration"`
}

// Variable is a root-module input variable value.
type Variable struct {
	Value any `json:"value"`
}

// ResourceChange is one entry of resource_changes.
type ResourceChange struct {
	Address       string          `json:"address"`
	ModuleAddress string          `json:"module_address"`
	Mode          string          `json:"mode"`
	Type          string          `json:"type"`
	Name          string          `json:"name"`
	Index         json.RawMessage `json:"index"`
	ProviderName  string          `json:"provider_name"`
	Change        Change          `json:"change"`
}

// Change is the before/after pair for a resource.
type Change struct {
	Actions      []string `json:"actions"`
	Before       any      `json:"before"`
	After        any      `json:"after"`
	AfterUnknown any      `json:"after_unknown"`
}

// Configuration is the decoded subset of the plan's "configuration" block.
type Configuration struct {
	ProviderConfig map[string]ProviderConfig `json:"provider_config"`
	RootModule     ModuleConfig              `json:"root_module"`
}

// ProviderConfig is one provider block (possibly aliased).
type ProviderConfig struct {
	Name        string                `json:"name"`
	FullName    string                `json:"full_name"`
	Alias       string                `json:"alias"`
	ModuleAddr  string                `json:"module_address"`
	Expressions map[string]Expression `json:"expressions"`
}

// Expression is a configuration expression: either a constant or a list of
// references (e.g. "var.region").
type Expression struct {
	ConstantValue any      `json:"constant_value"`
	References    []string `json:"references"`
}

// ModuleConfig is a module's configuration: its resources and child calls.
type ModuleConfig struct {
	Resources   []ResourceConfig      `json:"resources"`
	ModuleCalls map[string]ModuleCall `json:"module_calls"`
}

// ResourceConfig is a resource block in configuration.
type ResourceConfig struct {
	Address           string `json:"address"`
	Mode              string `json:"mode"`
	Type              string `json:"type"`
	ProviderConfigKey string `json:"provider_config_key"`
}

// ModuleCall is a module block and the module it instantiates.
type ModuleCall struct {
	Module ModuleConfig `json:"module"`
}

// MaxPlanBytes bounds how much input Parse reads (256 MiB). Real plans for
// thousands of resources are a few tens of MiB.
const MaxPlanBytes = 256 << 20

// Parse decodes a plan JSON document. It rejects documents that are not
// plans (no format_version) and format versions with an unknown major.
func Parse(r io.Reader) (*Plan, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxPlanBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	if len(data) > MaxPlanBytes {
		return nil, fmt.Errorf("plan JSON exceeds %d bytes", MaxPlanBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var p Plan
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode plan JSON: %w", err)
	}
	if p.FormatVersion == "" {
		return nil, errors.New("not a Terraform plan: missing format_version (use `terraform show -json <planfile>`)")
	}
	if major, _, _ := strings.Cut(p.FormatVersion, "."); major != "1" && major != "0" {
		return nil, fmt.Errorf("unsupported plan format_version %q (want 1.x)", p.FormatVersion)
	}
	for i, rc := range p.ResourceChanges {
		if rc.Address == "" || rc.Type == "" {
			return nil, fmt.Errorf("resource_changes[%d]: address and type are required", i)
		}
		if _, err := ParseAction(rc.Change.Actions); err != nil {
			return nil, fmt.Errorf("%s: %w", rc.Address, err)
		}
	}
	return &p, nil
}

// ParseFile reads and parses a plan JSON file.
func ParseFile(path string) (*Plan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	p, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Action is the normalised plan action for a resource.
type Action string

// Plan actions. Replace covers both create-before-destroy and
// destroy-before-create orderings.
const (
	ActionNoOp    Action = "no-op"
	ActionCreate  Action = "create"
	ActionRead    Action = "read"
	ActionUpdate  Action = "update"
	ActionDelete  Action = "delete"
	ActionReplace Action = "replace"
)

// ParseAction normalises a Terraform actions list.
func ParseAction(actions []string) (Action, error) {
	switch strings.Join(actions, ",") {
	case "no-op":
		return ActionNoOp, nil
	case "create":
		return ActionCreate, nil
	case "read":
		return ActionRead, nil
	case "update":
		return ActionUpdate, nil
	case "delete":
		return ActionDelete, nil
	case "delete,create", "create,delete":
		return ActionReplace, nil
	case "forget":
		// Terraform 1.7+ "removed" blocks: the object leaves state but keeps
		// running, so for costing it is a delete from the plan's view.
		return ActionDelete, nil
	default:
		return "", fmt.Errorf("unknown plan actions %v", actions)
	}
}
