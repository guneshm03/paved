package catalog

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	tagRe = regexp.MustCompile(`^[a-z0-9:+#]+(-[a-z0-9:+#]+)*$`)

	knownLifecycles = map[string]bool{"experimental": true, "production": true, "deprecated": true}
	knownAPITypes   = map[string]bool{"openapi": true, "asyncapi": true, "graphql": true, "grpc": true}
)

// requiredSpec lists the spec fields each kind must set.
var requiredSpec = map[Kind][]string{
	KindComponent: {"type", "lifecycle", "owner"},
	KindAPI:       {"type", "lifecycle", "owner", "definition"},
	KindSystem:    {"owner"},
	KindGroup:     {"type"},
	KindResource:  {"type", "owner"},
}

func specValue(s Spec, field string) string {
	switch field {
	case "type":
		return s.Type
	case "lifecycle":
		return s.Lifecycle
	case "owner":
		return s.Owner
	case "definition":
		return s.Definition
	}
	return ""
}

// validateEntity checks a single entity in isolation (no cross-references).
func validateEntity(e *Entity) []Diagnostic {
	var ds []Diagnostic
	ref := e.Ref().String()
	add := func(sev Severity, code, field, format string, args ...any) {
		ds = append(ds, Diagnostic{
			Severity: sev, Code: code, File: e.Source.File, Line: e.Source.LineOf(field),
			Entity: ref, Message: fmt.Sprintf(format, args...),
		})
	}

	switch {
	case e.APIVersion == "":
		add(SeverityError, "missing-api-version", "apiVersion", "apiVersion is required")
	case !supportedAPIVersions[e.APIVersion]:
		add(SeverityError, "unsupported-api-version", "apiVersion", "apiVersion %q is not supported (use backstage.io/v1alpha1)", e.APIVersion)
	}

	switch {
	case e.Metadata.Name == "":
		add(SeverityError, "missing-name", "metadata", "metadata.name is required")
	case !ValidName(e.Metadata.Name):
		add(SeverityError, "invalid-name", "metadata.name", "metadata.name %q must be 1-63 characters of [a-zA-Z0-9-_.], starting and ending alphanumeric", e.Metadata.Name)
	}
	if !ValidNamespace(e.Metadata.Namespace) {
		add(SeverityError, "invalid-namespace", "metadata.namespace", "metadata.namespace %q is invalid", e.Metadata.Namespace)
	}
	for i, t := range e.Metadata.Tags {
		if len(t) > 63 || !tagRe.MatchString(t) {
			add(SeverityWarning, "invalid-tag", "metadata.tags."+strconv.Itoa(i), "tag %q should be lower-case words joined by '-'", t)
		}
	}
	for i, l := range e.Metadata.Links {
		u, err := url.Parse(l.URL)
		if l.URL == "" || err != nil || u.Scheme == "" || u.Host == "" {
			add(SeverityWarning, "invalid-link", "metadata.links."+strconv.Itoa(i), "link %q is not an absolute URL", l.URL)
		}
	}

	for _, f := range requiredSpec[e.Kind] {
		if strings.TrimSpace(specValue(e.Spec, f)) == "" {
			add(SeverityError, "missing-field", "spec", "%s requires spec.%s", e.Kind, f)
		}
	}
	if (e.Kind == KindComponent || e.Kind == KindAPI) && e.Spec.Lifecycle != "" && !knownLifecycles[e.Spec.Lifecycle] {
		add(SeverityWarning, "unknown-lifecycle", "spec.lifecycle", "lifecycle %q is not one of experimental, production, deprecated", e.Spec.Lifecycle)
	}
	if e.Kind == KindAPI && e.Spec.Type != "" && !knownAPITypes[e.Spec.Type] {
		add(SeverityWarning, "unknown-api-type", "spec.type", "API type %q is not one of openapi, asyncapi, graphql, grpc", e.Spec.Type)
	}
	return ds
}

// refField describes a relation-bearing spec field.
type refField struct {
	name        string
	values      []string
	defaultKind Kind // kind assumed when the reference omits it ("" = required)
	allowed     []Kind
	rel         RelationType
}

func refFields(e *Entity) []refField {
	one := func(s string) []string {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []string{s}
	}
	s := e.Spec
	var fs []refField
	switch e.Kind {
	case KindComponent:
		fs = []refField{
			{"owner", one(s.Owner), KindGroup, []Kind{KindGroup}, RelOwnedBy},
			{"system", one(s.System), KindSystem, []Kind{KindSystem}, RelPartOf},
			{"subcomponentOf", one(s.SubcomponentOf), KindComponent, []Kind{KindComponent}, RelSubcomponentOf},
			{"providesApis", s.ProvidesAPIs, KindAPI, []Kind{KindAPI}, RelProvidesAPI},
			{"consumesApis", s.ConsumesAPIs, KindAPI, []Kind{KindAPI}, RelConsumesAPI},
			{"dependsOn", s.DependsOn, "", []Kind{KindComponent, KindResource}, RelDependsOn},
		}
	case KindAPI:
		fs = []refField{
			{"owner", one(s.Owner), KindGroup, []Kind{KindGroup}, RelOwnedBy},
			{"system", one(s.System), KindSystem, []Kind{KindSystem}, RelPartOf},
		}
	case KindSystem:
		fs = []refField{{"owner", one(s.Owner), KindGroup, []Kind{KindGroup}, RelOwnedBy}}
	case KindResource:
		fs = []refField{
			{"owner", one(s.Owner), KindGroup, []Kind{KindGroup}, RelOwnedBy},
			{"system", one(s.System), KindSystem, []Kind{KindSystem}, RelPartOf},
			{"dependsOn", s.DependsOn, "", []Kind{KindComponent, KindResource}, RelDependsOn},
		}
	case KindGroup:
		fs = []refField{
			{"parent", one(s.Parent), KindGroup, []Kind{KindGroup}, RelChildOf},
			{"children", s.Children, KindGroup, []Kind{KindGroup}, RelParentOf},
		}
	}
	return fs
}

// resolveRef parses one reference of entity e's field. It is shared by
// validation and by queries that report declared-but-unresolved targets.
func resolveRef(e *Entity, f refField, value string) (Ref, error) {
	return ParseRef(value, f.defaultKind, e.Metadata.Namespace)
}

// resolveRelations parses e's reference fields, records graph edges for
// resolvable targets, and reports malformed, mis-typed and dangling references.
func (c *Catalog) resolveRelations(e *Entity) []Diagnostic {
	var ds []Diagnostic
	self := e.Ref()
	for _, f := range refFields(e) {
		for i, v := range f.values {
			field := "spec." + f.name
			if isListField(f.name) {
				field += "." + strconv.Itoa(i)
			}
			add := func(code, format string, args ...any) {
				ds = append(ds, Diagnostic{
					Severity: SeverityError, Code: code, File: e.Source.File, Line: e.Source.LineOf(field),
					Entity: self.String(), Message: fmt.Sprintf(format, args...),
				})
			}
			target, err := resolveRef(e, f, v)
			if err != nil {
				add("invalid-ref", "spec.%s: %v", f.name, err)
				continue
			}
			if !containsKind(f.allowed, target.Kind) {
				add("wrong-ref-kind", "spec.%s must reference %s, got %s", f.name, kindList(f.allowed), target)
				continue
			}
			if target == self {
				add("self-reference", "spec.%s references the entity itself", f.name)
				continue
			}
			if _, ok := c.byRef[target]; !ok {
				add("dangling-ref", "spec.%s references %s, which is not in the catalog", f.name, target)
				continue
			}
			c.graph.add(self, f.rel, target)
		}
	}
	return ds
}

func isListField(name string) bool {
	switch name {
	case "providesApis", "consumesApis", "dependsOn", "children":
		return true
	}
	return false
}

func kindList(ks []Kind) string {
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = string(k)
	}
	return strings.Join(parts, " or ")
}

// checkGroupHierarchy reports parent/children mismatches (warning) and cycles
// in the group tree (error).
func (c *Catalog) checkGroupHierarchy() []Diagnostic {
	var ds []Diagnostic
	for _, e := range c.entities {
		if e.Kind != KindGroup {
			continue
		}
		ref := e.Ref()
		parents := c.graph.Targets(ref, RelChildOf)
		if len(parents) > 1 {
			names := make([]string, len(parents))
			for i, p := range parents {
				names[i] = p.String()
			}
			ds = append(ds, Diagnostic{
				Severity: SeverityError, Code: "multiple-parents", File: e.Source.File, Line: e.Source.LineOf("spec.parent"),
				Entity: ref.String(), Message: "group has more than one parent: " + strings.Join(names, ", "),
			})
		}
		// A child listed by its parent but not pointing back (or vice versa)
		// is legal but usually a mistake.
		if e.Spec.Parent != "" {
			if p, err := ParseRef(e.Spec.Parent, KindGroup, e.Metadata.Namespace); err == nil {
				if pe, ok := c.byRef[p]; ok && !declaresChild(pe, ref) {
					ds = append(ds, Diagnostic{
						Severity: SeverityWarning, Code: "inconsistent-hierarchy", File: e.Source.File, Line: e.Source.LineOf("spec.parent"),
						Entity: ref.String(), Message: fmt.Sprintf("parent %s does not list this group in spec.children", p),
					})
				}
			}
		}
	}
	for _, cyc := range c.findCycles(KindGroup, RelChildOf) {
		e := c.byRef[cyc[0]]
		ds = append(ds, Diagnostic{
			Severity: SeverityError, Code: "group-cycle", File: e.Source.File, Line: e.Source.LineOf("spec.parent"),
			Entity: cyc[0].String(), Message: "group hierarchy contains a cycle: " + cycleString(cyc),
		})
	}
	return ds
}

func declaresChild(parent *Entity, child Ref) bool {
	for _, v := range parent.Spec.Children {
		if r, err := ParseRef(v, KindGroup, parent.Metadata.Namespace); err == nil && r == child {
			return true
		}
	}
	return false
}

// checkDependencyCycles reports dependsOn cycles as warnings: they are legal
// in Backstage but make blast-radius answers circular.
func (c *Catalog) checkDependencyCycles() []Diagnostic {
	var ds []Diagnostic
	for _, cyc := range c.findCycles("", RelDependsOn) {
		e := c.byRef[cyc[0]]
		ds = append(ds, Diagnostic{
			Severity: SeverityWarning, Code: "dependency-cycle", File: e.Source.File, Line: e.Source.LineOf("spec.dependsOn"),
			Entity: cyc[0].String(), Message: "dependsOn cycle: " + cycleString(cyc),
		})
	}
	return ds
}

// findCycles returns one representative cycle per strongly connected component
// (Tarjan's algorithm, O(V+E)) of the subgraph formed by edges of type rel
// between entities of the given kind ("" = any kind). Each cycle is rotated to
// start at its smallest reference for deterministic output.
func (c *Catalog) findCycles(kind Kind, rel RelationType) [][]Ref {
	index := map[Ref]int{}
	low := map[Ref]int{}
	onStack := map[Ref]bool{}
	var stack []Ref
	var sccs [][]Ref
	next := 0

	var strong func(v Ref)
	strong = func(v Ref) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range c.graph.Targets(v, rel) {
			if kind != "" && w.Kind != kind {
				continue
			}
			if _, seen := index[w]; !seen {
				strong(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []Ref
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			if len(comp) > 1 {
				sccs = append(sccs, comp)
			}
		}
	}
	for _, e := range c.entities {
		if kind != "" && e.Kind != kind {
			continue
		}
		if _, seen := index[e.Ref()]; !seen {
			strong(e.Ref())
		}
	}

	cycles := make([][]Ref, 0, len(sccs))
	for _, comp := range sccs {
		cycles = append(cycles, c.cycleWithin(comp, rel))
	}
	sort.Slice(cycles, func(i, j int) bool { return cycles[i][0].String() < cycles[j][0].String() })
	return cycles
}

// cycleWithin walks rel edges inside one SCC from its smallest member until it
// returns to it, producing a concrete cycle path.
func (c *Catalog) cycleWithin(comp []Ref, rel RelationType) []Ref {
	in := map[Ref]bool{}
	start := comp[0]
	for _, r := range comp {
		in[r] = true
		if r.String() < start.String() {
			start = r
		}
	}
	// BFS from start back to start inside the component (shortest cycle through start).
	prev := map[Ref]Ref{}
	queue := []Ref{start}
	visited := map[Ref]bool{}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range c.graph.Targets(v, rel) {
			if !in[w] {
				continue
			}
			if w == start {
				path := []Ref{v}
				for p := v; p != start; {
					p = prev[p]
					path = append(path, p)
				}
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				return path
			}
			if !visited[w] {
				visited[w] = true
				prev[w] = v
				queue = append(queue, w)
			}
		}
	}
	return comp // unreachable for a true SCC; keep the component as a fallback
}

func cycleString(cyc []Ref) string {
	parts := make([]string, 0, len(cyc)+1)
	for _, r := range cyc {
		parts = append(parts, r.String())
	}
	parts = append(parts, cyc[0].String())
	return strings.Join(parts, " -> ")
}
