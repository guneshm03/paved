// Package schema implements the subset of JSON Schema (draft 2020-12 keyword
// semantics) that scaffolder templates use to declare their parameters:
// type, properties, required, additionalProperties, items, enum, const,
// default, string/number/array bounds, pattern and a few formats.
//
// Unsupported keywords are rejected at compile time rather than silently
// ignored, so a template never appears to validate something it does not.
package schema

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Schema is a compiled schema node.
type Schema struct {
	Types       []string `json:"type,omitempty"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`

	Properties map[string]*Schema `json:"properties,omitempty"`
	// Order preserves the declaration order of Properties.
	Order                []string `json:"-"`
	Required             []string `json:"required,omitempty"`
	AdditionalProperties *bool    `json:"additionalProperties,omitempty"`

	Items       *Schema `json:"items,omitempty"`
	MinItems    *int    `json:"minItems,omitempty"`
	MaxItems    *int    `json:"maxItems,omitempty"`
	UniqueItems bool    `json:"uniqueItems,omitempty"`

	Enum     []any `json:"enum,omitempty"`
	Const    any   `json:"const,omitempty"`
	HasConst bool  `json:"-"`

	Default    any  `json:"default,omitempty"`
	HasDefault bool `json:"-"`

	MinLength *int           `json:"minLength,omitempty"`
	MaxLength *int           `json:"maxLength,omitempty"`
	Pattern   string         `json:"pattern,omitempty"`
	patternRe *regexp.Regexp // compiled Pattern
	Format    string         `json:"format,omitempty"`

	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *float64 `json:"exclusiveMaximum,omitempty"`
}

var validTypes = map[string]bool{
	"string": true, "integer": true, "number": true, "boolean": true, "object": true, "array": true, "null": true,
}

var supportedFormats = map[string]bool{"email": true, "uri": true, "hostname": true}

// annotation-only keywords that are accepted and ignored.
var ignoredKeywords = map[string]bool{"$schema": true, "$comment": true, "examples": true, "ui:help": true, "ui:widget": true}

// CompileError reports a problem in a schema document.
type CompileError struct {
	Path string
	Line int
	Msg  string
}

func (e *CompileError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("schema %s (line %d): %s", e.Path, e.Line, e.Msg)
	}
	return fmt.Sprintf("schema %s: %s", e.Path, e.Msg)
}

// CompileYAML compiles a schema from a YAML node, preserving property order.
func CompileYAML(n *yaml.Node) (*Schema, error) {
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	return compile(n, "#")
}

// CompileBytes compiles a YAML or JSON schema document.
func CompileBytes(data []byte) (*Schema, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(data, &n); err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}
	return CompileYAML(&n)
}

func compile(n *yaml.Node, path string) (*Schema, error) {
	fail := func(line int, format string, args ...any) error {
		return &CompileError{Path: path, Line: line, Msg: fmt.Sprintf(format, args...)}
	}
	if n.Kind != yaml.MappingNode {
		return nil, fail(n.Line, "schema must be a mapping")
	}
	s := &Schema{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, n.Content[i+1]
		var err error
		switch key {
		case "type":
			s.Types, err = decodeTypes(val)
		case "title":
			err = val.Decode(&s.Title)
		case "description":
			err = val.Decode(&s.Description)
		case "properties":
			err = s.compileProperties(val, path)
		case "required":
			err = val.Decode(&s.Required)
		case "additionalProperties":
			var b bool
			if err = val.Decode(&b); err == nil {
				s.AdditionalProperties = &b
			} else {
				err = fmt.Errorf("only boolean additionalProperties is supported")
			}
		case "items":
			s.Items, err = compile(val, path+"/items")
		case "minItems":
			s.MinItems, err = decodeNonNegInt(val)
		case "maxItems":
			s.MaxItems, err = decodeNonNegInt(val)
		case "uniqueItems":
			err = val.Decode(&s.UniqueItems)
		case "enum":
			if err = val.Decode(&s.Enum); err == nil && len(s.Enum) == 0 {
				err = fmt.Errorf("enum must not be empty")
			}
			for j := range s.Enum {
				s.Enum[j] = Normalize(s.Enum[j])
			}
		case "const":
			err = val.Decode(&s.Const)
			s.Const, s.HasConst = Normalize(s.Const), true
		case "default":
			err = val.Decode(&s.Default)
			s.Default, s.HasDefault = Normalize(s.Default), true
		case "minLength":
			s.MinLength, err = decodeNonNegInt(val)
		case "maxLength":
			s.MaxLength, err = decodeNonNegInt(val)
		case "pattern":
			if err = val.Decode(&s.Pattern); err == nil {
				s.patternRe, err = regexp.Compile(s.Pattern)
			}
		case "format":
			if err = val.Decode(&s.Format); err == nil && !supportedFormats[s.Format] {
				err = fmt.Errorf("unsupported format %q (supported: email, uri, hostname)", s.Format)
			}
		case "minimum":
			s.Minimum, err = decodeFloat(val)
		case "maximum":
			s.Maximum, err = decodeFloat(val)
		case "exclusiveMinimum":
			s.ExclusiveMinimum, err = decodeFloat(val)
		case "exclusiveMaximum":
			s.ExclusiveMaximum, err = decodeFloat(val)
		default:
			if !ignoredKeywords[key] {
				err = fmt.Errorf("unsupported keyword %q", key)
			}
		}
		var nested *CompileError
		if errors.As(err, &nested) {
			return nil, nested // already located precisely
		}
		if err != nil {
			return nil, fail(n.Content[i].Line, "%s: %v", key, err)
		}
	}
	for _, r := range s.Required {
		if s.Properties == nil || s.Properties[r] == nil {
			return nil, fail(n.Line, "required property %q is not declared in properties", r)
		}
	}
	if s.HasDefault {
		if errs := s.Validate(s.Default); len(errs) > 0 {
			return nil, fail(n.Line, "default value is invalid: %s", errs[0].Error())
		}
	}
	return s, nil
}

func (s *Schema) compileProperties(val *yaml.Node, path string) error {
	if val.Kind != yaml.MappingNode {
		return fmt.Errorf("properties must be a mapping")
	}
	s.Properties = map[string]*Schema{}
	for j := 0; j+1 < len(val.Content); j += 2 {
		name := val.Content[j].Value
		child, err := compile(val.Content[j+1], path+"/properties/"+name)
		if err != nil {
			return err
		}
		s.Properties[name] = child
		s.Order = append(s.Order, name)
	}
	return nil
}

func decodeTypes(n *yaml.Node) ([]string, error) {
	var ts []string
	if n.Kind == yaml.ScalarNode {
		ts = []string{n.Value}
	} else if err := n.Decode(&ts); err != nil {
		return nil, err
	}
	for _, t := range ts {
		if !validTypes[t] {
			return nil, fmt.Errorf("unknown type %q", t)
		}
	}
	return ts, nil
}

func decodeNonNegInt(n *yaml.Node) (*int, error) {
	var v int
	if err := n.Decode(&v); err != nil {
		return nil, err
	}
	if v < 0 {
		return nil, fmt.Errorf("must be >= 0")
	}
	return &v, nil
}

func decodeFloat(n *yaml.Node) (*float64, error) {
	var v float64
	if err := n.Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Normalize converts decoded YAML/JSON values into the canonical forms the
// validator understands: all numbers become float64, maps become map[string]any.
func Normalize(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case float32:
		return float64(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = Normalize(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = Normalize(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[fmt.Sprint(k)] = Normalize(e)
		}
		return out
	}
	return v
}

// ValidationError is one failed constraint, located by JSON pointer.
type ValidationError struct {
	Path string `json:"path"`
	Msg  string `json:"message"`
}

func (e ValidationError) Error() string {
	p := e.Path
	if p == "" {
		p = "/"
	}
	return p + ": " + e.Msg
}

// Validate checks v (normalized with Normalize) against the schema and returns
// every violation, ordered by path.
func (s *Schema) Validate(v any) []ValidationError {
	var errs []ValidationError
	s.validate(Normalize(v), "", &errs)
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Path < errs[j].Path })
	return errs
}

func typeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func typeMatches(want, got string) bool {
	return want == got || (want == "number" && got == "integer")
}

func (s *Schema) validate(v any, path string, errs *[]ValidationError) {
	add := func(format string, args ...any) {
		*errs = append(*errs, ValidationError{Path: path, Msg: fmt.Sprintf(format, args...)})
	}
	got := typeOf(v)
	if len(s.Types) > 0 {
		ok := false
		for _, t := range s.Types {
			if typeMatches(t, got) {
				ok = true
				break
			}
		}
		if !ok {
			add("expected %s, got %s", strings.Join(s.Types, " or "), got)
			return
		}
	}
	if s.HasConst && !Equal(v, s.Const) {
		add("must equal %v", s.Const)
	}
	if len(s.Enum) > 0 {
		found := false
		for _, e := range s.Enum {
			if Equal(v, e) {
				found = true
				break
			}
		}
		if !found {
			add("must be one of %s", formatEnum(s.Enum))
		}
	}
	switch x := v.(type) {
	case string:
		s.validateString(x, add)
	case float64:
		s.validateNumber(x, add)
	case []any:
		s.validateArray(x, path, add, errs)
	case map[string]any:
		s.validateObject(x, path, add, errs)
	}
}

func (s *Schema) validateString(x string, add func(string, ...any)) {
	n := len([]rune(x))
	if s.MinLength != nil && n < *s.MinLength {
		add("must be at least %d characters", *s.MinLength)
	}
	if s.MaxLength != nil && n > *s.MaxLength {
		add("must be at most %d characters", *s.MaxLength)
	}
	if s.patternRe != nil && !s.patternRe.MatchString(x) {
		add("must match pattern %s", s.Pattern)
	}
	if s.Format != "" && !checkFormat(s.Format, x) {
		add("must be a valid %s", s.Format)
	}
}

func (s *Schema) validateNumber(x float64, add func(string, ...any)) {
	if s.Minimum != nil && x < *s.Minimum {
		add("must be >= %s", fmtNum(*s.Minimum))
	}
	if s.Maximum != nil && x > *s.Maximum {
		add("must be <= %s", fmtNum(*s.Maximum))
	}
	if s.ExclusiveMinimum != nil && x <= *s.ExclusiveMinimum {
		add("must be > %s", fmtNum(*s.ExclusiveMinimum))
	}
	if s.ExclusiveMaximum != nil && x >= *s.ExclusiveMaximum {
		add("must be < %s", fmtNum(*s.ExclusiveMaximum))
	}
}

func (s *Schema) validateArray(x []any, path string, add func(string, ...any), errs *[]ValidationError) {
	if s.MinItems != nil && len(x) < *s.MinItems {
		add("must have at least %d items", *s.MinItems)
	}
	if s.MaxItems != nil && len(x) > *s.MaxItems {
		add("must have at most %d items", *s.MaxItems)
	}
	if s.UniqueItems {
		for i := range x {
			for j := i + 1; j < len(x); j++ {
				if Equal(x[i], x[j]) {
					add("items %d and %d are equal; items must be unique", i, j)
				}
			}
		}
	}
	if s.Items != nil {
		for i, item := range x {
			s.Items.validate(item, path+"/"+strconv.Itoa(i), errs)
		}
	}
}

func (s *Schema) validateObject(x map[string]any, path string, add func(string, ...any), errs *[]ValidationError) {
	for _, r := range s.Required {
		if _, ok := x[r]; !ok {
			*errs = append(*errs, ValidationError{Path: path + "/" + escapePointer(r), Msg: "is required"})
		}
	}
	keys := make([]string, 0, len(x))
	for k := range x {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		child, ok := s.Properties[k]
		if !ok {
			if s.AdditionalProperties != nil && !*s.AdditionalProperties {
				add("property %q is not allowed", k)
			}
			continue
		}
		child.validate(x[k], path+"/"+escapePointer(k), errs)
	}
}

func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

var (
	emailRe    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	hostnameRe = regexp.MustCompile(`^([a-zA-Z0-9]([-a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)(\.[a-zA-Z0-9]([-a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)*$`)
	uriRe      = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://[^\s]+$`)
)

func checkFormat(format, x string) bool {
	switch format {
	case "email":
		return emailRe.MatchString(x)
	case "hostname":
		return len(x) <= 253 && hostnameRe.MatchString(x)
	case "uri":
		return uriRe.MatchString(x)
	}
	return true
}

// Equal compares normalized JSON values structurally.
func Equal(a, b any) bool {
	switch x := a.(type) {
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !Equal(v, w) {
				return false
			}
		}
		return true
	}
	return a == b
}

func formatEnum(vs []any) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		if s, ok := v.(string); ok {
			parts[i] = strconv.Quote(s)
		} else {
			parts[i] = fmt.Sprint(v)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
