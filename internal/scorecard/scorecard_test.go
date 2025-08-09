package scorecard

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/guneshm03/paved/internal/catalog"
	"github.com/guneshm03/paved/internal/facts"
	"go.yaml.in/yaml/v3"
)

var update = flag.Bool("update", false, "rewrite golden files")

const fixtureRoot = "../../fixtures/acme-monorepo"

func parseCond(t *testing.T, src string) (Condition, error) {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	return ParseCondition(n.Content[0])
}

func TestConditionEval(t *testing.T) {
	f := facts.Facts{
		"b": true, "n": 3.0, "s": "https://x", "empty": "", "tags": []string{"go", "pci"}, "lc": "deprecated",
	}
	tests := []struct {
		cond   string
		want   bool
		reason string
	}{
		{"{fact: b, equals: true}", true, "b = true (== true)"},
		{"{fact: b, notEquals: true}", false, "b = true, want != true"},
		{"{fact: n, gt: 2}", true, "n = 3 (> 2)"},
		{"{fact: n, gte: 4}", false, "n = 3, want >= 4"},
		{"{fact: n, lt: 3}", false, "n = 3, want < 3"},
		{"{fact: n, lte: 3}", true, "n = 3 (<= 3)"},
		{"{fact: n, equals: 3}", true, "n = 3 (== 3)"},
		{"{fact: s, gt: 1}", false, `s = "https://x", want > 1`},
		{"{fact: s, matches: '^https://'}", true, `s = "https://x" (matches "^https://")`},
		{"{fact: n, matches: '3'}", false, "n = 3, want matches"},
		{"{fact: empty, matches: '.'}", false, `empty = "", want matches "."`},
		{"{fact: missing, equals: true}", false, "missing is not available"},
		{"{fact: missing, exists: false}", true, "missing exists=false"},
		{"{fact: b, exists: false}", false, "b exists=true, want false"},
		{"{fact: lc, in: [production, deprecated]}", true, `lc = "deprecated" (in ["production", "deprecated"])`},
		{"{fact: lc, in: [production]}", false, "want in"},
		{"{fact: tags, contains: pci}", true, "tags = [go, pci] (contains"},
		{"{fact: tags, contains: sox}", false, "want contains"},
		{"{fact: s, contains: '://'}", true, "contains"},
		{"{fact: n, contains: 3}", false, "want contains"},
		{"{all: [{fact: b, equals: true}, {fact: n, gt: 5}]}", false, "n = 3, want > 5"},
		{"{all: [{fact: b, equals: true}, {fact: n, gt: 1}]}", true, "b = true (== true); n = 3 (> 1)"},
		{"{any: [{fact: b, equals: false}, {fact: n, gt: 1}]}", true, "n = 3 (> 1)"},
		{"{any: [{fact: b, equals: false}, {fact: n, gt: 5}]}", false, "none matched: "},
		{"{not: {fact: lc, equals: deprecated}}", false, `not(lc = "deprecated" (== "deprecated"))`},
	}
	for _, tt := range tests {
		c, err := parseCond(t, tt.cond)
		if err != nil {
			t.Fatalf("%s: %v", tt.cond, err)
		}
		ok, why := c.Eval(f)
		if ok != tt.want || !strings.Contains(why, tt.reason) {
			t.Errorf("%s = %v %q, want %v containing %q", tt.cond, ok, why, tt.want, tt.reason)
		}
	}
	c, _ := parseCond(t, "{all: [{fact: b, equals: true}, {any: [{fact: n, gt: 1}, {fact: b, equals: true}]}, {not: {fact: s, exists: false}}]}")
	if got := strings.Join(c.Facts(), ","); got != "b,n,s" {
		t.Fatalf("Facts() = %s", got)
	}
}

func TestConditionParseErrors(t *testing.T) {
	for src, want := range map[string]string{
		"[1]":                                   "must be a mapping",
		"{equals: true}":                        "needs 'fact'",
		"{fact: a}":                             "has no operator",
		"{fact: a, approx: 1}":                  "unknown operator",
		"{fact: a, gt: 1, lt: 2}":               "more than one operator",
		"{fact: a, gt: x}":                      "needs a number",
		"{fact: a, matches: 3}":                 "regular expression string",
		"{fact: a, matches: '('}":               "matches:",
		"{fact: a, exists: 1}":                  "true or false",
		"{fact: a, in: x}":                      "needs a list",
		"{all: [], x: 1}":                       "must be the only key",
		"{any: []}":                             "non-empty list",
		"{not: {fact: a, equals: 1}, other: 1}": "must be the only key",
		"{not: {fact: a}}":                      "has no operator",
		"{all: [{fact: a}]}":                    "has no operator",
		"{fact: a, equals: {x: [}":              "",
	} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(src), &n); err != nil {
			continue // invalid YAML is rejected before reaching the condition parser
		}
		_, err := ParseCondition(n.Content[0])
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseCondition(%s) err = %v, want %q", src, err, want)
		}
	}
}

func TestScorecardParseErrors(t *testing.T) {
	head := "apiVersion: paved.dev/v1\nkind: Scorecard\nmetadata: {name: s}\n"
	for src, want := range map[string]string{
		"a: [":             "s.yaml",
		"apiVersion: v0\n": "expected apiVersion",
		"apiVersion: paved.dev/v1\nkind: Scorecard\nmetadata: {}\nspec: {rules: [{id: a, check: {fact: x, exists: true}}]}\n": "metadata.name",
		head + "spec: {}\n": "rules must not be empty",
		head + "spec: {rules: [{check: {fact: x, exists: true}}]}":                                                                  "id is required",
		head + "spec: {rules: [{id: a, check: {fact: x, exists: true}}, {id: a, check: {fact: x, exists: true}}]}":                  "defined twice",
		head + "spec: {rules: [{id: a, weight: 0, check: {fact: x, exists: true}}]}":                                                "weight must be > 0",
		head + "spec: {rules: [{id: a}]}":                                                                                           "check is required",
		head + "spec: {rules: [{id: a, check: {fact: x}}]}":                                                                         "no operator",
		head + "spec: {levels: [{name: a, minScore: 1}, {name: a, minScore: 2}], rules: [{id: a, check: {fact: x, exists: true}}]}": "unique",
		head + "spec: {levels: [{name: a, minScore: 101}], rules: [{id: a, check: {fact: x, exists: true}}]}":                       "within 0..100",
	} {
		_, err := Parse("s.yaml", []byte(src))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) err = %v, want %q", src, err, want)
		}
	}
	sc, err := Parse("s.yaml", []byte(head+"spec:\n  levels: [{name: hi, minScore: 80}, {name: lo, minScore: 20}]\n  rules: [{id: a, check: {fact: x, exists: true}}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Title != "s" || sc.Rules[0].Title != "a" || sc.Rules[0].Weight != 1 || sc.Levels[0].Name != "lo" {
		t.Fatalf("defaults = %+v", sc)
	}
	for score, want := range map[float64]string{10: "", 20: "lo", 79.9: "lo", 80: "hi", 100: "hi"} {
		if got := sc.LevelFor(score); got != want {
			t.Errorf("LevelFor(%v) = %q, want %q", score, got, want)
		}
	}
}

func TestLoadDir(t *testing.T) {
	cards, err := LoadDir(filepath.Join(fixtureRoot, ".paved", "scorecards"))
	if err != nil || len(cards) != 2 || cards[0].Name != "hygiene" {
		t.Fatalf("LoadDir = %v, %v", cards, err)
	}
	if cards, err := LoadDir("missing"); err != nil || cards != nil {
		t.Fatalf("missing dir = %v, %v", cards, err)
	}
	dir := t.TempDir()
	card := "apiVersion: paved.dev/v1\nkind: Scorecard\nmetadata: {name: dup}\nspec: {rules: [{id: a, check: {fact: x, exists: true}}]}\n"
	for _, n := range []string{"a.yaml", "b.yml"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(card), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "defined twice") {
		t.Fatalf("dup err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yml"), []byte("kind: x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSelector(t *testing.T) {
	e := &catalog.Entity{Kind: catalog.KindComponent, Spec: catalog.Spec{Type: "service", Lifecycle: "production"},
		Metadata: catalog.Metadata{Tags: []string{"go"}}}
	for sel, want := range map[*Selector]bool{
		{}:                                     true,
		{Kinds: []string{"component"}}:         true,
		{Kinds: []string{"API"}}:               false,
		{Types: []string{"website"}}:           false,
		{Lifecycles: []string{"experimental"}}: false,
		{Tags: []string{"python", "go"}}:       true,
		{Tags: []string{"python"}}:             false,
	} {
		if got := sel.Matches(e); got != want {
			t.Errorf("%+v.Matches = %v, want %v", *sel, got, want)
		}
	}
}

func fixtureReport(t *testing.T, name string) (*Report, *catalog.Catalog) {
	t.Helper()
	cat, err := catalog.Load(fixtureRoot, catalog.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := facts.LoadRegistry(filepath.Join(fixtureRoot, ".paved", "registry.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cards, err := LoadDir(filepath.Join(fixtureRoot, ".paved", "scorecards"))
	if err != nil {
		t.Fatal(err)
	}
	ev := NewEvaluator(&facts.Collector{Root: fixtureRoot, Catalog: cat, Registry: reg})
	for _, sc := range cards {
		if sc.Name == name {
			rep, err := ev.Evaluate(sc)
			if err != nil {
				t.Fatal(err)
			}
			// Facts are cached across evaluations.
			if _, err := ev.Evaluate(sc); err != nil || len(ev.cache) == 0 {
				t.Fatalf("re-evaluate: %v", err)
			}
			return rep, cat
		}
	}
	t.Fatalf("scorecard %s missing", name)
	return nil, nil
}

func TestProductionReadinessOnFixture(t *testing.T) {
	rep, _ := fixtureReport(t, "production-readiness")
	var got []string
	for _, e := range rep.Entities {
		got = append(got, e.Ref.Short()+"="+strconv.FormatFloat(e.Score, 'f', -1, 64)+":"+levelKey(e.Level))
	}
	want := []string{
		"component:checkout-web=46.7:none", "component:catalog-service=53.3:bronze",
		"component:ledger-service=86.7:silver", "component:notification-worker=86.7:silver",
		"component:auth-service=100:gold", "component:payments-service=100:gold",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("scores = %v\nwant %v", got, want)
	}
	if rep.MeanScore != 78.9 || rep.LevelCounts["gold"] != 2 || rep.LevelCounts[NoLevel] != 1 {
		t.Fatalf("summary = %v %v", rep.MeanScore, rep.LevelCounts)
	}
	for _, r := range rep.Rules {
		if r.ID == "deps-fresh" && (r.Passed != 3 || r.Failed != 3 || r.PassRate != 50) {
			t.Fatalf("deps-fresh summary = %+v", r)
		}
	}
	// Failing rules carry their remediation.
	for _, rr := range rep.Entities[0].Rules {
		if !rr.Passed && rr.Remediation == "" {
			t.Fatalf("missing remediation on %s", rr.ID)
		}
	}
}

func TestRollupOnFixture(t *testing.T) {
	rep, cat := fixtureReport(t, "hygiene")
	rows := Rollup(rep, cat)
	var got []string
	for _, r := range rows {
		got = append(got, strings.Repeat(">", r.Depth)+r.Team+" direct="+strconv.Itoa(r.Direct.Entities)+
			" total="+strconv.Itoa(r.Total.Entities)+" mean="+strconv.FormatFloat(r.Total.MeanScore, 'f', -1, 64))
	}
	want := []string{
		"group:default/engineering direct=0 total=8 mean=68.3",
		">group:default/team-growth direct=2 total=3 mean=48.9",
		">>group:default/team-data direct=1 total=1 mean=53.3",
		">group:default/team-payments direct=3 total=3 mean=77.8",
		">group:default/team-platform direct=2 total=2 mean=83.4",
		"(unowned) direct=1 total=1 mean=26.7",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rollup:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	eng := rows[0].Total
	if eng.MinScore != 40 || eng.FailingRules[0] != (RuleCount{ID: "codeowners", Count: 6}) {
		t.Fatalf("engineering stats = %+v", eng)
	}
	if s := stats(nil); s.Entities != 0 || s.MeanScore != 0 {
		t.Fatalf("empty stats = %+v", s)
	}
}

func TestRollupHandlesGroupCycles(t *testing.T) {
	var es []*catalog.Entity
	for _, src := range []string{
		"apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: x}\nspec: {type: team, parent: y}\n",
		"apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: y}\nspec: {type: team, parent: x}\n",
	} {
		e, _ := catalog.ParseDescriptors("g.yaml", []byte(src))
		es = append(es, e...)
	}
	cat := catalog.Build(es, nil)
	rep := &Report{Entities: []EntityResult{{Ref: catalog.MustParseRef("component:c"), Owner: "group:default/x", Score: 50}}}
	rows := Rollup(rep, cat)
	if len(rows) != 1 || rows[0].Total.Entities != 1 { // terminates; y has nothing scored
		t.Fatalf("rows = %+v", rows)
	}
}

// TestReportFormatsGolden pins the text, rollup and Markdown renderings.
func TestReportFormatsGolden(t *testing.T) {
	rep, cat := fixtureReport(t, "production-readiness")
	rows := Rollup(rep, cat)
	var text, roll, md bytes.Buffer
	if err := WriteText(&text, rep, true); err != nil {
		t.Fatal(err)
	}
	if err := WriteRollupText(&roll, rep, rows); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarkdown(&md, rep, rows); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"production-readiness.txt": text.String(), "production-readiness-rollup.txt": roll.String(),
		"production-readiness.md": md.String(),
	} {
		p := filepath.Join("testdata", name)
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%v (run go test -update)", err)
		}
		if got != string(want) {
			t.Errorf("%s differs from golden:\n%s", name, got)
		}
	}
	if levelsString(map[string]int{}, nil) != "-" || topFailing(nil, 2) != "-" || mdDash("") != "_unowned_" || shortRef("not a ref!") != "not a ref!" {
		t.Fatal("format helpers")
	}
}
