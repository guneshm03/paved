package catalog

import (
	"fmt"
	"io"
	"strings"
)

// forwardRelations are the edge types drawn in exported diagrams (one
// direction per declared relation, so no edge is drawn twice).
var forwardRelations = map[RelationType]bool{
	RelOwnedBy: true, RelPartOf: true, RelDependsOn: true, RelProvidesAPI: true,
	RelConsumesAPI: true, RelChildOf: true, RelSubcomponentOf: true,
}

// WriteDOT renders the graph in Graphviz DOT format.
func (c *Catalog) WriteDOT(w io.Writer) error {
	var b strings.Builder
	b.WriteString("digraph catalog {\n  rankdir=LR;\n  node [fontname=\"Helvetica\"];\n")
	shapes := map[Kind]string{
		KindComponent: "box", KindAPI: "ellipse", KindSystem: "folder", KindGroup: "house", KindResource: "cylinder",
	}
	for _, e := range c.entities {
		fmt.Fprintf(&b, "  %q [label=%q shape=%s];\n", e.Ref().String(), string(e.Kind)+"\n"+e.Metadata.Name, shapes[e.Kind])
	}
	for _, e := range c.entities {
		for _, r := range c.graph.Relations(e.Ref()) {
			if forwardRelations[r.Type] {
				fmt.Fprintf(&b, "  %q -> %q [label=%q];\n", e.Ref().String(), r.Target.String(), string(r.Type))
			}
		}
	}
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteMermaid renders the graph as a Mermaid flowchart.
func (c *Catalog) WriteMermaid(w io.Writer) error {
	ids := map[Ref]string{}
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	for i, e := range c.entities {
		id := fmt.Sprintf("n%d", i)
		ids[e.Ref()] = id
		fmt.Fprintf(&b, "  %s[\"%s: %s\"]\n", id, e.Kind, e.Metadata.Name)
	}
	for _, e := range c.entities {
		for _, r := range c.graph.Relations(e.Ref()) {
			if forwardRelations[r.Type] {
				fmt.Fprintf(&b, "  %s -->|%s| %s\n", ids[e.Ref()], r.Type, ids[r.Target])
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
