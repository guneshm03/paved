package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/guneshm03/paved/internal/catalog"
)

func graphCommands() []command {
	return []command{
		{name: "owner", summary: "who owns an entity (direct, inherited, group chain)", run: runOwner},
		{name: "dependents", summary: "what depends on an entity (upstream)", run: walkCmd(catalog.Upstream)},
		{name: "dependencies", summary: "what an entity depends on (downstream)", run: walkCmd(catalog.Downstream)},
		{name: "impact", summary: "blast radius: dependents grouped by owning team", run: runImpact},
		{name: "path", summary: "shortest dependency path between two entities", run: runPath},
		{name: "owned", summary: "entities owned by a group", run: runOwned},
	}
}

func runOwner(a *app, args []string) error {
	fs := a.newFlags("graph owner")
	format := fs.String("format", "text", "text|json")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "graph owner <ref>"); err != nil {
		return err
	}
	if err := checkFormat(*format, "text", "json"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	e, err := ws.Catalog.Lookup(pos[0])
	if err != nil {
		return err
	}
	o, err := ws.Catalog.OwnerOf(e.Ref())
	if err != nil {
		return err
	}
	if *format == "json" {
		return a.printJSON(o)
	}
	w := a.stdout
	fmt.Fprintf(w, "%s\n", o.Entity)
	switch {
	case o.Effective == nil:
		decl := "none"
		if o.Declared != "" {
			decl = o.Declared + " (does not resolve)"
		}
		fmt.Fprintf(w, "  owner:     UNOWNED (declared: %s)\n", decl)
		return nil
	case o.Via == "self":
		fmt.Fprintf(w, "  is a group\n")
	case o.Direct != nil:
		fmt.Fprintf(w, "  owner:     %s (spec.owner)\n", o.Direct)
	default:
		fmt.Fprintf(w, "  owner:     %s (inherited via %s from %s)\n", o.Effective, o.Via, o.InheritedFrom)
	}
	if len(o.Chain) > 0 {
		parts := make([]string, len(o.Chain))
		for i, r := range o.Chain {
			parts[i] = r.String()
		}
		fmt.Fprintf(w, "  escalates: %s\n", strings.Join(parts, " -> "))
	}
	return nil
}

func walkCmd(dir catalog.Direction) func(a *app, args []string) error {
	name := "dependents"
	if dir == catalog.Downstream {
		name = "dependencies"
	}
	return func(a *app, args []string) error {
		fs := a.newFlags("graph " + name)
		direct := fs.Bool("direct", false, "only direct (depth 1) results")
		depth := fs.Int("depth", 0, "maximum hops (0 = unlimited)")
		format := fs.String("format", "text", "text|json")
		pos, err := parse(fs, args)
		if err != nil {
			return err
		}
		if err := needArgs(pos, 1, "graph "+name+" <ref>"); err != nil {
			return err
		}
		if err := checkFormat(*format, "text", "json"); err != nil {
			return err
		}
		if *depth < 0 {
			return usagef("--depth must be >= 0")
		}
		ws, err := a.open()
		if err != nil {
			return err
		}
		e, err := ws.Catalog.Lookup(pos[0])
		if err != nil {
			return err
		}
		hits, err := ws.Catalog.Walk(e.Ref(), dir, catalog.WalkOptions{Transitive: !*direct, MaxDepth: *depth})
		if err != nil {
			return err
		}
		if *format == "json" {
			return a.printJSON(hits)
		}
		fmt.Fprintf(a.stdout, "%s of %s: %d\n", name, e.Ref(), len(hits))
		for _, h := range hits {
			fmt.Fprintf(a.stdout, "  [%d] %-40s via %-28s %s\n", h.Depth, h.Ref.Short(), h.Via, pathString(h.Path))
		}
		return nil
	}
}

func pathString(p []catalog.Ref) string {
	parts := make([]string, len(p))
	for i, r := range p {
		parts[i] = r.Name
	}
	return strings.Join(parts, " -> ")
}

func runImpact(a *app, args []string) error {
	fs := a.newFlags("graph impact")
	format := fs.String("format", "text", "text|json")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "graph impact <ref>"); err != nil {
		return err
	}
	if err := checkFormat(*format, "text", "json"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	e, err := ws.Catalog.Lookup(pos[0])
	if err != nil {
		return err
	}
	im, err := ws.Catalog.ImpactOf(e.Ref())
	if err != nil {
		return err
	}
	if *format == "json" {
		return a.printJSON(im)
	}
	fmt.Fprintf(a.stdout, "impact of %s: %d dependents across %d teams\n", e.Ref(), len(im.Dependents), len(im.Teams))
	teams := make([]catalog.Ref, 0, len(im.Teams))
	for t := range im.Teams {
		teams = append(teams, t)
	}
	sort.Slice(teams, func(i, j int) bool { return teams[i].String() < teams[j].String() })
	for _, t := range teams {
		names := make([]string, len(im.Teams[t]))
		for i, r := range im.Teams[t] {
			names[i] = r.Short()
		}
		fmt.Fprintf(a.stdout, "  %-28s %s\n", t.Short(), strings.Join(names, ", "))
	}
	if len(im.Unowned) > 0 {
		names := make([]string, len(im.Unowned))
		for i, r := range im.Unowned {
			names[i] = r.Short()
		}
		fmt.Fprintf(a.stdout, "  %-28s %s\n", "(unowned)", strings.Join(names, ", "))
	}
	return nil
}

func runPath(a *app, args []string) error {
	fs := a.newFlags("graph path")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 2, "graph path <from> <to>"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	from, err := ws.Catalog.Lookup(pos[0])
	if err != nil {
		return err
	}
	to, err := ws.Catalog.Lookup(pos[1])
	if err != nil {
		return err
	}
	p, err := ws.Catalog.PathBetween(from.Ref(), to.Ref())
	if err != nil {
		return err
	}
	if p == nil {
		return &findingsError{msg: fmt.Sprintf("%s does not depend on %s", from.Ref(), to.Ref())}
	}
	parts := make([]string, len(p))
	for i, r := range p {
		parts[i] = r.Short()
	}
	fmt.Fprintln(a.stdout, strings.Join(parts, " -> "))
	return nil
}

func runOwned(a *app, args []string) error {
	fs := a.newFlags("graph owned")
	recursive := fs.Bool("recursive", false, "include entities owned by sub-groups")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "graph owned <group>"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	ref, err := catalog.ParseRef(pos[0], catalog.KindGroup, catalog.DefaultNamespace)
	if err != nil {
		return usagef("%v", err)
	}
	if ref.Kind != catalog.KindGroup {
		return usagef("%s is not a group", ref)
	}
	if _, ok := ws.Catalog.Get(ref); !ok {
		return &catalog.NotFoundError{Query: ref.String()}
	}
	items := ws.Catalog.OwnedBy(ref, *recursive)
	fmt.Fprintf(a.stdout, "%s owns %d entities", ref, len(items))
	if *recursive {
		fmt.Fprint(a.stdout, " (including sub-groups)")
	}
	fmt.Fprintln(a.stdout)
	for _, e := range items {
		fmt.Fprintf(a.stdout, "  %-36s %s\n", e.Ref().Short(), e.Spec.Owner)
	}
	return nil
}
