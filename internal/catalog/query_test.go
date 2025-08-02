package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

const fixtureRoot = "../../fixtures/acme-monorepo"

func loadFixture(t testing.TB) *Catalog {
	t.Helper()
	c, err := Load(fixtureRoot, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func refs(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = fmt.Sprintf("%d:%s", h.Depth, h.Ref.Short())
	}
	return out
}

func TestFixtureLoads(t *testing.T) {
	c := loadFixture(t)
	st := c.Stats()
	if st.Entities != 29 {
		t.Fatalf("entities = %d, want 29", st.Entities)
	}
	want := map[Kind]int{KindComponent: 9, KindAPI: 5, KindSystem: 3, KindGroup: 6, KindResource: 6}
	for k, n := range want {
		if st.ByKind[k] != n {
			t.Errorf("%s = %d, want %d", k, st.ByKind[k], n)
		}
	}
	// Exactly one deliberate validation error: the disbanded owner of legacy-reporting.
	if st.Errors != 1 || st.Warnings != 0 {
		t.Fatalf("diagnostics = %v", c.Diagnostics())
	}
	d := c.Diagnostics()[0]
	if d.Code != "dangling-ref" || d.File != "services/legacy-reporting/catalog-info.yaml" || d.Line != 11 {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestWhoOwns(t *testing.T) {
	c := loadFixture(t)
	tests := []struct {
		ref, effective, via string
		chain               []string
	}{
		{"component:payments-service", "group:default/team-payments", "spec.owner", []string{"group:default/engineering"}},
		{"component:recommendations-ml", "group:default/team-data", "spec.owner", []string{"group:default/team-growth", "group:default/engineering"}},
		{"api:catalog-api", "group:default/team-growth", "spec.owner", []string{"group:default/engineering"}},
		{"resource:events-bus", "group:default/team-platform", "spec.owner", []string{"group:default/engineering"}},
		{"group:team-data", "group:default/team-data", "self", []string{"group:default/team-growth", "group:default/engineering"}},
	}
	for _, tt := range tests {
		o, err := c.OwnerOf(MustParseRef(tt.ref))
		if err != nil {
			t.Fatal(err)
		}
		if o.Effective == nil || o.Effective.String() != tt.effective || o.Via != tt.via {
			t.Errorf("OwnerOf(%s) = %+v, want %s via %s", tt.ref, o, tt.effective, tt.via)
			continue
		}
		var chain []string
		for _, r := range o.Chain {
			chain = append(chain, r.String())
		}
		if strings.Join(chain, ",") != strings.Join(tt.chain, ",") {
			t.Errorf("chain(%s) = %v, want %v", tt.ref, chain, tt.chain)
		}
	}

	o, err := c.OwnerOf(MustParseRef("component:legacy-reporting"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Effective != nil || o.Direct != nil || o.Declared != "team-reporting" {
		t.Fatalf("legacy-reporting ownership = %+v, want unowned with declared team-reporting", o)
	}
	if _, err := c.OwnerOf(MustParseRef("component:nope")); err == nil {
		t.Fatal("expected not found")
	}
}

func TestOwnershipInheritance(t *testing.T) {
	c := build(t, map[string]string{
		"g.yaml": groupA,
		"s.yaml": "apiVersion: backstage.io/v1alpha1\nkind: System\nmetadata: {name: sys}\nspec: {owner: team-a}\n",
		"c.yaml": strings.Join([]string{
			// parent's owner dangles, so ownership is inherited from the system
			"apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: parent}\nspec: {type: service, lifecycle: production, owner: gone, system: sys}\n",
			// child has a dangling owner and no system: inherits through parent -> system
			"apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: child}\nspec: {type: library, lifecycle: production, owner: gone, subcomponentOf: parent}\n",
		}, "---\n"),
	})
	o, err := c.OwnerOf(MustParseRef("component:parent"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Effective == nil || o.Effective.Name != "team-a" || o.Via != "system" || o.InheritedFrom.Name != "sys" {
		t.Fatalf("parent ownership = %+v", o)
	}
	o, err = c.OwnerOf(MustParseRef("component:child"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Effective == nil || o.Effective.Name != "team-a" || o.Via != "system" {
		t.Fatalf("child ownership = %+v", o)
	}
	// Inheritance means these are not ownership orphans, and child is in a system via its parent.
	for _, orphan := range c.FindOrphans() {
		if orphan.Ref.Name == "child" || orphan.Ref.Name == "parent" {
			t.Errorf("unexpected orphan %+v", orphan)
		}
	}
}

func TestWhatDependsOn(t *testing.T) {
	c := loadFixture(t)
	tests := []struct {
		ref  string
		opts WalkOptions
		want []string
	}{
		{"resource:payments-db", WalkOptions{Transitive: true}, []string{
			"1:component:payments-service", "2:component:checkout-web", "2:component:payments-sdk"}},
		{"resource:payments-db", WalkOptions{}, []string{"1:component:payments-service"}},
		{"component:ledger-service", WalkOptions{Transitive: true}, []string{
			"1:component:legacy-reporting", "1:component:payments-service", "2:component:checkout-web", "2:component:payments-sdk"}},
		{"component:auth-service", WalkOptions{Transitive: true, MaxDepth: 1}, []string{
			"1:component:catalog-service", "1:component:checkout-web", "1:component:payments-service"}},
		{"api:catalog-api", WalkOptions{Transitive: true}, []string{
			"1:component:checkout-web", "1:component:recommendations-ml"}},
		{"resource:legacy-ftp", WalkOptions{Transitive: true}, nil},
	}
	for _, tt := range tests {
		hits, err := c.Walk(MustParseRef(tt.ref), Upstream, tt.opts)
		if err != nil {
			t.Fatal(err)
		}
		if got := refs(hits); strings.Join(got, " ") != strings.Join(tt.want, " ") {
			t.Errorf("dependents(%s, %+v) = %v, want %v", tt.ref, tt.opts, got, tt.want)
		}
	}
	hits, _ := c.Walk(MustParseRef("resource:payments-db"), Upstream, WalkOptions{Transitive: true})
	if hits[1].Via != "api:default/payments-api" || len(hits[1].Path) != 3 {
		t.Errorf("via/path = %s %v", hits[1].Via, hits[1].Path)
	}
}

func TestDependenciesAndPath(t *testing.T) {
	c := loadFixture(t)
	hits, err := c.Walk(MustParseRef("component:checkout-web"), Downstream, WalkOptions{Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "1:component:auth-service 1:component:catalog-service 1:component:payments-service " +
		"2:component:ledger-service 2:resource:catalog-db 2:resource:events-bus 2:resource:payments-db 2:resource:sessions-cache " +
		"3:resource:ledger-db"
	if got := strings.Join(refs(hits), " "); got != want {
		t.Fatalf("dependencies = %s\nwant %s", got, want)
	}
	p, err := c.PathBetween(MustParseRef("component:checkout-web"), MustParseRef("resource:ledger-db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 4 || p[1].Name != "payments-service" || p[2].Name != "ledger-service" {
		t.Fatalf("path = %v", p)
	}
	p, err = c.PathBetween(MustParseRef("resource:ledger-db"), MustParseRef("component:checkout-web"))
	if err != nil || p != nil {
		t.Fatalf("reverse path = %v, %v", p, err)
	}
	if _, err := c.PathBetween(MustParseRef("component:checkout-web"), MustParseRef("component:none")); err == nil {
		t.Fatal("expected not found")
	}
	if _, err := c.Walk(MustParseRef("component:none"), Downstream, WalkOptions{}); err == nil {
		t.Fatal("expected not found")
	}
	// An API's downstream is its provider.
	hits, _ = c.Walk(MustParseRef("api:ledger-api"), Downstream, WalkOptions{})
	if got := strings.Join(refs(hits), " "); got != "1:component:ledger-service" {
		t.Fatalf("api downstream = %s", got)
	}
}

func TestImpact(t *testing.T) {
	c := loadFixture(t)
	im, err := c.ImpactOf(MustParseRef("component:ledger-service"))
	if err != nil {
		t.Fatal(err)
	}
	if len(im.Dependents) != 4 || len(im.Teams) != 2 {
		t.Fatalf("impact = %+v", im)
	}
	if len(im.Unowned) != 1 || im.Unowned[0].Name != "legacy-reporting" {
		t.Fatalf("unowned = %v", im.Unowned)
	}
	if got := im.Teams[MustParseRef("group:team-payments")]; len(got) != 2 {
		t.Fatalf("payments team impact = %v", got)
	}
	if _, err := c.ImpactOf(MustParseRef("component:none")); err == nil {
		t.Fatal("expected not found")
	}
}

func TestOwnedByAndFind(t *testing.T) {
	c := loadFixture(t)
	direct := c.OwnedBy(MustParseRef("group:team-growth"), false)
	all := c.OwnedBy(MustParseRef("group:team-growth"), true)
	if len(direct) != 5 || len(all) != 6 {
		t.Fatalf("owned direct=%d recursive=%d", len(direct), len(all))
	}
	if n := len(c.OwnedBy(MustParseRef("group:engineering"), true)); n != 22 {
		t.Fatalf("engineering recursive = %d, want 22", n)
	}
	if got := c.Find(Filter{Kind: KindComponent, Lifecycle: "production", Owner: MustParseRef("group:team-payments")}); len(got) != 3 {
		t.Fatalf("find = %d", len(got))
	}
	if got := c.Find(Filter{System: MustParseRef("system:billing"), Kind: KindAPI}); len(got) != 2 {
		t.Fatalf("find system apis = %d", len(got))
	}
	if got := c.Find(Filter{Tag: "pci", Type: "SERVICE"}); len(got) != 1 {
		t.Fatalf("find tag = %d", len(got))
	}
}

func TestLookup(t *testing.T) {
	c := loadFixture(t)
	e, err := c.Lookup("ledger-service")
	if err != nil || e.Kind != KindComponent {
		t.Fatalf("Lookup = %v, %v", e, err)
	}
	if _, err := c.Lookup("api:default/ledger-api"); err != nil {
		t.Fatal(err)
	}
	var nf *NotFoundError
	if _, err := c.Lookup("ghost"); !errors.As(err, &nf) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Lookup("component:ghost"); !errors.As(err, &nf) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Lookup("bad name!"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := c.Lookup("user:x"); err == nil {
		t.Fatal("expected error")
	}
	// payments-api exists only as an API, but a name shared across kinds is ambiguous.
	amb := build(t, map[string]string{"g.yaml": groupA, "c.yaml": comp("team-a", "")})
	var ae *AmbiguousRefError
	if _, err := amb.Lookup("team-a"); !errors.As(err, &ae) || len(ae.Candidates) != 2 {
		t.Fatalf("ambiguous err = %v", err)
	}
}

func TestOrphansFixture(t *testing.T) {
	c := loadFixture(t)
	var got []string
	for _, o := range c.FindOrphans() {
		got = append(got, string(o.Reason)+" "+o.Ref.Short())
	}
	want := []string{
		"empty-group group:team-security",
		"no-owner component:legacy-reporting",
		"no-system component:legacy-reporting",
		"no-system resource:legacy-ftp",
		"unprovided-api api:events-api",
		"unused-resource resource:legacy-ftp",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("orphans:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestExport(t *testing.T) {
	c := loadFixture(t)
	var dot, mm bytes.Buffer
	if err := c.WriteDOT(&dot); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteMermaid(&mm); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dot.String(), `"component:default/payments-service" -> "api:default/payments-api" [label="providesApi"]`) {
		t.Errorf("dot missing providesApi edge:\n%s", dot.String())
	}
	if strings.Contains(dot.String(), "apiProvidedBy") {
		t.Error("dot should draw only forward relations")
	}
	if !strings.HasPrefix(mm.String(), "flowchart LR\n") || !strings.Contains(mm.String(), "-->|dependsOn|") {
		t.Errorf("mermaid output unexpected:\n%s", mm.String())
	}
	// Each declared relation is drawn once.
	if n := strings.Count(dot.String(), " -> "); n != c.Stats().Edges {
		t.Errorf("dot edges = %d, want %d", n, c.Stats().Edges)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load("does-not-exist", LoadOptions{}); err == nil {
		t.Fatal("expected error for missing root")
	}
	if _, err := Load(fixtureRoot+"/paved.yaml", LoadOptions{}); err == nil {
		t.Fatal("expected error for file root")
	}
	files, err := Discover(fixtureRoot, LoadOptions{DescriptorNames: []string{"package.json"}})
	if err != nil || len(files) != 2 {
		t.Fatalf("custom names = %v, %v", files, err)
	}
}

// genCatalog builds a random layered catalog: n components spread over teams,
// each depending on up to three components of lower index (a DAG) or a
// resource, and consuming APIs of lower-index providers.
func genCatalog(n int, seed int64) *Catalog {
	rng := rand.New(rand.NewSource(seed))
	var b strings.Builder
	teams := max(n/20, 1)
	for i := range teams {
		fmt.Fprintf(&b, "---\napiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: t%d}\nspec: {type: team, members: [m]}\n", i)
	}
	for i := range n {
		var deps, consumes []string
		for range rng.Intn(4) {
			if i > 0 {
				deps = append(deps, fmt.Sprintf("component:c%d", rng.Intn(i)))
			}
		}
		if i > 0 && rng.Intn(3) == 0 {
			consumes = append(consumes, fmt.Sprintf("a%d", rng.Intn(i)))
		}
		deps = append(deps, fmt.Sprintf("resource:r%d", i%max(n/10, 1)))
		fmt.Fprintf(&b, "---\napiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: c%d}\nspec: {type: service, lifecycle: production, owner: t%d, providesApis: [a%d], consumesApis: [%s], dependsOn: [%s]}\n",
			i, i%teams, i, strings.Join(consumes, ", "), strings.Join(deps, ", "))
		fmt.Fprintf(&b, "---\napiVersion: backstage.io/v1alpha1\nkind: API\nmetadata: {name: a%d}\nspec: {type: openapi, lifecycle: production, owner: t%d, definition: x}\n", i, i%teams)
	}
	for i := range max(n/10, 1) {
		fmt.Fprintf(&b, "---\napiVersion: backstage.io/v1alpha1\nkind: Resource\nmetadata: {name: r%d}\nspec: {type: database, owner: t0}\n", i)
	}
	es, ds := ParseDescriptors("gen.yaml", []byte(b.String()))
	return Build(es, ds)
}

// Property: for every pair, b is a (transitive) dependency of a exactly when a
// is a (transitive) dependent of b, and depth-1 walks are subsets of full walks.
func TestWalkDirectionsAreMirrors(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		c := genCatalog(60, seed)
		if errs, _ := CountBySeverity(c.Diagnostics()); errs != 0 {
			t.Fatalf("seed %d: generated catalog invalid: %v", seed, c.Diagnostics()[:1])
		}
		down := map[[2]Ref]bool{}
		var comps []Ref
		for _, e := range c.Find(Filter{Kind: KindComponent}) {
			comps = append(comps, e.Ref())
		}
		for _, a := range comps {
			hits, _ := c.Walk(a, Downstream, WalkOptions{Transitive: true})
			for _, h := range hits {
				down[[2]Ref{a, h.Ref}] = true
			}
			direct, _ := c.Walk(a, Downstream, WalkOptions{})
			for _, h := range direct {
				if !down[[2]Ref{a, h.Ref}] || h.Depth != 1 {
					t.Fatalf("seed %d: direct hit %v not in transitive set", seed, h)
				}
			}
		}
		up := 0
		for _, b := range comps {
			hits, _ := c.Walk(b, Upstream, WalkOptions{Transitive: true})
			for _, h := range hits {
				if !down[[2]Ref{h.Ref, b}] {
					t.Fatalf("seed %d: %s depends on %s upstream but not downstream", seed, h.Ref, b)
				}
				up++
			}
		}
		downComp := 0
		for k := range down {
			if k[1].Kind == KindComponent {
				downComp++
			}
		}
		if up != downComp {
			t.Fatalf("seed %d: upstream pairs %d != downstream pairs %d", seed, up, downComp)
		}
	}
}

func BenchmarkBuild1000(b *testing.B) {
	for b.Loop() {
		genCatalog(1000, 42)
	}
}

func BenchmarkWalkUpstream1000(b *testing.B) {
	c := genCatalog(1000, 42)
	root := MustParseRef("resource:r0")
	b.ResetTimer()
	for b.Loop() {
		if _, err := c.Walk(root, Upstream, WalkOptions{Transitive: true}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFindOrphans1000(b *testing.B) {
	c := genCatalog(1000, 42)
	b.ResetTimer()
	for b.Loop() {
		c.FindOrphans()
	}
}
