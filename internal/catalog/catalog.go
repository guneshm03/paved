package catalog

import (
	"sort"
	"strings"
)

// Catalog is an immutable, validated snapshot of a set of entities and their
// relation graph. It is safe for concurrent reads.
type Catalog struct {
	entities []*Entity
	byRef    map[Ref]*Entity
	graph    *Graph
	diags    []Diagnostic
}

// Build validates entities, resolves their references and constructs the
// relation graph. parseDiags are carried through into Diagnostics().
func Build(entities []*Entity, parseDiags []Diagnostic) *Catalog {
	c := &Catalog{
		byRef: map[Ref]*Entity{},
		graph: newGraph(),
		diags: append([]Diagnostic(nil), parseDiags...),
	}
	for _, e := range entities {
		ref := e.Ref()
		if prev, dup := c.byRef[ref]; dup {
			c.diags = append(c.diags, Diagnostic{
				Severity: SeverityError, Code: "duplicate-entity", File: e.Source.File, Line: e.Source.Line,
				Entity: ref.String(), Message: "entity " + ref.String() + " is already declared in " + prev.Source.File,
			})
			continue
		}
		c.byRef[ref] = e
		c.entities = append(c.entities, e)
	}
	sort.Slice(c.entities, func(i, j int) bool { return lessRef(c.entities[i].Ref(), c.entities[j].Ref()) })

	for _, e := range c.entities {
		c.diags = append(c.diags, validateEntity(e)...)
		c.diags = append(c.diags, c.resolveRelations(e)...)
	}
	c.graph.finish()
	c.diags = append(c.diags, c.checkGroupHierarchy()...)
	c.diags = append(c.diags, c.checkDependencyCycles()...)
	SortDiagnostics(c.diags)
	return c
}

func lessRef(a, b Ref) bool {
	if a.Kind != b.Kind {
		return kindOrder(a.Kind) < kindOrder(b.Kind)
	}
	if a.Namespace != b.Namespace {
		return a.Namespace < b.Namespace
	}
	return a.Name < b.Name
}

func kindOrder(k Kind) int {
	for i, x := range AllKinds {
		if x == k {
			return i
		}
	}
	return len(AllKinds)
}

// Entities returns all entities sorted by kind, namespace and name.
func (c *Catalog) Entities() []*Entity { return c.entities }

// Get returns the entity for ref.
func (c *Catalog) Get(ref Ref) (*Entity, bool) {
	e, ok := c.byRef[ref]
	return e, ok
}

// Lookup parses a user-supplied reference (kind optional when unambiguous) and
// returns the matching entity.
func (c *Catalog) Lookup(s string) (*Entity, error) {
	if strings.Contains(s, ":") {
		ref, err := ParseRef(s, "", DefaultNamespace)
		if err != nil {
			return nil, err
		}
		if e, ok := c.byRef[ref]; ok {
			return e, nil
		}
		return nil, &NotFoundError{Query: s}
	}
	var matches []*Entity
	for _, k := range AllKinds {
		ref, err := ParseRef(s, k, DefaultNamespace)
		if err != nil {
			return nil, err
		}
		if e, ok := c.byRef[ref]; ok {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 0:
		return nil, &NotFoundError{Query: s}
	case 1:
		return matches[0], nil
	default:
		cands := make([]string, len(matches))
		for i, m := range matches {
			cands[i] = m.Ref().String()
		}
		return nil, &AmbiguousRefError{Query: s, Candidates: cands}
	}
}

// NotFoundError is returned when no entity matches a query.
type NotFoundError struct{ Query string }

func (e *NotFoundError) Error() string { return "entity not found: " + e.Query }

// AmbiguousRefError is returned when a kind-less reference matches several entities.
type AmbiguousRefError struct {
	Query      string
	Candidates []string
}

func (e *AmbiguousRefError) Error() string {
	return "reference " + e.Query + " is ambiguous; use one of: " + strings.Join(e.Candidates, ", ")
}

// Graph returns the relation graph.
func (c *Catalog) Graph() *Graph { return c.graph }

// Diagnostics returns all parse and validation findings, sorted.
func (c *Catalog) Diagnostics() []Diagnostic { return c.diags }

// Filter selects entities. Zero-valued fields match everything.
type Filter struct {
	Kind      Kind
	Type      string
	Lifecycle string
	Owner     Ref
	System    Ref
	Tag       string
}

// Find returns entities matching f, in catalog order.
func (c *Catalog) Find(f Filter) []*Entity {
	var out []*Entity
	for _, e := range c.entities {
		ref := e.Ref()
		switch {
		case f.Kind != "" && e.Kind != f.Kind,
			f.Type != "" && !strings.EqualFold(e.Spec.Type, f.Type),
			f.Lifecycle != "" && !strings.EqualFold(e.Spec.Lifecycle, f.Lifecycle),
			f.Tag != "" && !e.HasTag(f.Tag),
			!f.Owner.IsZero() && !refIn(c.graph.Targets(ref, RelOwnedBy), f.Owner),
			!f.System.IsZero() && !refIn(c.graph.Targets(ref, RelPartOf), f.System):
			continue
		}
		out = append(out, e)
	}
	return out
}

func refIn(refs []Ref, r Ref) bool {
	for _, x := range refs {
		if x == r {
			return true
		}
	}
	return false
}

// Stats summarises the catalog.
type Stats struct {
	Entities int          `json:"entities"`
	ByKind   map[Kind]int `json:"byKind"`
	Edges    int          `json:"edges"`
	Errors   int          `json:"errors"`
	Warnings int          `json:"warnings"`
}

// Stats computes summary counts.
func (c *Catalog) Stats() Stats {
	s := Stats{Entities: len(c.entities), ByKind: map[Kind]int{}, Edges: c.graph.EdgeCount() / 2}
	for _, e := range c.entities {
		s.ByKind[e.Kind]++
	}
	s.Errors, s.Warnings = CountBySeverity(c.diags)
	return s
}
