package facts

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/guneshm03/paved/internal/catalog"
)

const fixtureRoot = "../../fixtures/acme-monorepo"

func fixtureCollector(t *testing.T) *Collector {
	t.Helper()
	cat, err := catalog.Load(fixtureRoot, catalog.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := LoadRegistry(filepath.Join(fixtureRoot, ".paved", "registry.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return &Collector{Root: fixtureRoot, Catalog: cat, Registry: reg}
}

func collect(t *testing.T, c *Collector, ref string) Facts {
	t.Helper()
	e, ok := c.Catalog.Get(catalog.MustParseRef(ref))
	if !ok {
		t.Fatalf("%s not in fixture", ref)
	}
	f, err := c.Collect(e)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCollectFixtureFacts(t *testing.T) {
	c := fixtureCollector(t)
	tests := map[string]map[string]any{
		"component:payments-service": {
			"ci.present": true, "ci.provider": "github-actions", "ci.workflows": 1.0, "ci.runsTests": true,
			"readme.present": true, "readme.words": 130.0, "readme.headings": 4.0,
			"oncall.link": "https://oncall.acme.example/schedules/payments", "oncall.present": true,
			"pagerduty.present": true, "techdocs.present": true, "codeowners.present": true,
			"owner.declared": true, "owner.resolved": true, "owner.effective": "group:default/team-payments", "owner.via": "spec.owner",
			"system.present": true, "api.provides": 1.0, "api.consumes": 2.0,
			"deps.present": true, "deps.manifests": []string{"go"}, "deps.total": 5.0, "deps.evaluated": 5.0, "deps.outdated": 1.0,
			"deps.outdatedRatio": 0.2, "deps.majorsBehind": 0.0, "deps.libyears": 1.09, "deps.unknown": 0.0, "deps.unpinned": 0.0,
			"entity.kind": "Component", "entity.type": "service", "entity.lifecycle": "production",
			"entity.tags": []string{"go", "payments", "pci"}, "entity.hasDescription": true,
		},
		"component:ledger-service": {
			"ci.runsTests": true, "deps.outdated": 3.0, "deps.outdatedRatio": 1.0, "deps.majorsBehind": 1.0,
			"deps.libyears": 5.06, "pagerduty.present": false, "codeowners.present": false,
		},
		"component:catalog-service": {
			"ci.present": false, "ci.provider": "none", "oncall.link": "https://oncall.acme.example/schedules/growth",
			"deps.total": 5.0, "deps.unpinned": 1.0, "deps.outdated": 3.0, "deps.evaluated": 4.0, "deps.outdatedRatio": 0.75,
		},
		"component:notification-worker": {
			"ci.present": true, "ci.provider": "gitlab-ci", "ci.runsTests": true, "readme.present": false, "readme.words": 0.0,
		},
		"component:recommendations-ml": {"ci.provider": "jenkins", "ci.runsTests": true, "owner.effective": "group:default/team-data"},
		"component:legacy-reporting": {
			"owner.declared": true, "owner.resolved": false, "owner.effective": "", "system.present": false,
			"deps.manifests": []string{"npm"}, "deps.unknown": 1.0, "deps.outdated": 1.0,
		},
		"component:checkout-web": {"ci.present": true, "ci.runsTests": false, "readme.words": 7.0, "oncall.present": false},
	}
	for ref, want := range tests {
		f := collect(t, c, ref)
		for k, v := range want {
			if !reflect.DeepEqual(f[k], v) {
				t.Errorf("%s %s = %#v, want %#v", ref, k, f[k], v)
			}
		}
	}
	if keys := collect(t, c, "component:payments-service").Keys(); keys[0] != "api.consumes" || len(keys) != 34 {
		t.Errorf("keys = %d %v", len(keys), keys)
	}
}

func TestCollectWithoutRegistry(t *testing.T) {
	c := fixtureCollector(t)
	c.Registry = nil
	f := collect(t, c, "component:ledger-service")
	if f["deps.total"] != 3.0 {
		t.Fatalf("deps.total = %v", f["deps.total"])
	}
	if _, ok := f["deps.outdated"]; ok {
		t.Fatal("freshness facts need a registry")
	}
	e, _ := c.Catalog.Get(catalog.MustParseRef("component:ledger-service"))
	if _, err := c.Dependencies(e); err == nil {
		t.Fatal("Dependencies without registry should fail")
	}
}

func TestDependenciesReport(t *testing.T) {
	c := fixtureCollector(t)
	e, _ := c.Catalog.Get(catalog.MustParseRef("component:checkout-web"))
	s, err := c.Dependencies(e)
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 5 || s.Outdated != 4 || s.MajorsBehind != 4 || s.OutdatedRatio() != 0.8 || s.Libyears != 6.52 {
		t.Fatalf("summary = %+v", s)
	}
	if s.Items[0].Name != "@acme/http-client" || s.Items[0].Latest != "1.9.3" || s.Items[0].Libyears != 1.38 {
		t.Fatalf("first item = %+v", s.Items[0])
	}
}

const testRegistry = `snapshot: 2026-01-01
packages:
  go:
    example.com/a:
      - {version: v1.0.0, released: 2024-01-01}
      - {version: v2.0.0, released: 2025-01-01}
      - {version: v2.1.0-rc.1, released: 2025-06-01}
    example.com/pre:
      - {version: v0.1.0-alpha, released: 2025-01-01}
  pypi:
    Some_Pkg:
      - {version: 1.0, released: 2024-01-01}
      - {version: 1.5, released: 2025-01-01}
`

func TestRegistryCheck(t *testing.T) {
	r, err := ParseRegistry([]byte(testRegistry))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		dep            Dependency
		status, latest string
		majors         int
		libyears       float64
	}{
		{Dependency{Ecosystem: EcosystemGo, Name: "example.com/a", Version: "v1.0.0"}, StatusOutdated, "v2.0.0", 1, 1.0},
		{Dependency{Ecosystem: EcosystemGo, Name: "example.com/a/v2", Version: "v2.0.0"}, StatusCurrent, "v2.0.0", 0, 0},
		{Dependency{Ecosystem: EcosystemGo, Name: "example.com/a/v2", Version: "v2.1.0-rc.1"}, StatusCurrent, "v2.0.0", 0, 0},
		{Dependency{Ecosystem: EcosystemGo, Name: "example.com/a", Version: "v0.5.0"}, StatusOutdated, "v2.0.0", 2, 0}, // older than any release: libyears unknown
		{Dependency{Ecosystem: EcosystemGo, Name: "example.com/pre", Version: "v0.0.1"}, StatusOutdated, "v0.1.0-alpha", 0, 0},
		{Dependency{Ecosystem: EcosystemGo, Name: "example.com/zzz", Version: "v1.0.0"}, StatusUnknown, "", 0, 0},
		{Dependency{Ecosystem: EcosystemGo, Name: "example.com/a", Version: "latest"}, StatusUnknown, "", 0, 0},
		{Dependency{Ecosystem: EcosystemPyPI, Name: "some.pkg", Version: ">=1.2"}, StatusOutdated, "1.5", 0, 1.0},
		{Dependency{Ecosystem: EcosystemPyPI, Name: "some-pkg"}, StatusUnpinned, "", 0, 0},
	}
	for _, tt := range tests {
		f := r.Check(tt.dep)
		if f.Status != tt.status || f.Latest != tt.latest || f.MajorsBehind != tt.majors || f.Libyears != tt.libyears {
			t.Errorf("Check(%+v) = %+v, want %s latest=%s majors=%d libyears=%v", tt.dep, f, tt.status, tt.latest, tt.majors, tt.libyears)
		}
	}
	if r.Snapshot.Year() != 2026 {
		t.Fatalf("snapshot = %v", r.Snapshot)
	}
	if (DependencySummary{}).OutdatedRatio() != 0 {
		t.Fatal("empty ratio")
	}
}

func TestRegistryErrors(t *testing.T) {
	for _, src := range []string{
		"snapshot: [",
		"snapshot: someday\n",
		"snapshot: 2026-01-01\npackages: {go: {x: []}}\n",
		"snapshot: 2026-01-01\npackages: {go: {x: [{version: abc, released: 2024-01-01}]}}\n",
		"snapshot: 2026-01-01\npackages: {go: {x: [{version: v1.0.0, released: soon}]}}\n",
	} {
		if _, err := ParseRegistry([]byte(src)); err == nil {
			t.Errorf("ParseRegistry(%q) should fail", src)
		}
	}
	if _, err := LoadRegistry("missing.yaml"); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestManifestParsing(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module x\n\ngo 1.24\n\nrequire example.com/one v1.0.0\n\nrequire (\n\texample.com/two v2.0.0 // indirect\n\n)\n")
	write("package.json", `{"dependencies": {"left-pad": "^1.3.0"}, "devDependencies": {"mocha": "10.0.0"}}`)
	write("requirements.txt", "-r base.txt\n# comment\nDjango[argon2]==4.2.1 ; python_version>'3'\nflask>=2\nrequests\n")
	deps, ecos, err := ReadManifests(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ecos, []Ecosystem{EcosystemGo, EcosystemNPM, EcosystemPyPI}) {
		t.Fatalf("ecosystems = %v", ecos)
	}
	var got []string
	for _, d := range deps {
		s := string(d.Ecosystem) + ":" + d.Name + "@" + d.Version
		if d.Dev {
			s += "(dev)"
		}
		got = append(got, s)
	}
	want := "go:example.com/one@v1.0.0 go:example.com/two@v2.0.0(dev) npm:left-pad@^1.3.0 npm:mocha@10.0.0(dev) pypi:django@==4.2.1 pypi:flask@>=2 pypi:requests@"
	if strings.Join(got, " ") != want {
		t.Fatalf("deps = %s\nwant %s", strings.Join(got, " "), want)
	}

	for name, bad := range map[string]string{
		"go.mod":           "require (\n\tonly-one-field\n)\n",
		"package.json":     "{not json",
		"requirements.txt": "==1.0\n",
	} {
		d := t.TempDir()
		if err := os.WriteFile(filepath.Join(d, name), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadManifests(d); err == nil {
			t.Errorf("%s: expected parse error", name)
		}
	}
}

func TestReadmeAndCIDetection(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"README.md":                "# Title\n\nTwo words.\n\n```sh\nignored code words here\n```\n## Next\n",
		".github/workflows/a.yml":  "run: echo build\n",
		".github/workflows/b.yaml": "run: make check\n",
		".github/workflows/notes":  "go test",
		"Jenkinsfile":              "sh 'echo'",
		"docs/CODEOWNERS":          "*",
	}
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := Facts{}
	if err := readmeFacts(dir, f); err != nil {
		t.Fatal(err)
	}
	if f["readme.words"] != 4.0 || f["readme.headings"] != 2.0 {
		t.Fatalf("readme facts = %v", f)
	}
	if err := ciFacts(dir, f); err != nil {
		t.Fatal(err)
	}
	if f["ci.workflows"] != 3.0 || f["ci.provider"] != "github-actions" || f["ci.runsTests"] != true {
		t.Fatalf("ci facts = %v", f)
	}
	if !anyExists(dir, "CODEOWNERS", "docs/CODEOWNERS") || anyExists(dir, "nope") {
		t.Fatal("anyExists")
	}
}
