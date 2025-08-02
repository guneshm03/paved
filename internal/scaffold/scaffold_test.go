package scaffold

import (
	"errors"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/guneshm03/paved/internal/catalog"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	fixtureRoot  = "../../fixtures/acme-monorepo"
	templatesDir = fixtureRoot + "/.paved/templates"
)

func loadTemplate(t *testing.T, name string) *Template {
	t.Helper()
	ts, err := LoadTemplates(templatesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range ts {
		if tpl.Name == name {
			return tpl
		}
	}
	t.Fatalf("template %s not found", name)
	return nil
}

// assertGolden compares rendered files with testdata/golden/<name>/ exactly:
// same file set, same bytes. With -update it rewrites the directory.
func assertGolden(t *testing.T, name string, res *Result) {
	t.Helper()
	dir := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		for _, f := range res.Files {
			p := filepath.Join(dir, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, f.Content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	var onDisk []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		onDisk = append(onDisk, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("read golden dir (run go test -update): %v", err)
	}
	var rendered []string
	for _, f := range res.Files {
		rendered = append(rendered, f.Path)
		want, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Errorf("golden missing %s (run go test -update)", f.Path)
			continue
		}
		if string(want) != string(f.Content) {
			t.Errorf("%s differs from golden:\n--- got ---\n%s\n--- want ---\n%s", f.Path, f.Content, want)
		}
	}
	sort.Strings(onDisk)
	if strings.Join(onDisk, ",") != strings.Join(rendered, ",") {
		t.Errorf("file set = %v, golden = %v", rendered, onDisk)
	}
}

func TestGoldenRenders(t *testing.T) {
	tests := []struct {
		golden, template, outDir string
		values                   map[string]any
	}{
		{"go-service-minimal", "go-service", "services/orders-api", map[string]any{
			"name": "orders-api", "description": "Accepts and tracks customer orders.", "owner": "team-growth",
		}},
		{"go-service-full", "go-service", "services/invoice-service", map[string]any{
			"name": "invoice-service", "description": "Generates and sends customer invoices.", "owner": "team-payments",
			"system": "billing", "port": 9090, "withDatabase": true, "goVersion": "1.23",
			"oncallUrl": "https://oncall.acme.example/schedules/payments", "tags": []any{"go", "payments", "billing"},
		}},
		{"python-worker", "python-worker", "services/audit-worker", map[string]any{
			"name": "audit-worker", "owner": "team-platform", "topic": "audit.events", "concurrency": 8,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			res, err := Render(loadTemplate(t, tt.template), tt.values)
			if err != nil {
				t.Fatal(err)
			}
			if res.OutputDir != tt.outDir {
				t.Fatalf("output dir = %s, want %s", res.OutputDir, tt.outDir)
			}
			assertGolden(t, tt.golden, res)
			// Rendering is deterministic.
			again, err := Render(loadTemplate(t, tt.template), tt.values)
			if err != nil {
				t.Fatal(err)
			}
			for i := range res.Files {
				if string(res.Files[i].Content) != string(again.Files[i].Content) {
					t.Fatalf("non-deterministic render of %s", res.Files[i].Path)
				}
			}
			// Every generated catalog descriptor is valid against the fixture catalog.
			cat, err := catalog.Load(fixtureRoot, catalog.LoadOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if ds := res.CheckDescriptors(cat); len(ds) != 0 {
				t.Fatalf("generated descriptors have diagnostics: %v", ds)
			}
		})
	}
}

func TestConditionalDirectory(t *testing.T) {
	tpl := loadTemplate(t, "go-service")
	base := map[string]any{"name": "svc-a", "description": "A sample service here.", "owner": "team-growth"}
	res, err := Render(tpl, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		if strings.HasPrefix(f.Path, "migrations/") {
			t.Fatalf("migrations rendered without withDatabase: %s", f.Path)
		}
	}
	base["withDatabase"] = true
	res, err = Render(tpl, base)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range res.Files {
		found = found || f.Path == "migrations/0001_init.sql"
	}
	if !found {
		t.Fatal("migrations/0001_init.sql missing with withDatabase=true")
	}
}

func TestParamErrors(t *testing.T) {
	tpl := loadTemplate(t, "go-service")
	_, err := Render(tpl, map[string]any{"name": "Bad Name", "port": 80, "extra": true})
	var pe *ParamError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v", err)
	}
	var got []string
	for _, e := range pe.Errors {
		got = append(got, e.Path)
	}
	if strings.Join(got, " ") != " /description /name /owner /port" {
		t.Fatalf("error paths = %q (%v)", strings.Join(got, " "), pe)
	}
	if !strings.Contains(pe.Error(), `property "extra" is not allowed`) {
		t.Fatalf("message = %s", pe.Error())
	}
}

func TestCheckDescriptorsFindsUnknownOwner(t *testing.T) {
	cat, err := catalog.Load(fixtureRoot, catalog.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	codesOf := func(ds []catalog.Diagnostic) string {
		var codes []string
		for _, d := range ds {
			codes = append(codes, d.Code)
		}
		return strings.Join(codes, ",")
	}
	// A name already declared in a different descriptor is a real duplicate.
	res, err := Render(loadTemplate(t, "go-service"), map[string]any{
		"name": "payments-sdk", "description": "Collides with an existing entity.", "owner": "team-ghost",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := codesOf(res.CheckDescriptors(cat)); got != "duplicate-entity" {
		// The duplicate is rejected before its references are resolved.
		t.Fatalf("codes = %v", got)
	}
	// Re-rendering an existing service overwrites its descriptor, so the old
	// declaration is replaced, not duplicated; its references are still checked.
	res, err = Render(loadTemplate(t, "go-service"), map[string]any{
		"name": "ledger-service", "description": "Replaces the existing descriptor.", "owner": "team-ghost",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := codesOf(res.CheckDescriptors(cat)); got != "dangling-ref" {
		t.Fatalf("replacing codes = %v", got)
	}
	// Written alongside (another target root), the same file is a duplicate.
	if got := codesOf(res.CheckDescriptorsAlongside(cat)); got != "duplicate-entity" {
		t.Fatalf("alongside codes = %v", got)
	}
	res, err = Render(loadTemplate(t, "go-service"), map[string]any{
		"name": "new-svc", "description": "Owner does not exist yet.", "owner": "team-ghost",
	})
	if err != nil {
		t.Fatal(err)
	}
	ds := res.CheckDescriptors(cat)
	if len(ds) != 1 || ds[0].Code != "dangling-ref" || ds[0].File != "services/new-svc/catalog-info.yaml" || ds[0].Line != 11 {
		t.Fatalf("diagnostics = %v", ds)
	}
	// Without a catalog, only the generated files are considered.
	if ds := res.CheckDescriptors(nil); len(ds) != 1 {
		t.Fatalf("standalone diagnostics = %v", ds)
	}
}

func TestDryRunPlanAgainstFixture(t *testing.T) {
	res, err := Render(loadTemplate(t, "go-service"), map[string]any{
		"name": "payments-service", "description": "Authorises and captures card payments.", "owner": "team-payments",
		"system": "billing", "oncallUrl": "https://oncall.acme.example/schedules/payments",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := MakePlan(res, fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, c := range plan.Changes {
		b.WriteString(string(c.Action) + " " + c.Path + "\n")
		b.WriteString(c.Diff)
	}
	golden := filepath.Join("testdata", "golden", "dry-run-payments-service.diff")
	if *update {
		if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Fatalf("dry-run plan differs from golden:\n%s", b.String())
	}
	s := plan.Summary()
	if s[ActionCreate] != 2 || s[ActionUpdate] != 4 || s[ActionUnchanged] != 1 {
		t.Fatalf("summary = %v", s)
	}
	if len(plan.Conflicts()) != 4 {
		t.Fatalf("conflicts = %v", plan.Conflicts())
	}
	// Diffing against an existing service must not report the service's own
	// current descriptor as a duplicate of the one replacing it.
	cat, err := catalog.Load(fixtureRoot, catalog.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ds := res.CheckDescriptors(cat); len(ds) != 0 {
		t.Fatalf("checks = %v", ds)
	}
}

func TestApply(t *testing.T) {
	tpl := loadTemplate(t, "python-worker")
	values := map[string]any{"name": "wk1", "owner": "team-platform", "topic": "a.b"}
	res, err := Render(tpl, values)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	plan, err := MakePlan(res, root)
	if err != nil {
		t.Fatal(err)
	}
	n, err := Apply(plan, false)
	if err != nil || n != len(res.Files) {
		t.Fatalf("apply = %d, %v", n, err)
	}
	// Re-planning the same render is a no-op.
	plan, _ = MakePlan(res, root)
	if s := plan.Summary(); s[ActionUnchanged] != len(res.Files) {
		t.Fatalf("second plan = %v", s)
	}
	// A different value produces an update that requires --force.
	values["concurrency"] = 9
	res2, _ := Render(tpl, values)
	plan, _ = MakePlan(res2, root)
	if _, err := Apply(plan, false); err == nil {
		t.Fatal("expected conflict")
	} else {
		var ce *ConflictError
		if !errors.As(err, &ce) || len(ce.Paths) != 1 || !strings.Contains(ce.Error(), "--force") {
			t.Fatalf("conflict = %v", err)
		}
	}
	if plan.Changes[len(plan.Changes)-1].Diff == "" {
		t.Fatal("update must carry a diff")
	}
	if n, err := Apply(plan, true); err != nil || n != 1 {
		t.Fatalf("forced apply = %d, %v", n, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "services/wk1/worker.py"))
	if !strings.Contains(string(data), "CONCURRENCY = 9") {
		t.Fatalf("worker.py not updated:\n%s", data)
	}
	// Binary content is reported but not diffed.
	if err := os.WriteFile(filepath.Join(root, "services/wk1/worker.py"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	plan, _ = MakePlan(res2, root)
	for _, c := range plan.Changes {
		if c.Path == "services/wk1/worker.py" && (!c.Binary || c.Diff != "") {
			t.Fatalf("binary change = %+v", c)
		}
	}
}

// writeTemplate creates a template directory with the given files.
func writeTemplate(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const minimalTemplate = `apiVersion: paved.dev/v1
kind: Template
metadata: {name: t}
spec:
  output: "%s"
  parameters:
    type: object
    properties:
      x: {type: string}
`

func TestTemplateErrors(t *testing.T) {
	tmpl := func(output string) string { return strings.Replace(minimalTemplate, "%s", output, 1) }
	loadErrs := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no file", map[string]string{"skeleton/a": ""}, "read template"},
		{"bad yaml", map[string]string{"template.yaml": "a: [", "skeleton/a": ""}, "parse"},
		{"wrong kind", map[string]string{"template.yaml": "apiVersion: paved.dev/v1\nkind: Scorecard\n", "skeleton/a": ""}, "kind Template"},
		{"no name", map[string]string{"template.yaml": "apiVersion: paved.dev/v1\nkind: Template\nspec: {parameters: {type: object}}\n", "skeleton/a": ""}, "metadata.name"},
		{"no params", map[string]string{"template.yaml": "apiVersion: paved.dev/v1\nkind: Template\nmetadata: {name: t}\n", "skeleton/a": ""}, "spec.parameters is required"},
		{"bad schema", map[string]string{"template.yaml": "apiVersion: paved.dev/v1\nkind: Template\nmetadata: {name: t}\nspec: {parameters: {type: object, foo: 1}}\n", "skeleton/a": ""}, "unsupported keyword"},
		{"not object", map[string]string{"template.yaml": "apiVersion: paved.dev/v1\nkind: Template\nmetadata: {name: t}\nspec: {parameters: {type: string}}\n", "skeleton/a": ""}, "type object"},
		{"no skeleton", map[string]string{"template.yaml": tmpl(".")}, "missing skeleton/"},
		{"bad output", map[string]string{"template.yaml": tmpl("{{ .x "), "skeleton/a": ""}, "spec.output"},
	}
	for _, tt := range loadErrs {
		_, err := LoadTemplate(writeTemplate(t, tt.files))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}

	renderErrs := []struct {
		name   string
		output string
		files  map[string]string
		values map[string]any
		want   string
	}{
		{"escaping output", "../{{ .x }}", map[string]string{"skeleton/a": ""}, map[string]any{"x": "y"}, "unsafe path"},
		{"escaping file", ".", map[string]string{"skeleton/{{ .x }}/a": ""}, map[string]any{"x": ".."}, "unsafe path"},
		{"absolute output", "/{{ .x }}", map[string]string{"skeleton/a": ""}, map[string]any{"x": "etc"}, "unsafe path"},
		{"undeclared key", ".", map[string]string{"skeleton/a.tmpl": "{{ .missing }}"}, map[string]any{}, "missing"},
		{"bad template body", ".", map[string]string{"skeleton/a.tmpl": "{{ if }}"}, map[string]any{}, "a.tmpl"},
		{"duplicate output path", ".", map[string]string{"skeleton/a.tmpl": "1", "skeleton/a": "2"}, map[string]any{}, "both render to a"},
		{"bad func arg", ".", map[string]string{"skeleton/a.tmpl": "{{ join \",\" .x }}"}, map[string]any{"x": "s"}, "expected a list"},
	}
	for _, tt := range renderErrs {
		files := map[string]string{"template.yaml": tmpl(tt.output)}
		for k, v := range tt.files {
			files[k] = v
		}
		tpl, err := LoadTemplate(writeTemplate(t, files))
		if err != nil {
			t.Fatalf("%s: load: %v", tt.name, err)
		}
		_, err = Render(tpl, tt.values)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}

func TestLoadTemplatesDir(t *testing.T) {
	ts, err := LoadTemplates(templatesDir)
	if err != nil || len(ts) != 2 || ts[0].Name != "go-service" || ts[1].Name != "python-worker" {
		t.Fatalf("templates = %v, %v", ts, err)
	}
	if ts, err := LoadTemplates("does-not-exist"); err != nil || ts != nil {
		t.Fatalf("missing dir = %v, %v", ts, err)
	}
	dup := t.TempDir()
	for _, d := range []string{"one", "two"} {
		writeAt(t, filepath.Join(dup, d, "template.yaml"), strings.Replace(minimalTemplate, "%s", ".", 1))
		writeAt(t, filepath.Join(dup, d, "skeleton", "f"), "")
	}
	writeAt(t, filepath.Join(dup, "not-a-template", "readme"), "")
	writeAt(t, filepath.Join(dup, "file.txt"), "")
	if _, err := LoadTemplates(dup); err == nil || !strings.Contains(err.Error(), "defined in both") {
		t.Fatalf("duplicate err = %v", err)
	}
	bad := t.TempDir()
	writeAt(t, filepath.Join(bad, "x", "template.yaml"), "kind: nope")
	if _, err := LoadTemplates(bad); err == nil {
		t.Fatal("expected invalid template error")
	}
}

func writeAt(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFuncs(t *testing.T) {
	cases := map[string]string{
		`{{ kebab "PaymentsAPI v2" }}`:            "payments-api-v2",
		`{{ snake "payments-api" }}`:              "payments_api",
		`{{ camel "payments-api" }}`:              "paymentsApi",
		`{{ pascal "payments_api" }}`:             "PaymentsApi",
		`{{ title "payments-api" }}`:              "Payments Api",
		`{{ upper "a" }}{{ lower "B" }}`:          "Ab",
		`{{ trim "  x " }}`:                       "x",
		`{{ replace "-" "." "a-b" }}`:             "a.b",
		`{{ hasPrefix "a" "abc" }}`:               "true",
		`{{ contains "b" "abc" }}`:                "true",
		`{{ quote "x" }}`:                         `"x"`,
		`{{ join "," .list }}`:                    "a,1",
		`{{ join "," .strs }}`:                    "p,q",
		`{{ join "," .nothing }}`:                 "",
		`{{ default "d" .empty }}`:                "d",
		`{{ default "d" .emptyList }}`:            "d",
		`{{ default "d" .nothing }}`:              "d",
		`{{ default "d" "v" }}`:                   "v",
		`{{ indent 2 "a\n\nb" }}`:                 "  a\n\n  b",
		`{{ toInt .num }}{{ toInt 3 }}`:           "83",
		`{{ toInt "12" }}`:                        "12",
		`{{ has "a" .list }} {{ has "z" .list }}`: "true false",
		`{{ has "a" .strs }}`:                     "false",
		`{{ title "" }}`:                          "",
	}
	vals := map[string]any{
		"list": []any{"a", 1}, "strs": []string{"p", "q"}, "empty": "", "emptyList": []any{}, "nothing": nil, "num": 8.0,
	}
	for src, want := range cases {
		got, err := renderString("t", src, vals)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", src, got, err, want)
		}
	}
	if _, err := renderString("t", `{{ toInt .list }}`, vals); err == nil {
		t.Error("toInt of a list should fail")
	}
}

func TestSafeRel(t *testing.T) {
	for p, ok := range map[string]bool{
		"a/b": true, "./a": true, ".": true, "a/../b": true,
		"": false, "/abs": false, "..": false, "../x": false, "a/../../x": false, `a\b`: false,
	} {
		if _, got := safeRel(p); got != ok {
			t.Errorf("safeRel(%q) = %v, want %v", p, got, ok)
		}
	}
}

// TestGeneratedGoServiceBuildsAndPasses renders go-service into a temp dir and
// runs `go vet` and `go test` on the result, proving the skeleton is a working
// service rather than just text. Skipped with -short or without a go toolchain.
func TestGeneratedGoServiceBuildsAndPasses(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	res, err := Render(loadTemplate(t, "go-service"), map[string]any{
		"name": "orders-api", "description": "Accepts and tracks customer orders.", "owner": "team-growth",
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	plan, err := MakePlan(res, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan, false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "services", "orders-api")
	for _, args := range [][]string{{"vet", "./..."}, {"test", "./..."}} {
		cmd := exec.Command(goBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v failed: %v\n%s", args, err, out)
		}
	}
}
