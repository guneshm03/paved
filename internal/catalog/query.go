package catalog

import (
	"sort"
)

// Ownership answers "who owns X".
type Ownership struct {
	Entity Ref `json:"entity"`
	// Declared is the raw spec.owner value ("" when absent).
	Declared string `json:"declared,omitempty"`
	// Direct is the resolved spec.owner, if it resolves.
	Direct *Ref `json:"direct,omitempty"`
	// Effective is the group accountable for the entity: Direct, or else the
	// owner inherited from the parent component or the system.
	Effective *Ref `json:"effective,omitempty"`
	// Via explains where Effective came from: "spec.owner", "subcomponentOf",
	// "system", "self" (for groups) or "" when unowned.
	Via string `json:"via,omitempty"`
	// InheritedFrom is the entity Effective was inherited from, if any.
	InheritedFrom *Ref `json:"inheritedFrom,omitempty"`
	// Chain is the group hierarchy above Effective, nearest parent first.
	Chain []Ref `json:"chain,omitempty"`
}

// OwnerOf resolves direct and effective ownership of ref.
func (c *Catalog) OwnerOf(ref Ref) (Ownership, error) {
	e, ok := c.byRef[ref]
	if !ok {
		return Ownership{}, &NotFoundError{Query: ref.String()}
	}
	o := Ownership{Entity: ref, Declared: e.Spec.Owner}
	if e.Kind == KindGroup {
		self := ref
		o.Effective, o.Via = &self, "self"
		o.Chain = c.Ancestors(ref)
		return o, nil
	}
	if owners := c.graph.Targets(ref, RelOwnedBy); len(owners) > 0 {
		d := owners[0]
		o.Direct, o.Effective, o.Via = &d, &d, "spec.owner"
	} else {
		// Inherit along subcomponentOf, then partOf(system); guard against cycles.
		seen := map[Ref]bool{ref: true}
		cur := ref
		for o.Effective == nil {
			next, via := c.inheritanceParent(cur)
			if next.IsZero() || seen[next] {
				break
			}
			seen[next] = true
			if owners := c.graph.Targets(next, RelOwnedBy); len(owners) > 0 {
				eff, from := owners[0], next
				o.Effective, o.Via, o.InheritedFrom = &eff, via, &from
			}
			cur = next
		}
	}
	if o.Effective != nil {
		o.Chain = c.Ancestors(*o.Effective)
	}
	return o, nil
}

func (c *Catalog) inheritanceParent(ref Ref) (Ref, string) {
	if p := c.graph.Targets(ref, RelSubcomponentOf); len(p) > 0 {
		return p[0], "subcomponentOf"
	}
	if s := c.graph.Targets(ref, RelPartOf); len(s) > 0 {
		return s[0], "system"
	}
	return Ref{}, ""
}

// Ancestors returns the parent chain of a group, nearest first. Cycles are cut.
func (c *Catalog) Ancestors(group Ref) []Ref {
	var out []Ref
	seen := map[Ref]bool{group: true}
	cur := group
	for {
		ps := c.graph.Targets(cur, RelChildOf)
		if len(ps) == 0 || seen[ps[0]] {
			return out
		}
		seen[ps[0]] = true
		out = append(out, ps[0])
		cur = ps[0]
	}
}

// Descendants returns all sub-groups of a group (breadth-first, sorted per level).
func (c *Catalog) Descendants(group Ref) []Ref {
	var out []Ref
	seen := map[Ref]bool{group: true}
	queue := []Ref{group}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, ch := range c.graph.Targets(cur, RelParentOf) {
			if !seen[ch] {
				seen[ch] = true
				out = append(out, ch)
				queue = append(queue, ch)
			}
		}
	}
	return out
}

// OwnedBy returns entities whose direct owner is group, and with recursive set,
// those owned by any of its descendant groups. Sorted in catalog order.
func (c *Catalog) OwnedBy(group Ref, recursive bool) []*Entity {
	groups := []Ref{group}
	if recursive {
		groups = append(groups, c.Descendants(group)...)
	}
	var out []*Entity
	for _, g := range groups {
		for _, r := range c.graph.Targets(g, RelOwnerOf) {
			out = append(out, c.byRef[r])
		}
	}
	sort.Slice(out, func(i, j int) bool { return lessRef(out[i].Ref(), out[j].Ref()) })
	return out
}

// Direction selects which way a dependency walk goes.
type Direction int

// Walk directions.
const (
	// Downstream follows "X depends on" edges: dependencies of X.
	Downstream Direction = iota
	// Upstream follows reverse edges: dependents of X ("what depends on X").
	Upstream
)

// WalkOptions bounds a dependency walk.
type WalkOptions struct {
	// MaxDepth limits the number of hops; 0 means unlimited.
	MaxDepth int
	// Transitive set to false returns only depth-1 results.
	Transitive bool
}

// Hit is one entity reached by a dependency walk.
type Hit struct {
	Ref   Ref `json:"ref"`
	Depth int `json:"depth"`
	// Path is the chain from the query root to Ref (inclusive of both ends).
	Path []Ref `json:"path"`
	// Via is the relation used for the last hop: "dependsOn" or the reference
	// of the API that links the two components (e.g. "api:default/payments-api").
	Via string `json:"via"`
}

// step is one hop of a dependency walk.
type step struct {
	to  Ref
	via string
}

// neighbours yields the dependency hops out of ref in the given direction.
// Downstream: dependsOn targets, plus providers of consumed APIs (through the API),
// plus the APIs consumed themselves. Upstream is the exact mirror, so that
// "what depends on Y" includes components that consume an API Y provides.
func (c *Catalog) neighbours(ref Ref, dir Direction) []step {
	var out []step
	dep, consume, provided := RelDependsOn, RelConsumesAPI, RelAPIProvidedBy
	if dir == Upstream {
		dep, consume, provided = RelDependencyOf, RelProvidesAPI, RelAPIConsumedBy
	}
	for _, t := range c.graph.Targets(ref, dep) {
		out = append(out, step{to: t, via: string(RelDependsOn)})
	}
	for _, api := range c.graph.Targets(ref, consume) {
		for _, t := range c.graph.Targets(api, provided) {
			out = append(out, step{to: t, via: api.String()})
		}
	}
	// An API itself: upstream are its consumers, downstream its providers.
	if ref.Kind == KindAPI {
		for _, t := range c.graph.Targets(ref, provided) {
			out = append(out, step{to: t, via: ref.String()})
		}
	}
	return out
}

// Walk performs a breadth-first dependency traversal from root. Each entity is
// reported once, at its shortest distance. O(V+E) over the reached subgraph.
func (c *Catalog) Walk(root Ref, dir Direction, opts WalkOptions) ([]Hit, error) {
	if _, ok := c.byRef[root]; !ok {
		return nil, &NotFoundError{Query: root.String()}
	}
	maxDepth := opts.MaxDepth
	if !opts.Transitive {
		maxDepth = 1
	}
	type item struct {
		ref  Ref
		path []Ref
	}
	visited := map[Ref]bool{root: true}
	queue := []item{{ref: root, path: []Ref{root}}}
	var hits []Hit
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		depth := len(cur.path) - 1
		if maxDepth > 0 && depth >= maxDepth {
			continue
		}
		for _, s := range c.neighbours(cur.ref, dir) {
			if visited[s.to] {
				continue
			}
			visited[s.to] = true
			p := append(append(make([]Ref, 0, len(cur.path)+1), cur.path...), s.to)
			hits = append(hits, Hit{Ref: s.to, Depth: depth + 1, Path: p, Via: s.via})
			queue = append(queue, item{ref: s.to, path: p})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Depth != hits[j].Depth {
			return hits[i].Depth < hits[j].Depth
		}
		return lessRef(hits[i].Ref, hits[j].Ref)
	})
	return hits, nil
}

// Impact answers "if X breaks, who is affected": all transitive dependents and
// the effective owners of those dependents, so the right teams can be paged.
type Impact struct {
	Root       Ref           `json:"root"`
	Dependents []Hit         `json:"dependents"`
	Teams      map[Ref][]Ref `json:"teams"`
	Unowned    []Ref         `json:"unowned,omitempty"`
}

// ImpactOf computes the blast radius of ref.
func (c *Catalog) ImpactOf(ref Ref) (Impact, error) {
	hits, err := c.Walk(ref, Upstream, WalkOptions{Transitive: true})
	if err != nil {
		return Impact{}, err
	}
	im := Impact{Root: ref, Dependents: hits, Teams: map[Ref][]Ref{}}
	for _, h := range hits {
		o, err := c.OwnerOf(h.Ref)
		if err != nil {
			return Impact{}, err
		}
		if o.Effective == nil {
			im.Unowned = append(im.Unowned, h.Ref)
			continue
		}
		im.Teams[*o.Effective] = append(im.Teams[*o.Effective], h.Ref)
	}
	return im, nil
}

// PathBetween returns the shortest downstream dependency path from a to b, or
// nil when b is not reachable from a.
func (c *Catalog) PathBetween(a, b Ref) ([]Ref, error) {
	if _, ok := c.byRef[b]; !ok {
		return nil, &NotFoundError{Query: b.String()}
	}
	hits, err := c.Walk(a, Downstream, WalkOptions{Transitive: true})
	if err != nil {
		return nil, err
	}
	for _, h := range hits {
		if h.Ref == b {
			return h.Path, nil
		}
	}
	return nil, nil
}
