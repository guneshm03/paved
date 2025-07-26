package catalog

import "sort"

// OrphanReason classifies why an entity is considered orphaned.
type OrphanReason string

// Orphan reasons.
const (
	// OrphanNoOwner: an ownable entity whose owner is missing or does not resolve,
	// and that cannot inherit one from a parent component or system.
	OrphanNoOwner OrphanReason = "no-owner"
	// OrphanNoSystem: a Component, API or Resource not part of any System
	// (directly or through its parent component).
	OrphanNoSystem OrphanReason = "no-system"
	// OrphanUnprovidedAPI: an API that no component provides.
	OrphanUnprovidedAPI OrphanReason = "unprovided-api"
	// OrphanUnusedResource: a Resource nothing depends on.
	OrphanUnusedResource OrphanReason = "unused-resource"
	// OrphanEmptyGroup: a Group that owns nothing, has no members and no sub-groups.
	OrphanEmptyGroup OrphanReason = "empty-group"
)

// Orphan is one finding of FindOrphans.
type Orphan struct {
	Ref     Ref          `json:"ref"`
	Reason  OrphanReason `json:"reason"`
	Detail  string       `json:"detail"`
	File    string       `json:"file"`
	Line    int          `json:"line"`
	Declare string       `json:"declared,omitempty"`
}

// FindOrphans reports entities disconnected from the ownership or system
// structure of the catalog. An entity can appear once per reason.
func (c *Catalog) FindOrphans() []Orphan {
	var out []Orphan
	add := func(e *Entity, r OrphanReason, field, detail, declared string) {
		out = append(out, Orphan{
			Ref: e.Ref(), Reason: r, Detail: detail, File: e.Source.File,
			Line: e.Source.LineOf(field), Declare: declared,
		})
	}
	for _, e := range c.entities {
		ref := e.Ref()
		if e.Kind != KindGroup {
			o, err := c.OwnerOf(ref)
			if err == nil && o.Effective == nil {
				detail := "no spec.owner and nothing to inherit one from"
				if e.Spec.Owner != "" {
					detail = "spec.owner does not resolve to a Group in the catalog"
				}
				add(e, OrphanNoOwner, "spec.owner", detail, e.Spec.Owner)
			}
		}
		switch e.Kind {
		case KindComponent, KindAPI, KindResource:
			if !c.inSystem(ref) {
				add(e, OrphanNoSystem, "spec.system", "not part of any System", e.Spec.System)
			}
		}
		if e.Kind == KindAPI && len(c.graph.Targets(ref, RelAPIProvidedBy)) == 0 {
			add(e, OrphanUnprovidedAPI, "metadata.name", "no Component lists this API in spec.providesApis", "")
		}
		if e.Kind == KindResource && len(c.graph.Targets(ref, RelDependencyOf)) == 0 {
			add(e, OrphanUnusedResource, "metadata.name", "nothing declares dependsOn on this Resource", "")
		}
		if e.Kind == KindGroup && len(e.Spec.Members) == 0 &&
			len(c.graph.Targets(ref, RelOwnerOf)) == 0 && len(c.graph.Targets(ref, RelParentOf)) == 0 {
			add(e, OrphanEmptyGroup, "metadata.name", "owns no entities and has no members or sub-groups", "")
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Reason != out[j].Reason {
			return out[i].Reason < out[j].Reason
		}
		return lessRef(out[i].Ref, out[j].Ref)
	})
	return out
}

// inSystem reports whether ref is part of a System directly or via its chain
// of parent components.
func (c *Catalog) inSystem(ref Ref) bool {
	seen := map[Ref]bool{}
	for cur := ref; !seen[cur]; {
		seen[cur] = true
		if len(c.graph.Targets(cur, RelPartOf)) > 0 {
			return true
		}
		parents := c.graph.Targets(cur, RelSubcomponentOf)
		if len(parents) == 0 {
			return false
		}
		cur = parents[0]
	}
	return false
}
