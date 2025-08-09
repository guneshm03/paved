package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureRoot = "../../fixtures/acme-monorepo"

func TestOpenFixture(t *testing.T) {
	w, err := Open(fixtureRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Scorecards) != 2 || len(w.Templates) != 2 || w.Registry == nil || len(w.Catalog.Entities()) != 29 {
		t.Fatalf("workspace = %+v", w)
	}
	if _, err := w.Scorecard("hygiene"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Template("go-service"); err != nil {
		t.Fatal(err)
	}
	var nf *NotFoundError
	if _, err := w.Scorecard("x"); !errors.As(err, &nf) || nf.Error() != `scorecard "x" not found` {
		t.Fatalf("err = %v", err)
	}
	if _, err := w.Template("x"); !errors.As(err, &nf) {
		t.Fatalf("err = %v", err)
	}
	if c := w.Collector(); c.Root != fixtureRoot || c.Registry != w.Registry {
		t.Fatal("collector wiring")
	}
}

func write(t *testing.T, root, p, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigOverrides(t *testing.T) {
	root := t.TempDir()
	abs := t.TempDir()
	write(t, abs, "reg.yaml", "snapshot: 2026-01-01\npackages: {}\n")
	write(t, root, "paved.yaml", "scorecards: cards\ntemplates: tpl\nregistry: "+filepath.Join(abs, "reg.yaml")+"\ndescriptorNames: [entity.yaml]\n")
	write(t, root, "a/entity.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: g}\nspec: {type: team}\n")
	write(t, root, "b/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: ignored}\nspec: {type: team}\n")
	w, err := Open(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.Config.Scorecards != "cards" || w.Config.Templates != "tpl" || len(w.Catalog.Entities()) != 1 || w.Registry == nil {
		t.Fatalf("config = %+v entities=%d", w.Config, len(w.Catalog.Entities()))
	}
}

func TestDefaultsWithoutConfig(t *testing.T) {
	root := t.TempDir()
	cfg, err := LoadConfig(root)
	if err != nil || cfg.Scorecards != DefaultConfig.Scorecards || cfg.Registry != DefaultConfig.Registry {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	w, err := Open(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.Registry != nil || w.Scorecards != nil || w.Templates != nil {
		t.Fatalf("empty workspace = %+v", w)
	}
}

func TestOpenErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"parse paved.yaml":       {"paved.yaml": "scorecards: ["},
		"spec.rules":             {".paved/scorecards/a.yaml": "apiVersion: paved.dev/v1\nkind: Scorecard\nmetadata: {name: a}\n"},
		"kind Template":          {".paved/templates/t/template.yaml": "kind: x\n"},
		"registry snapshot date": {".paved/registry.yaml": "snapshot: x\n"},
	}
	for want, files := range cases {
		root := t.TempDir()
		for p, c := range files {
			write(t, root, p, c)
		}
		if _, err := Open(root, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
	if _, err := Open("does-not-exist", nil); err == nil {
		t.Fatal("expected error for missing root")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "paved.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("expected read error when paved.yaml is a directory")
	}
}
