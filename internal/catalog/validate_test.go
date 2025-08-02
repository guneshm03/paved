package catalog

import (
	"sort"
	"strings"
	"testing"
)

// build parses descriptor files (name -> YAML) and builds a catalog.
func build(t *testing.T, files map[string]string) *Catalog {
	t.Helper()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var (
		es []*Entity
		ds []Diagnostic
	)
	for _, n := range names {
		e, d := ParseDescriptors(n, []byte(files[n]))
		es = append(es, e...)
		ds = append(ds, d...)
	}
	return Build(es, ds)
}

// codes returns "code@file:line" strings for diagnostics, for compact asserts.
func codes(ds []Diagnostic) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Code
	}
	return out
}

func hasCode(ds []Diagnostic, code string) *Diagnostic {
	for i := range ds {
		if ds[i].Code == code {
			return &ds[i]
		}
	}
	return nil
}

const groupA = `apiVersion: backstage.io/v1alpha1
kind: Group
metadata: {name: team-a}
spec: {type: team, members: [someone]}
`

func TestValidCatalogHasNoDiagnostics(t *testing.T) {
	c := build(t, map[string]string{
		"a.yaml": groupA + `---
apiVersion: backstage.io/v1alpha1
kind: System
metadata: {name: sys}
spec: {owner: team-a}
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata: {name: svc, tags: [go], links: [{url: "https://x.example/d", title: d}]}
spec: {type: service, lifecycle: production, owner: team-a, system: sys, providesApis: [svc-api], dependsOn: [resource:db]}
---
apiVersion: backstage.io/v1alpha1
kind: API
metadata: {name: svc-api}
spec: {type: openapi, lifecycle: production, owner: team-a, system: sys, definition: "openapi: 3.0.0"}
---
apiVersion: backstage.io/v1alpha1
kind: Resource
metadata: {name: db}
spec: {type: database, owner: group:default/team-a, system: sys}
`,
	})
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
	st := c.Stats()
	if st.Entities != 5 || st.ByKind[KindComponent] != 1 {
		t.Fatalf("stats = %+v", st)
	}
	// owner x4, system x3, providesApi, dependsOn
	if st.Edges != 9 {
		t.Fatalf("edges = %d, want 9", st.Edges)
	}
}

func TestValidationCodes(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		code string
		line int
		sev  Severity
	}{
		{"missing apiVersion", "kind: Group\nmetadata: {name: g}\nspec: {type: team}\n", "missing-api-version", 1, SeverityError},
		{"bad apiVersion", "apiVersion: v2\nkind: Group\nmetadata: {name: g}\nspec: {type: team}\n", "unsupported-api-version", 1, SeverityError},
		{"missing name", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {}\nspec: {type: team}\n", "missing-name", 3, SeverityError},
		{"bad name", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata:\n  name: -bad\nspec: {type: team}\n", "invalid-name", 4, SeverityError},
		{"bad namespace", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata:\n  name: g\n  namespace: Bad_NS\nspec: {type: team}\n", "invalid-namespace", 5, SeverityError},
		{"missing owner", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: c}\nspec: {type: service, lifecycle: production}\n", "missing-field", 4, SeverityError},
		{"api without definition", "apiVersion: backstage.io/v1alpha1\nkind: API\nmetadata: {name: a}\nspec: {type: openapi, lifecycle: production, owner: team-a}\n", "missing-field", 4, SeverityError},
		{"bad tag", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata:\n  name: g\n  tags:\n    - Not_OK\nspec: {type: team}\n", "invalid-tag", 6, SeverityWarning},
		{"bad link", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata:\n  name: g\n  links:\n    - url: not-a-url\nspec: {type: team}\n", "invalid-link", 6, SeverityWarning},
		{"unknown lifecycle", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: c}\nspec:\n  type: service\n  lifecycle: beta\n  owner: team-a\n", "unknown-lifecycle", 6, SeverityWarning},
		{"unknown api type", "apiVersion: backstage.io/v1alpha1\nkind: API\nmetadata: {name: a}\nspec:\n  type: soap\n  lifecycle: production\n  owner: team-a\n  definition: x\n", "unknown-api-type", 5, SeverityWarning},
		{"dangling owner", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: c}\nspec:\n  type: service\n  lifecycle: production\n  owner: nobody\n", "dangling-ref", 7, SeverityError},
		{"owner wrong kind", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: c}\nspec:\n  type: service\n  lifecycle: production\n  owner: component:c2\n", "wrong-ref-kind", 7, SeverityError},
		{"dependsOn needs kind", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: c}\nspec:\n  type: service\n  lifecycle: production\n  owner: team-a\n  dependsOn:\n    - db\n", "invalid-ref", 9, SeverityError},
		{"self reference", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: c}\nspec:\n  type: service\n  lifecycle: production\n  owner: team-a\n  dependsOn: [component:c]\n", "self-reference", 8, SeverityError},
		{"unknown spec field", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: g}\nspec:\n  type: team\n  colour: blue\n", "unknown-field", 6, SeverityWarning},
		{"field for other kind", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: g}\nspec:\n  type: team\n  lifecycle: production\n", "unexpected-field", 6, SeverityWarning},
		{"unknown top-level", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: g}\nspec: {type: team}\nextra: 1\n", "unknown-field", 5, SeverityWarning},
		{"unsupported kind", "apiVersion: backstage.io/v1alpha1\nkind: Domain\nmetadata: {name: d}\n", "unsupported-kind", 2, SeverityError},
		{"missing kind", "apiVersion: backstage.io/v1alpha1\nmetadata: {name: d}\n", "missing-kind", 1, SeverityError},
		{"not a mapping", "- a\n- b\n", "invalid-document", 1, SeverityError},
		{"spec not mapping", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: g}\nspec: [1]\n", "invalid-field", 4, SeverityError},
		{"type error", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: g}\nspec:\n  type: team\n  children: {a: b}\n", "invalid-field", 6, SeverityError},
		{"yaml syntax", "apiVersion: [\n", "yaml-syntax", 1, SeverityError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := build(t, map[string]string{"g.yaml": groupA, "x.yaml": tt.yaml})
			var got *Diagnostic
			for i, d := range c.Diagnostics() {
				if d.Code == tt.code && d.File == "x.yaml" {
					got = &c.Diagnostics()[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("no %s diagnostic; got %v", tt.code, c.Diagnostics())
			}
			if got.Line != tt.line {
				t.Errorf("line = %d, want %d (%s)", got.Line, tt.line, got)
			}
			if got.Severity != tt.sev {
				t.Errorf("severity = %s, want %s", got.Severity, tt.sev)
			}
		})
	}
}

func TestDuplicateEntity(t *testing.T) {
	c := build(t, map[string]string{"a.yaml": groupA, "b.yaml": groupA})
	d := hasCode(c.Diagnostics(), "duplicate-entity")
	if d == nil || d.File != "b.yaml" || !strings.Contains(d.Message, "a.yaml") {
		t.Fatalf("duplicate diagnostic = %+v", d)
	}
	if len(c.Entities()) != 1 {
		t.Fatalf("entities = %d, want 1", len(c.Entities()))
	}
}

func group(name, parent string, children ...string) string {
	s := "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: " + name + "}\nspec:\n  type: team\n  members: [m]\n"
	if parent != "" {
		s += "  parent: " + parent + "\n"
	}
	if len(children) > 0 {
		s += "  children: [" + strings.Join(children, ", ") + "]\n"
	}
	return s
}

func TestGroupHierarchyChecks(t *testing.T) {
	c := build(t, map[string]string{
		"g.yaml": strings.Join([]string{
			group("root", "", "a"),
			group("a", "root"),
			group("b", "root"), // root does not list b
			group("x", "y"),
			group("y", "x"), // cycle x <-> y
			group("p1", "", "kid"),
			group("p2", "", "kid"),
			group("kid", ""), // two parents via children lists
		}, "---\n"),
	})
	ds := c.Diagnostics()
	if d := hasCode(ds, "inconsistent-hierarchy"); d == nil || d.Entity != "group:default/b" {
		t.Errorf("inconsistent-hierarchy = %+v", d)
	}
	if d := hasCode(ds, "group-cycle"); d == nil || !strings.Contains(d.Message, "group:default/x -> group:default/y -> group:default/x") {
		t.Errorf("group-cycle = %+v", d)
	}
	if d := hasCode(ds, "multiple-parents"); d == nil || d.Entity != "group:default/kid" {
		t.Errorf("multiple-parents = %+v", d)
	}
	// Cycles must not hang ancestor/descendant walks.
	if got := c.Ancestors(MustParseRef("group:x")); len(got) != 1 {
		t.Errorf("Ancestors(x) = %v", got)
	}
	if got := c.Descendants(MustParseRef("group:x")); len(got) != 1 {
		t.Errorf("Descendants(x) = %v", got)
	}
}

func comp(name string, extra string) string {
	return "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: " + name + "}\nspec:\n  type: service\n  lifecycle: production\n  owner: team-a\n" + extra
}

func TestDependencyCycleIsWarning(t *testing.T) {
	c := build(t, map[string]string{
		"g.yaml": groupA,
		"c.yaml": strings.Join([]string{
			comp("a", "  dependsOn: [component:b]\n"),
			comp("b", "  dependsOn: [component:c]\n"),
			comp("c", "  dependsOn: [component:a]\n"),
			comp("d", "  dependsOn: [component:a]\n"),
		}, "---\n"),
	})
	d := hasCode(c.Diagnostics(), "dependency-cycle")
	if d == nil || d.Severity != SeverityWarning {
		t.Fatalf("dependency-cycle = %+v (all: %v)", d, codes(c.Diagnostics()))
	}
	want := "component:default/a -> component:default/b -> component:default/c -> component:default/a"
	if !strings.Contains(d.Message, want) {
		t.Fatalf("cycle message = %q, want %q", d.Message, want)
	}
	// Walks terminate on cycles and report each entity once.
	hits, err := c.Walk(MustParseRef("component:a"), Upstream, WalkOptions{Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 { // c, b, d
		t.Fatalf("upstream of a = %v", hits)
	}
}

func TestSortDiagnosticsAndString(t *testing.T) {
	ds := []Diagnostic{
		{File: "b", Line: 1, Code: "x", Severity: SeverityError, Message: "m"},
		{File: "a", Line: 9, Code: "y", Severity: SeverityWarning},
		{File: "a", Line: 2, Code: "z", Severity: SeverityError},
		{File: "a", Line: 2, Code: "c", Severity: SeverityError},
	}
	SortDiagnostics(ds)
	if got := strings.Join(codes(ds), ","); got != "c,z,y,x" {
		t.Fatalf("order = %s", got)
	}
	if s := ds[3].String(); s != "b:1: error [x] m" {
		t.Fatalf("String = %q", s)
	}
	if s := (Diagnostic{Code: "q", Severity: SeverityError}).String(); !strings.HasPrefix(s, "<catalog>") {
		t.Fatalf("String = %q", s)
	}
	if s := (Diagnostic{File: "f", Code: "q", Severity: SeverityError}).String(); !strings.HasPrefix(s, "f: ") {
		t.Fatalf("String = %q", s)
	}
	e, w := CountBySeverity(ds)
	if e != 3 || w != 1 {
		t.Fatalf("counts = %d,%d", e, w)
	}
}

func TestParseDescriptorsDetails(t *testing.T) {
	es, ds := ParseDescriptors("x.yaml", []byte(`---
---
apiVersion: backstage.io/v1alpha1
kind: API
metadata:
  name: a
  annotations: {k: v}
spec:
  type: openapi
  lifecycle: production
  owner: team-a
  definition:
    $text: ./openapi.yaml
`))
	if len(ds) != 0 || len(es) != 1 {
		t.Fatalf("es=%d ds=%v", len(es), ds)
	}
	e := es[0]
	if e.Spec.Definition != "$text:./openapi.yaml" {
		t.Errorf("definition = %q", e.Spec.Definition)
	}
	if e.Metadata.Namespace != DefaultNamespace || e.Annotation("k") != "v" {
		t.Errorf("metadata = %+v", e.Metadata)
	}
	if e.Source.Line != 3 || e.Source.LineOf("spec.owner") != 11 || e.Source.LineOf("nope") != 3 {
		t.Errorf("lines: doc=%d owner=%d", e.Source.Line, e.Source.LineOf("spec.owner"))
	}
	if e.Source.Dir() != "." {
		t.Errorf("dir = %q", e.Source.Dir())
	}
	if e.DisplayName() != "a" {
		t.Errorf("DisplayName = %q", e.DisplayName())
	}
}

func FuzzParseDescriptors(f *testing.F) {
	f.Add([]byte(groupA))
	f.Add([]byte("kind: Component\nmetadata: {name: x}\nspec: {dependsOn: [a, b]}\n"))
	f.Add([]byte("---\n- 1\n---\nkind: [\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		es, _ := ParseDescriptors("f.yaml", data)
		c := Build(es, nil) // must never panic, whatever the input
		_ = c.FindOrphans()
	})
}
