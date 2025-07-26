package catalog

import "sort"

// RelationType names a directed edge, using Backstage's vocabulary.
type RelationType string

// Relation types. Every declared relation is stored together with its inverse.
const (
	RelOwnedBy        RelationType = "ownedBy"
	RelOwnerOf        RelationType = "ownerOf"
	RelPartOf         RelationType = "partOf"
	RelHasPart        RelationType = "hasPart"
	RelDependsOn      RelationType = "dependsOn"
	RelDependencyOf   RelationType = "dependencyOf"
	RelProvidesAPI    RelationType = "providesApi"
	RelAPIProvidedBy  RelationType = "apiProvidedBy"
	RelConsumesAPI    RelationType = "consumesApi"
	RelAPIConsumedBy  RelationType = "apiConsumedBy"
	RelChildOf        RelationType = "childOf"
	RelParentOf       RelationType = "parentOf"
	RelSubcomponentOf RelationType = "subcomponentOf"
	RelHasSubcomp     RelationType = "hasSubcomponent"
)

var inverseRelation = map[RelationType]RelationType{
	RelOwnedBy:        RelOwnerOf,
	RelOwnerOf:        RelOwnedBy,
	RelPartOf:         RelHasPart,
	RelHasPart:        RelPartOf,
	RelDependsOn:      RelDependencyOf,
	RelDependencyOf:   RelDependsOn,
	RelProvidesAPI:    RelAPIProvidedBy,
	RelAPIProvidedBy:  RelProvidesAPI,
	RelConsumesAPI:    RelAPIConsumedBy,
	RelAPIConsumedBy:  RelConsumesAPI,
	RelChildOf:        RelParentOf,
	RelParentOf:       RelChildOf,
	RelSubcomponentOf: RelHasSubcomp,
	RelHasSubcomp:     RelSubcomponentOf,
}

// Inverse returns the inverse relation type.
func (t RelationType) Inverse() RelationType { return inverseRelation[t] }

// Relation is one outgoing edge from an entity.
type Relation struct {
	Type   RelationType `json:"type"`
	Target Ref          `json:"target"`
}

// Graph is a directed multigraph of entity relations. Edges are deduplicated and
// every edge has its inverse, so both directions can be walked in O(degree).
type Graph struct {
	out  map[Ref][]Relation
	seen map[edgeKey]struct{}
}

type edgeKey struct {
	from Ref
	rel  Relation
}

func newGraph() *Graph {
	return &Graph{out: map[Ref][]Relation{}, seen: map[edgeKey]struct{}{}}
}

// add inserts from -t-> to and its inverse, ignoring duplicates.
func (g *Graph) add(from Ref, t RelationType, to Ref) {
	g.addOne(from, Relation{Type: t, Target: to})
	g.addOne(to, Relation{Type: t.Inverse(), Target: from})
}

func (g *Graph) addOne(from Ref, r Relation) {
	k := edgeKey{from: from, rel: r}
	if _, dup := g.seen[k]; dup {
		return
	}
	g.seen[k] = struct{}{}
	g.out[from] = append(g.out[from], r)
}

// finish sorts adjacency lists so iteration order is deterministic.
func (g *Graph) finish() {
	for ref, rels := range g.out {
		sort.Slice(rels, func(i, j int) bool {
			if rels[i].Type != rels[j].Type {
				return rels[i].Type < rels[j].Type
			}
			return rels[i].Target.String() < rels[j].Target.String()
		})
		g.out[ref] = rels
	}
	g.seen = nil
}

// Relations returns all outgoing relations of ref.
func (g *Graph) Relations(ref Ref) []Relation { return g.out[ref] }

// Targets returns the targets of ref's relations of type t.
func (g *Graph) Targets(ref Ref, t RelationType) []Ref {
	var out []Ref
	for _, r := range g.out[ref] {
		if r.Type == t {
			out = append(out, r.Target)
		}
	}
	return out
}

// EdgeCount returns the number of stored directed edges (each declared
// relation counts twice: once per direction).
func (g *Graph) EdgeCount() int {
	n := 0
	for _, rels := range g.out {
		n += len(rels)
	}
	return n
}
