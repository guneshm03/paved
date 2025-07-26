package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/guneshm03/paved/internal/catalog"
)

func catalogCommands() []command {
	return []command{
		{name: "validate", summary: "validate every descriptor; exit 1 on errors", run: runValidate},
		{name: "list", summary: "list entities with filters", run: runList},
		{name: "get", summary: "show one entity and its relations", run: runGet},
		{name: "orphans", summary: "report entities detached from owners/systems", run: runOrphans},
		{name: "export", summary: "export the relation graph as dot or mermaid", run: runExport},
		{name: "stats", summary: "summary counts", run: runStats},
	}
}

func runValidate(a *app, args []string) error {
	fs := a.newFlags("catalog validate")
	format := fs.String("format", "text", "text|json")
	strict := fs.Bool("strict", false, "treat warnings as failures")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if err := checkFormat(*format, "text", "json"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	ds := ws.Catalog.Diagnostics()
	st := ws.Catalog.Stats()
	if *format == "json" {
		if err := a.printJSON(map[string]any{"stats": st, "diagnostics": ds}); err != nil {
			return err
		}
	} else {
		for _, d := range ds {
			fmt.Fprintln(a.stdout, d.String())
		}
		fmt.Fprintf(a.stdout, "\n%d entities, %d relations: %d errors, %d warnings\n", st.Entities, st.Edges, st.Errors, st.Warnings)
	}
	if st.Errors > 0 || (*strict && st.Warnings > 0) {
		return &findingsError{msg: fmt.Sprintf("catalog validation failed (%d errors, %d warnings)", st.Errors, st.Warnings)}
	}
	return nil
}

func runList(a *app, args []string) error {
	fs := a.newFlags("catalog list")
	kind := fs.String("kind", "", "filter by kind")
	typ := fs.String("type", "", "filter by spec.type")
	lifecycle := fs.String("lifecycle", "", "filter by spec.lifecycle")
	owner := fs.String("owner", "", "filter by owning group")
	system := fs.String("system", "", "filter by system")
	tag := fs.String("tag", "", "filter by tag")
	format := fs.String("format", "text", "text|json")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if err := checkFormat(*format, "text", "json"); err != nil {
		return err
	}
	f := catalog.Filter{Type: *typ, Lifecycle: *lifecycle, Tag: *tag}
	if *kind != "" {
		k, ok := catalog.ParseKind(*kind)
		if !ok {
			return usagef("unsupported kind %q", *kind)
		}
		f.Kind = k
	}
	var err error
	if *owner != "" {
		if f.Owner, err = catalog.ParseRef(*owner, catalog.KindGroup, catalog.DefaultNamespace); err != nil {
			return usagef("%v", err)
		}
	}
	if *system != "" {
		if f.System, err = catalog.ParseRef(*system, catalog.KindSystem, catalog.DefaultNamespace); err != nil {
			return usagef("%v", err)
		}
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	items := ws.Catalog.Find(f)
	if *format == "json" {
		return a.printJSON(items)
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REF\tTYPE\tLIFECYCLE\tOWNER\tSOURCE")
	for _, e := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Ref().Short(), dash(e.Spec.Type), dash(e.Spec.Lifecycle), dash(e.Spec.Owner), e.Source.File)
	}
	fmt.Fprintf(tw, "\n%d entities\n", len(items))
	return tw.Flush()
}

func runGet(a *app, args []string) error {
	fs := a.newFlags("catalog get")
	format := fs.String("format", "text", "text|json")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "catalog get <ref>"); err != nil {
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
	rels := ws.Catalog.Graph().Relations(e.Ref())
	if *format == "json" {
		return a.printJSON(map[string]any{"entity": e, "relations": rels})
	}
	w := a.stdout
	fmt.Fprintf(w, "%s  (%s:%d)\n", e.Ref(), e.Source.File, e.Source.Line)
	if e.Metadata.Description != "" {
		fmt.Fprintf(w, "  %s\n", e.Metadata.Description)
	}
	for _, kv := range [][2]string{
		{"type", e.Spec.Type}, {"lifecycle", e.Spec.Lifecycle}, {"owner", e.Spec.Owner}, {"system", e.Spec.System},
		{"tags", strings.Join(e.Metadata.Tags, ", ")},
	} {
		if kv[1] != "" {
			fmt.Fprintf(w, "  %-10s %s\n", kv[0]+":", kv[1])
		}
	}
	if len(rels) > 0 {
		fmt.Fprintln(w, "  relations:")
		for _, r := range rels {
			fmt.Fprintf(w, "    %-16s %s\n", r.Type, r.Target.Short())
		}
	}
	return nil
}

func runOrphans(a *app, args []string) error {
	fs := a.newFlags("catalog orphans")
	format := fs.String("format", "text", "text|json")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if err := checkFormat(*format, "text", "json"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	orphans := ws.Catalog.FindOrphans()
	if *format == "json" {
		return a.printJSON(orphans)
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REASON\tENTITY\tLOCATION\tDETAIL")
	for _, o := range orphans {
		detail := o.Detail
		if o.Declare != "" {
			detail += " (declared: " + o.Declare + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s:%d\t%s\n", o.Reason, o.Ref.Short(), o.File, o.Line, detail)
	}
	fmt.Fprintf(tw, "\n%d orphan findings\n", len(orphans))
	return tw.Flush()
}

func runExport(a *app, args []string) error {
	fs := a.newFlags("catalog export")
	format := fs.String("format", "mermaid", "dot|mermaid")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if err := checkFormat(*format, "dot", "mermaid"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	if *format == "dot" {
		return ws.Catalog.WriteDOT(a.stdout)
	}
	return ws.Catalog.WriteMermaid(a.stdout)
}

func runStats(a *app, args []string) error {
	fs := a.newFlags("catalog stats")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	st := ws.Catalog.Stats()
	fmt.Fprintf(a.stdout, "entities: %d\n", st.Entities)
	for _, k := range catalog.AllKinds {
		fmt.Fprintf(a.stdout, "  %-10s %d\n", k, st.ByKind[k])
	}
	fmt.Fprintf(a.stdout, "relations: %d\nerrors: %d\nwarnings: %d\n", st.Edges, st.Errors, st.Warnings)
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
