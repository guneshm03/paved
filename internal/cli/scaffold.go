package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/guneshm03/paved/internal/catalog"
	"github.com/guneshm03/paved/internal/scaffold"
	"go.yaml.in/yaml/v3"
)

func scaffoldCommands() []command {
	return []command{
		{name: "list", summary: "list available templates", run: runTemplates},
		{name: "show", summary: "show a template's parameters", run: runTemplateShow},
		{name: "run", summary: "render a template (--dry-run shows a diff)", run: runScaffold},
	}
}

// setFlags collects repeated --set key=value flags.
type setFlags []string

func (s *setFlags) String() string     { return strings.Join(*s, ",") }
func (s *setFlags) Set(v string) error { *s = append(*s, v); return nil }

func runTemplates(a *app, args []string) error {
	fs := a.newFlags("scaffold list")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TEMPLATE\tTITLE\tOUTPUT")
	for _, t := range ws.Templates {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name, t.Title, t.Output)
	}
	return tw.Flush()
}

func runTemplateShow(a *app, args []string) error {
	fs := a.newFlags("scaffold show")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "scaffold show <template>"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	t, err := ws.Template(pos[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s — %s\n%s\noutput: %s\n\n", t.Name, t.Title, t.Description, t.Output)
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PARAMETER\tTYPE\tREQUIRED\tDEFAULT\tCONSTRAINTS\tDESCRIPTION")
	for _, p := range t.Parameters.Params() {
		def := "-"
		if p.Default != nil {
			b, _ := json.Marshal(p.Default)
			def = string(b)
		}
		fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%s\t%s\n", p.Name, p.Type, p.Required, def, dash(p.Constraints), p.Description)
	}
	return tw.Flush()
}

func runScaffold(a *app, args []string) error {
	fs := a.newFlags("scaffold run")
	var sets setFlags
	fs.Var(&sets, "set", "parameter as key=value (repeatable)")
	valuesFile := fs.String("values", "", "YAML or JSON file with parameter values")
	target := fs.String("target", "", "directory to write into (default: the catalog root)")
	dryRun := fs.Bool("dry-run", false, "show the planned changes and diff without writing")
	force := fs.Bool("force", false, "overwrite files that already exist with different content")
	format := fs.String("format", "text", "text|json (json implies --dry-run)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "scaffold run <template> [--set k=v ...] [--dry-run]"); err != nil {
		return err
	}
	if err := checkFormat(*format, "text", "json"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	t, err := ws.Template(pos[0])
	if err != nil {
		return err
	}
	values, err := collectValues(t, *valuesFile, sets)
	if err != nil {
		return err
	}
	res, err := scaffold.Render(t, values)
	if err != nil {
		return err
	}
	root := ws.Root
	if *target != "" {
		root = *target
	}
	plan, err := scaffold.MakePlan(res, root)
	if err != nil {
		return err
	}
	// Writing into the catalog root replaces the descriptors being overwritten;
	// writing elsewhere leaves every loaded entity in place.
	var checks []catalog.Diagnostic
	if sameDir(root, ws.Root) {
		checks = res.CheckDescriptors(ws.Catalog)
	} else {
		checks = res.CheckDescriptorsAlongside(ws.Catalog)
	}
	if *format == "json" {
		return a.printJSON(map[string]any{
			"template": t.Name, "values": res.Values, "outputDir": res.OutputDir,
			"summary": plan.Summary(), "changes": plan.Changes, "checks": checks,
		})
	}
	a.printPlan(plan, *dryRun)
	for _, d := range checks {
		fmt.Fprintln(a.stdout, "check:", d.String())
	}
	errs, _ := catalog.CountBySeverity(checks)
	if *dryRun {
		if errs > 0 {
			return &findingsError{msg: fmt.Sprintf("generated descriptor has %d error(s)", errs)}
		}
		return nil
	}
	if errs > 0 {
		return &findingsError{msg: fmt.Sprintf("refusing to scaffold: generated descriptor has %d error(s)", errs)}
	}
	n, err := scaffold.Apply(plan, *force)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "wrote %d files under %s\n", n, root)
	return nil
}

func (a *app) printPlan(p *scaffold.Plan, withDiff bool) {
	s := p.Summary()
	fmt.Fprintf(a.stdout, "plan: %d to create, %d to update, %d unchanged\n",
		s[scaffold.ActionCreate], s[scaffold.ActionUpdate], s[scaffold.ActionUnchanged])
	for _, c := range p.Changes {
		fmt.Fprintf(a.stdout, "  %-9s %s (+%d -%d)\n", c.Action, c.Path, c.Added, c.Deleted)
	}
	if !withDiff {
		return
	}
	for _, c := range p.Changes {
		if c.Diff != "" {
			fmt.Fprint(a.stdout, "\n", c.Diff)
		}
	}
	fmt.Fprintln(a.stdout)
}

// collectValues merges --values file content with --set flags (flags win),
// coercing --set strings to the types the parameter schema declares.
func collectValues(t *scaffold.Template, file string, sets []string) (map[string]any, error) {
	values := map[string]any{}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read values: %w", err)
		}
		if err := yaml.Unmarshal(data, &values); err != nil {
			return nil, fmt.Errorf("parse values %s: %w", file, err)
		}
	}
	for _, kv := range sets {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, usagef("--set expects key=value, got %q", kv)
		}
		prop, known := t.Parameters.Properties[k]
		if !known {
			values[k] = v // let schema validation report it if additionalProperties is false
			continue
		}
		cv, err := prop.CoerceString(v)
		if err != nil {
			return nil, usagef("--set %s: %v", k, err)
		}
		values[k] = cv
	}
	return values, nil
}

// sameDir reports whether two paths name the same directory (after resolving
// symlinks when possible), so scaffold checks know whether the rendered
// descriptors replace files of the loaded catalog.
func sameDir(a, b string) bool {
	ra, rb := resolveDir(a), resolveDir(b)
	if ra == rb {
		return true
	}
	ia, errA := os.Stat(ra)
	ib, errB := os.Stat(rb)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

func resolveDir(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
