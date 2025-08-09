package schema

import (
	"errors"
	"strings"
	"testing"
)

const paramSchema = `
type: object
additionalProperties: false
required: [name, owner]
properties:
  name: {type: string, pattern: '^[a-z][a-z0-9-]+$', minLength: 3, maxLength: 20, description: Service name}
  owner: {type: string, title: Owner group}
  port: {type: integer, minimum: 1024, maximum: 65535, default: 8080}
  ratio: {type: number, exclusiveMinimum: 0, exclusiveMaximum: 1}
  enabled: {type: boolean, default: true}
  tier: {enum: [gold, silver, 3]}
  kind: {const: service}
  email: {type: string, format: email}
  url: {type: string, format: uri}
  host: {type: string, format: hostname}
  tags: {type: array, items: {type: string}, minItems: 1, maxItems: 3, uniqueItems: true, default: [a]}
  db:
    type: object
    properties:
      engine: {type: string, default: postgres}
      size: {type: [integer, "null"]}
`

func mustCompile(t *testing.T, src string) *Schema {
	t.Helper()
	s, err := CompileBytes([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidate(t *testing.T) {
	s := mustCompile(t, paramSchema)
	tests := []struct {
		name  string
		value map[string]any
		want  []string // "path: message prefix"
	}{
		{"valid", map[string]any{"name": "svc", "owner": "t", "port": 8081, "ratio": 0.5, "tier": 3, "kind": "service",
			"email": "a@b.co", "url": "https://x.example/a", "host": "api.x.example", "tags": []any{"a", "b"},
			"db": map[string]any{"engine": "mysql", "size": nil}}, nil},
		{"missing required", map[string]any{"name": "svc"}, []string{"/owner: is required"}},
		{"wrong types", map[string]any{"name": 1, "owner": "t", "enabled": "yes"}, []string{
			"/enabled: expected boolean, got string", "/name: expected string, got integer"}},
		{"string bounds", map[string]any{"name": "ab", "owner": "t"}, []string{"/name: must be at least 3"}},
		{"too long and pattern", map[string]any{"name": "Abcdefghijklmnopqrstuvwxyz", "owner": "t"}, []string{
			"/name: must be at most 20", "/name: must match pattern"}},
		{"number bounds", map[string]any{"name": "svc", "owner": "t", "port": 80, "ratio": 1}, []string{
			"/port: must be >= 1024", "/ratio: must be < 1"}},
		{"integer vs number", map[string]any{"name": "svc", "owner": "t", "port": 8080.5}, []string{"/port: expected integer, got number"}},
		{"enum const", map[string]any{"name": "svc", "owner": "t", "tier": "bronze", "kind": "job"}, []string{
			"/kind: must equal service", "/tier: must be one of"}},
		{"formats", map[string]any{"name": "svc", "owner": "t", "email": "nope", "url": "x", "host": "-bad-"}, []string{
			"/email: must be a valid email", "/host: must be a valid hostname", "/url: must be a valid uri"}},
		{"array rules", map[string]any{"name": "svc", "owner": "t", "tags": []any{"a", "a", 1, "d"}}, []string{
			"/tags: must have at most 3", "/tags: items 0 and 1 are equal", "/tags/2: expected string"}},
		{"empty array", map[string]any{"name": "svc", "owner": "t", "tags": []any{}}, []string{"/tags: must have at least 1"}},
		{"additional", map[string]any{"name": "svc", "owner": "t", "extra": 1}, []string{`: property "extra" is not allowed`}},
		{"nested", map[string]any{"name": "svc", "owner": "t", "db": map[string]any{"size": "big"}}, []string{
			"/db/size: expected integer or null, got string"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := s.Validate(tt.value)
			if len(errs) != len(tt.want) {
				t.Fatalf("got %d errors %v, want %d %v", len(errs), errs, len(tt.want), tt.want)
			}
			for i, w := range tt.want {
				if !strings.HasPrefix(errs[i].Path+": "+errs[i].Msg, w) {
					t.Errorf("error %d = %q, want prefix %q", i, errs[i].Path+": "+errs[i].Msg, w)
				}
			}
		})
	}
}

func TestCompileErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{"type: object\nproperties: {a: {oneOf: []}}", `unsupported keyword "oneOf"`},
		{"type: thing", `unknown type "thing"`},
		{"type: object\nrequired: [a]", `required property "a" is not declared`},
		{"type: string\npattern: '('", "pattern"},
		{"type: string\nformat: uuid", `unsupported format "uuid"`},
		{"type: string\nminLength: -1", "must be >= 0"},
		{"type: integer\ndefault: x", "default value is invalid"},
		{"enum: []", "enum must not be empty"},
		{"type: object\nadditionalProperties: {type: string}", "only boolean additionalProperties"},
		{"type: object\nproperties: [a]", "properties must be a mapping"},
		{"- 1", "schema must be a mapping"},
		{"minimum: abc", "minimum"},
		{"type: [string, {}]", "type"},
		{"items: 3", "schema must be a mapping"},
		{"a: [", "parse schema"},
	}
	for _, tt := range tests {
		_, err := CompileBytes([]byte(tt.src))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Compile(%q) err = %v, want containing %q", tt.src, err, tt.want)
		}
	}
	_, err := CompileBytes([]byte("type: object\nproperties:\n  a: {foo: 1}\n"))
	var ce *CompileError
	if !errors.As(err, &ce) || ce.Path != "#/properties/a" || ce.Line != 3 {
		t.Fatalf("compile error location = %+v", ce)
	}
	if !strings.Contains((&CompileError{Path: "#", Msg: "m"}).Error(), "schema #: m") {
		t.Fatal("CompileError without line")
	}
}

func TestApplyDefaults(t *testing.T) {
	s := mustCompile(t, paramSchema)
	in := map[string]any{"name": "svc", "owner": "t", "db": map[string]any{}}
	out := s.ApplyDefaults(in)
	if out["port"] != float64(8080) || out["enabled"] != true {
		t.Fatalf("defaults = %v", out)
	}
	if out["db"].(map[string]any)["engine"] != "postgres" {
		t.Fatalf("nested default missing: %v", out["db"])
	}
	if _, ok := in["port"]; ok {
		t.Fatal("ApplyDefaults mutated its input")
	}
	// Default slices are copied, not shared between renders.
	out["tags"].([]any)[0] = "changed"
	if s.ApplyDefaults(map[string]any{})["tags"].([]any)[0] != "a" {
		t.Fatal("default slice was shared")
	}
	if errs := s.Validate(out); len(errs) != 0 {
		t.Fatalf("defaults should validate: %v", errs)
	}
}

func TestCoerceString(t *testing.T) {
	s := mustCompile(t, paramSchema)
	ok := []struct {
		prop, raw string
		want      any
	}{
		{"port", " 9000 ", float64(9000)},
		{"ratio", "0.25", 0.25},
		{"enabled", "no", false},
		{"enabled", "ON", true},
		{"name", "x", "x"},
		{"tier", "gold", "gold"}, // untyped stays string
	}
	for _, tt := range ok {
		got, err := s.Properties[tt.prop].CoerceString(tt.raw)
		if err != nil || !Equal(got, tt.want) {
			t.Errorf("Coerce(%s, %q) = %v, %v; want %v", tt.prop, tt.raw, got, err, tt.want)
		}
	}
	tags, err := s.Properties["tags"].CoerceString("a, b,c")
	if err != nil || !Equal(tags, []any{"a", "b", "c"}) {
		t.Fatalf("array coercion = %v, %v", tags, err)
	}
	empty, err := s.Properties["tags"].CoerceString("")
	if err != nil || len(empty.([]any)) != 0 {
		t.Fatalf("empty array = %v, %v", empty, err)
	}
	size, err := s.Properties["db"].Properties["size"].CoerceString("null")
	if err != nil || size != nil {
		t.Fatalf("nullable = %v, %v", size, err)
	}
	for prop, raw := range map[string]string{"port": "8x", "ratio": "x", "enabled": "maybe", "db": "{}"} {
		if _, err := s.Properties[prop].CoerceString(raw); err == nil {
			t.Errorf("Coerce(%s, %q) should fail", prop, raw)
		}
	}
	ints := mustCompile(t, "type: array\nitems: {type: integer}")
	if _, err := ints.CoerceString("1,x"); err == nil {
		t.Fatal("bad array item should fail")
	}
	if _, err := mustCompile(t, "type: 'null'").CoerceString("x"); err == nil {
		t.Fatal("null coercion of x should fail")
	}
}

func TestParams(t *testing.T) {
	s := mustCompile(t, paramSchema)
	ps := s.Params()
	if len(ps) != 12 || ps[0].Name != "name" || ps[2].Name != "port" {
		t.Fatalf("params order = %+v", ps)
	}
	if !ps[0].Required || ps[0].Constraints != "pattern ^[a-z][a-z0-9-]+$; length 3..20" || ps[0].Description != "Service name" {
		t.Errorf("name param = %+v", ps[0])
	}
	if ps[1].Description != "Owner group" {
		t.Errorf("title fallback = %+v", ps[1])
	}
	if ps[2].Default != float64(8080) || ps[2].Constraints != "range 1024..65535" {
		t.Errorf("port param = %+v", ps[2])
	}
	if ps[5].Constraints != `one of ["gold", "silver", 3]` {
		t.Errorf("enum constraints = %q", ps[5].Constraints)
	}
	if ps[7].Constraints != "format email" {
		t.Errorf("format constraints = %q", ps[7].Constraints)
	}
	open := mustCompile(t, "type: object\nproperties:\n  a: {type: string, maxLength: 5}\n  b: {type: number, minimum: 1}\n").Params()
	if open[0].Constraints != "length 0..5" || open[1].Constraints != "range 1..∞" {
		t.Errorf("open ranges = %+v", open)
	}
}

func TestEqualAndNormalize(t *testing.T) {
	a := Normalize(map[any]any{"x": []any{1, int64(2), uint64(3), float32(4)}})
	b := map[string]any{"x": []any{1.0, 2.0, 3.0, 4.0}}
	if !Equal(a, b) {
		t.Fatalf("Equal(%v, %v) = false", a, b)
	}
	if Equal(b, map[string]any{"y": []any{}}) || Equal([]any{1.0}, []any{1.0, 2.0}) || Equal(b, []any{}) {
		t.Fatal("Equal false positives")
	}
	if (ValidationError{Msg: "m"}).Error() != "/: m" {
		t.Fatal("root path rendering")
	}
}
