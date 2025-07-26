package schema

import (
	"fmt"
	"strconv"
	"strings"
)

// ApplyDefaults returns a copy of an object value with defaults filled in for
// missing properties, recursively. Non-object values are returned unchanged.
func (s *Schema) ApplyDefaults(v map[string]any) map[string]any {
	out := make(map[string]any, len(v)+len(s.Properties))
	for k, x := range v {
		out[k] = Normalize(x)
	}
	for _, name := range s.Order {
		child := s.Properties[name]
		cur, present := out[name]
		if !present && child.HasDefault {
			cur, present = deepCopy(child.Default), true
			out[name] = cur
		}
		if obj, ok := cur.(map[string]any); ok && present && child.Properties != nil {
			out[name] = child.ApplyDefaults(obj)
		}
	}
	return out
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = deepCopy(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = deepCopy(e)
		}
		return out
	}
	return v
}

// CoerceString converts a command-line string into the value type the schema
// for that property expects: integers and numbers are parsed, booleans accept
// true/false/yes/no, arrays are comma-separated. Properties without a declared
// type stay strings.
func (s *Schema) CoerceString(raw string) (any, error) {
	if len(s.Types) == 0 {
		return raw, nil
	}
	var firstErr error
	for _, t := range s.Types {
		v, err := coerceAs(t, raw, s.Items)
		if err == nil {
			return v, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

func coerceAs(t, raw string, items *Schema) (any, error) {
	switch t {
	case "string":
		return raw, nil
	case "integer":
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", raw)
		}
		return float64(n), nil
	case "number":
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return f, nil
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true", "yes", "1", "on":
			return true, nil
		case "false", "no", "0", "off":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not a boolean", raw)
	case "array":
		out := []any{}
		if strings.TrimSpace(raw) == "" {
			return out, nil
		}
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if items == nil {
				out = append(out, part)
				continue
			}
			v, err := items.CoerceString(part)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case "null":
		if raw == "null" || raw == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("%q is not null", raw)
	}
	return nil, fmt.Errorf("cannot set a %s from the command line", t)
}

// ParamDoc is a flattened, human-readable description of one top-level property.
type ParamDoc struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
	Constraints string `json:"constraints,omitempty"`
}

// Params describes the top-level properties in declaration order.
func (s *Schema) Params() []ParamDoc {
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}
	out := make([]ParamDoc, 0, len(s.Order))
	for _, name := range s.Order {
		p := s.Properties[name]
		d := ParamDoc{Name: name, Type: strings.Join(p.Types, "|"), Required: req[name], Description: p.Description}
		if d.Description == "" {
			d.Description = p.Title
		}
		if p.HasDefault {
			d.Default = p.Default
		}
		d.Constraints = p.constraintSummary()
		out = append(out, d)
	}
	return out
}

func (s *Schema) constraintSummary() string {
	var parts []string
	if len(s.Enum) > 0 {
		parts = append(parts, "one of "+formatEnum(s.Enum))
	}
	if s.Pattern != "" {
		parts = append(parts, "pattern "+s.Pattern)
	}
	if s.MinLength != nil || s.MaxLength != nil {
		lo, hi := "0", "∞"
		if s.MinLength != nil {
			lo = strconv.Itoa(*s.MinLength)
		}
		if s.MaxLength != nil {
			hi = strconv.Itoa(*s.MaxLength)
		}
		parts = append(parts, "length "+lo+".."+hi)
	}
	if s.Minimum != nil || s.Maximum != nil {
		lo, hi := "-∞", "∞"
		if s.Minimum != nil {
			lo = fmtNum(*s.Minimum)
		}
		if s.Maximum != nil {
			hi = fmtNum(*s.Maximum)
		}
		parts = append(parts, "range "+lo+".."+hi)
	}
	if s.Format != "" {
		parts = append(parts, "format "+s.Format)
	}
	return strings.Join(parts, "; ")
}
