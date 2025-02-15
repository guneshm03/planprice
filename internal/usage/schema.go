package usage

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/guneshm03/planprice/internal/tfaddr"
)

// Kind is the type of a usage field.
type Kind int

// Field kinds.
const (
	// Number is a non-negative number.
	Number Kind = iota
	// Enum is a string from a fixed set (Field.Enum).
	Enum
	// GroupMap is a map whose keys come from Field.Enum and whose values are
	// objects described by Field.Fields (e.g. S3 storage classes).
	GroupMap
)

// Field documents and validates one usage key.
type Field struct {
	Key         string
	Kind        Kind
	Description string
	Enum        []string
	Fields      []Field
	// Example is used when generating usage-file templates.
	Example string
}

// SchemaFunc returns the usage schema for a resource type, and false if the
// type is not supported (so its usage cannot be validated).
type SchemaFunc func(resourceType string) ([]Field, bool)

// Validate checks every entry of the file against the schema. Unknown keys,
// wrong types and negative numbers are errors (listed together); entries for
// unsupported resource types are returned as warnings.
func (f *File) Validate(schema SchemaFunc) (warnings []string, err error) {
	if f == nil {
		return nil, nil
	}
	var problems []string
	check := func(where, typ string, vals map[string]any) {
		fields, ok := schema(typ)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s: resource type %s has no usage-based costs; entry ignored", where, typ))
			return
		}
		problems = append(problems, validateFields(where, fields, vals)...)
	}
	for _, typ := range sortedKeys(f.defaults) {
		check("defaults."+typ, typ, f.defaults[typ])
	}
	for _, addr := range sortedKeys(f.exact) {
		check(addr, ResourceTypeOf(addr), f.exact[addr])
	}
	for _, p := range f.patterns {
		check(p.text, ResourceTypeOf(p.text), p.values)
	}
	if len(problems) > 0 {
		return warnings, fmt.Errorf("invalid usage file:\n  %s", strings.Join(problems, "\n  "))
	}
	return warnings, nil
}

func validateFields(where string, fields []Field, vals map[string]any) []string {
	var problems []string
	allowed := make([]string, 0, len(fields))
	for _, fd := range fields {
		allowed = append(allowed, fd.Key)
	}
	for _, key := range sortedKeys(vals) {
		v := vals[key]
		idx := slices.IndexFunc(fields, func(fd Field) bool { return fd.Key == key })
		if idx < 0 {
			problems = append(problems, fmt.Sprintf("%s: unknown key %q (allowed: %s)", where, key, strings.Join(allowed, ", ")))
			continue
		}
		fd := fields[idx]
		path := where + "." + key
		switch fd.Kind {
		case Number:
			d, ok := toDecimal(v)
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("%s: want a number, got %v", path, v))
			case d.Sign() < 0:
				problems = append(problems, fmt.Sprintf("%s: must not be negative", path))
			}
		case Enum:
			s, ok := v.(string)
			if !ok || !slices.Contains(fd.Enum, s) {
				problems = append(problems, fmt.Sprintf("%s: want one of %s, got %v", path, strings.Join(fd.Enum, ", "), v))
			}
		case GroupMap:
			m, ok := v.(map[string]any)
			if !ok {
				problems = append(problems, fmt.Sprintf("%s: want a map of %s", path, strings.Join(fd.Enum, ", ")))
				continue
			}
			for _, g := range sortedKeys(m) {
				if !slices.Contains(fd.Enum, g) {
					problems = append(problems, fmt.Sprintf("%s: unknown group %q (allowed: %s)", path, g, strings.Join(fd.Enum, ", ")))
					continue
				}
				sub, ok := m[g].(map[string]any)
				if !ok {
					problems = append(problems, fmt.Sprintf("%s.%s: want a map", path, g))
					continue
				}
				problems = append(problems, validateFields(path+"."+g, fd.Fields, sub)...)
			}
		}
	}
	return problems
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TemplateEntry is one resource to include in a generated usage template.
type TemplateEntry struct {
	Address string
	Type    string
}

// Template renders a commented usage-file skeleton for the given resources.
// Every key is commented out so the file is valid (and changes nothing)
// until the user fills values in. Instances of the same resource collapse to
// one "[*]" entry.
func Template(entries []TemplateEntry, schema SchemaFunc) string {
	var b strings.Builder
	b.WriteString("# planprice usage file. Uncomment and set the values you know.\n")
	b.WriteString("# Precedence: defaults.<type> < address pattern with [*] < exact address.\n")
	b.WriteString("version: 1\nresource_usage:\n")
	seen := map[string]bool{}
	wrote := 0
	for _, e := range entries {
		fields, ok := schema(e.Type)
		if !ok || len(fields) == 0 {
			continue
		}
		key := collapseInstanceKeys(e.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		wrote++
		fmt.Fprintf(&b, "  %s:\n", key)
		writeFields(&b, fields, "    ")
	}
	if wrote == 0 {
		b.WriteString("  {}\n")
	}
	return b.String()
}

func writeFields(b *strings.Builder, fields []Field, indent string) {
	for _, fd := range fields {
		switch fd.Kind {
		case GroupMap:
			fmt.Fprintf(b, "%s# %s: # %s\n", indent, fd.Key, fd.Description)
			for _, g := range fd.Enum {
				fmt.Fprintf(b, "%s#   %s:\n", indent, g)
				for _, sub := range fd.Fields {
					fmt.Fprintf(b, "%s#     %s: %s # %s\n", indent, sub.Key, exampleOr(sub), sub.Description)
				}
			}
		default:
			fmt.Fprintf(b, "%s# %s: %s # %s\n", indent, fd.Key, exampleOr(fd), fd.Description)
		}
	}
}

func exampleOr(fd Field) string {
	if fd.Example != "" {
		return fd.Example
	}
	if fd.Kind == Enum && len(fd.Enum) > 0 {
		return fd.Enum[0]
	}
	return "0"
}

func collapseInstanceKeys(addr string) string {
	var b strings.Builder
	for i := 0; i < len(addr); i++ {
		if addr[i] == '[' {
			if end := tfaddr.KeyEnd(addr[i:]); end > 0 {
				b.WriteString("[*]")
				i += end - 1
				continue
			}
		}
		b.WriteByte(addr[i])
	}
	return b.String()
}
