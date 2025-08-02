package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const fixtureRoot = "../../fixtures/acme-monorepo"

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestCommands(t *testing.T) {
	r := "--root=" + fixtureRoot
	tests := []struct {
		args     []string
		code     int
		contains []string
	}{
		{[]string{"version"}, ExitOK, []string{"paved " + Version}},
		{[]string{r, "catalog", "validate"}, ExitFindings, []string{"dangling-ref", "29 entities, 65 relations: 1 errors, 0 warnings"}},
		{[]string{r, "catalog", "validate", "--format", "json"}, ExitFindings, []string{`"code": "dangling-ref"`}},
		{[]string{r, "catalog", "list", "--kind", "api"}, ExitOK, []string{"api:payments-api", "5 entities"}},
		{[]string{r, "catalog", "list", "--owner", "team-platform", "--system", "platform-core", "--format", "json"}, ExitOK, []string{`"name": "auth-service"`}},
		{[]string{r, "catalog", "list", "--type", "database", "--lifecycle", "", "--tag", ""}, ExitOK, []string{"3 entities"}},
		{[]string{r, "catalog", "get", "payments-service"}, ExitOK, []string{"component:default/payments-service", "providesApi", "api:payments-api", "tags:"}},
		{[]string{r, "catalog", "get", "api:ledger-api", "--format", "json"}, ExitOK, []string{`"relations"`}},
		{[]string{r, "catalog", "orphans"}, ExitOK, []string{"empty-group", "group:team-security", "6 orphan findings", "(declared: team-reporting)"}},
		{[]string{r, "catalog", "orphans", "--format", "json"}, ExitOK, []string{`"reason": "unprovided-api"`}},
		{[]string{r, "catalog", "export"}, ExitOK, []string{"flowchart LR"}},
		{[]string{r, "catalog", "export", "--format", "dot"}, ExitOK, []string{"digraph catalog"}},
		{[]string{r, "catalog", "stats"}, ExitOK, []string{"entities: 29", "Component  9"}},
		{[]string{r, "graph", "owner", "recommendations-ml"}, ExitOK, []string{"group:default/team-data (spec.owner)", "escalates: group:default/team-growth -> group:default/engineering"}},
		{[]string{r, "graph", "owner", "legacy-reporting"}, ExitOK, []string{"UNOWNED (declared: team-reporting (does not resolve))"}},
		{[]string{r, "graph", "owner", "group:team-data"}, ExitOK, []string{"is a group"}},
		{[]string{r, "graph", "owner", "auth-service", "--format", "json"}, ExitOK, []string{`"effective": "group:default/team-platform"`}},
		{[]string{r, "graph", "dependents", "resource:payments-db"}, ExitOK, []string{"dependents of resource:default/payments-db: 3", "via api:default/payments-api"}},
		{[]string{r, "graph", "dependents", "resource:payments-db", "--direct"}, ExitOK, []string{": 1\n"}},
		{[]string{r, "graph", "dependencies", "checkout-web", "--depth", "1", "--format", "json"}, ExitOK, []string{`"depth": 1`}},
		{[]string{r, "graph", "impact", "ledger-service"}, ExitOK, []string{"4 dependents across 2 teams", "(unowned)", "component:legacy-reporting"}},
		{[]string{r, "graph", "impact", "ledger-service", "--format", "json"}, ExitOK, []string{`"teams"`}},
		{[]string{r, "graph", "path", "checkout-web", "resource:ledger-db"}, ExitOK, []string{"component:checkout-web -> component:payments-service -> component:ledger-service -> resource:ledger-db"}},
		{[]string{r, "graph", "path", "resource:ledger-db", "checkout-web"}, ExitFindings, nil},
		{[]string{r, "graph", "owned", "team-growth", "--recursive"}, ExitOK, []string{"owns 6 entities (including sub-groups)", "component:recommendations-ml"}},
		{[]string{r, "scaffold", "list"}, ExitOK, []string{"go-service", "python-worker"}},
		{[]string{r, "scaffold", "show", "go-service"}, ExitOK, []string{"withDatabase", "range 1024..65535", `["go","service"]`}},
		{[]string{r, "scaffold", "run", "go-service", "--dry-run", "--set", "name=orders-api", "--set", "description=Accepts and tracks orders.", "--set", "owner=team-growth", "--set", "port=9000"},
			ExitOK, []string{"plan: 7 to create, 0 to update, 0 unchanged", "+PORT=9000 go run ."}},
		{[]string{r, "scaffold", "run", "go-service", "--dry-run", "--set", "name=orders-api", "--set", "description=Accepts and tracks orders.", "--set", "owner=team-ghost"},
			ExitFindings, []string{"check: services/orders-api/catalog-info.yaml:11: error [dangling-ref]"}},
		{[]string{r, "scaffold", "run", "go-service", "--format", "json", "--set", "name=orders-api", "--set", "description=Accepts and tracks orders.", "--set", "owner=team-growth"},
			ExitOK, []string{`"outputDir": "services/orders-api"`}},
		{[]string{r, "score", "list"}, ExitOK, []string{"production-readiness", "bronze>=50"}},
		{[]string{r, "score", "run", "production-readiness"}, ExitOK, []string{"Mean score: 78.9", "component:checkout-web"}},
		{[]string{r, "score", "run", "production-readiness", "--details"}, ExitOK, []string{"x readme: readme.words = 7, want >= 60"}},
		{[]string{r, "score", "run", "production-readiness", "--format", "md"}, ExitOK, []string{"## Team rollup"}},
		{[]string{r, "score", "run", "hygiene", "--format", "json"}, ExitOK, []string{`"scorecard": "hygiene"`, `"rollup"`}},
		{[]string{r, "score", "run", "production-readiness", "--min-score", "50"}, ExitFindings, nil},
		{[]string{r, "score", "run", "production-readiness", "--min-score", "40"}, ExitOK, nil},
		{[]string{r, "score", "rollup", "production-readiness"}, ExitOK, []string{"group:engineering", "  group:team-growth"}},
		{[]string{r, "score", "rollup", "hygiene", "--format", "json"}, ExitOK, []string{`"team": "(unowned)"`}},
		{[]string{r, "score", "facts", "payments-service"}, ExitOK, []string{"deps.libyears", "1.09"}},
		{[]string{r, "score", "facts", "payments-service", "--format", "json"}, ExitOK, []string{`"ci.present": true`}},
		{[]string{r, "score", "deps", "ledger-service"}, ExitOK, []string{"registry snapshot 2026-10-01", "example.com/pgdriver", "libyears 5.06"}},
		{[]string{r, "score", "deps", "ledger-service", "--format", "json"}, ExitOK, []string{`"outdated": 3`}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args[1:], " "), func(t *testing.T) {
			code, out, errOut := run(t, tt.args...)
			if code != tt.code {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.code, out, errOut)
			}
			for _, c := range tt.contains {
				if !strings.Contains(out, c) {
					t.Errorf("stdout missing %q:\n%s", c, out)
				}
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	r := "--root=" + fixtureRoot
	tests := []struct {
		args []string
		code int
		msg  string
	}{
		{nil, ExitUsage, "Usage: paved"},
		{[]string{"help"}, ExitOK, "Commands:"},
		{[]string{"-h"}, ExitOK, "Commands:"},
		{[]string{"--bogus"}, ExitUsage, "flag provided but not defined"},
		{[]string{"nope"}, ExitUsage, `unknown command "nope"`},
		{[]string{"catalog"}, ExitUsage, "catalog needs a subcommand"},
		{[]string{"catalog", "help"}, ExitOK, "Subcommands:"},
		{[]string{"catalog", "nope"}, ExitUsage, `unknown command "catalog nope"`},
		{[]string{r, "catalog", "get"}, ExitUsage, "usage: paved catalog get <ref>"},
		{[]string{r, "catalog", "list", "--format", "xml"}, ExitUsage, "--format must be one of json, text"},
		{[]string{r, "catalog", "list", "--kind", "domain"}, ExitUsage, `unsupported kind "domain"`},
		{[]string{r, "catalog", "list", "--owner", "bad name"}, ExitUsage, "invalid entity reference"},
		{[]string{r, "catalog", "list", "--system", "bad name"}, ExitUsage, "invalid entity reference"},
		{[]string{r, "catalog", "get", "ghost"}, ExitError, "entity not found: ghost"},
		{[]string{r, "catalog", "validate", "--help"}, ExitOK, "-strict"},
		{[]string{r, "graph", "dependents", "x", "--depth", "-1"}, ExitUsage, "--depth must be >= 0"},
		{[]string{r, "graph", "owned", "component:x"}, ExitUsage, "is not a group"},
		{[]string{r, "graph", "owned", "ghost-team"}, ExitError, "entity not found"},
		{[]string{r, "graph", "owned", "Bad Name"}, ExitUsage, "invalid entity reference"},
		{[]string{r, "graph", "path", "a"}, ExitUsage, "usage: paved graph path"},
		{[]string{r, "scaffold", "show", "nope"}, ExitError, `template "nope" not found`},
		{[]string{r, "scaffold", "run", "go-service", "--set", "novalue"}, ExitUsage, "--set expects key=value"},
		{[]string{r, "scaffold", "run", "go-service", "--set", "port=abc"}, ExitUsage, `"abc" is not an integer`},
		{[]string{r, "scaffold", "run", "go-service", "--set", "name=X", "--set", "unknown=1"}, ExitUsage, `property "unknown" is not allowed`},
		{[]string{r, "scaffold", "run", "go-service", "--values", "missing.yaml"}, ExitError, "read values"},
		{[]string{r, "score", "run", "nope"}, ExitError, `scorecard "nope" not found`},
		{[]string{"--root=does-not-exist", "catalog", "stats"}, ExitError, "catalog root"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			code, out, errOut := run(t, tt.args...)
			if code != tt.code {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.code, out, errOut)
			}
			if !strings.Contains(errOut, tt.msg) {
				t.Errorf("stderr missing %q:\n%s", tt.msg, errOut)
			}
		})
	}
}

func TestScaffoldApplyAndConflict(t *testing.T) {
	target := t.TempDir()
	values := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(values, []byte("name: audit-worker\nowner: team-platform\ntopic: audit.events\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--root=" + fixtureRoot, "scaffold", "run", "python-worker", "--values", values, "--target", target}
	code, out, errOut := run(t, args...)
	if code != ExitOK || !strings.Contains(out, "wrote 5 files under "+target) {
		t.Fatalf("apply: %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(target, "services", "audit-worker", "worker.py")); err != nil {
		t.Fatal(err)
	}
	// Re-running with a different value conflicts unless --force.
	code, _, errOut = run(t, append(args, "--set", "concurrency=2")...)
	if code != ExitError || !strings.Contains(errOut, "refusing to overwrite") {
		t.Fatalf("conflict: %d %s", code, errOut)
	}
	code, out, _ = run(t, append(args, "--set", "concurrency=2", "--force")...)
	if code != ExitOK || !strings.Contains(out, "wrote 1 files") {
		t.Fatalf("force: %d %s", code, out)
	}
	// Invalid generated descriptors block a real (non dry-run) apply.
	code, _, errOut = run(t, "--root="+fixtureRoot, "scaffold", "run", "python-worker", "--target", target,
		"--set", "name=other-worker", "--set", "owner=team-ghost", "--set", "topic=a.b")
	if code != ExitFindings || !strings.Contains(errOut, "refusing to scaffold") {
		t.Fatalf("invalid owner: %d %s", code, errOut)
	}
}

func TestScaffoldForceUpdatesExistingService(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(fixtureRoot)); err != nil {
		t.Fatal(err)
	}
	args := []string{"--root=" + root, "scaffold", "run", "go-service",
		"--set", "name=ledger-service", "--set", "description=Regenerated double-entry ledger.", "--set", "owner=team-payments"}
	// The dry run diffs against the existing service without flagging the
	// descriptor it replaces as a duplicate of itself.
	code, out, errOut := run(t, append(args, "--dry-run")...)
	if code != ExitOK || strings.Contains(out, "check:") || !strings.Contains(out, "update    services/ledger-service/catalog-info.yaml") {
		t.Fatalf("dry run: %d\n%s\n%s", code, out, errOut)
	}
	// Without --force the differing files conflict; with it they are written.
	if code, _, errOut = run(t, args...); code != ExitError || !strings.Contains(errOut, "refusing to overwrite") {
		t.Fatalf("conflict: %d %s", code, errOut)
	}
	// --target spelled differently but naming the catalog root still replaces.
	code, out, errOut = run(t, append(args, "--force", "--target", root+string(filepath.Separator)+".")...)
	if code != ExitOK || !strings.Contains(out, "wrote ") {
		t.Fatalf("force: %d\n%s\n%s", code, out, errOut)
	}
	b, err := os.ReadFile(filepath.Join(root, "services", "ledger-service", "catalog-info.yaml"))
	if err != nil || !strings.Contains(string(b), "Regenerated double-entry ledger.") {
		t.Fatalf("descriptor not overwritten: %v\n%s", err, b)
	}
	code, out, _ = run(t, "--root="+root, "catalog", "get", "ledger-service")
	if code != ExitOK || !strings.Contains(out, "Regenerated double-entry ledger.") {
		t.Fatalf("reloaded catalog: %d\n%s", code, out)
	}
	// A second forced run is a no-op.
	code, out, _ = run(t, append(args, "--force")...)
	if code != ExitOK || !strings.Contains(out, "wrote 0 files") {
		t.Fatalf("idempotent force: %d\n%s", code, out)
	}
	// Written into a different target, the same name duplicates the loaded entity.
	code, out, errOut = run(t, append(args, "--dry-run", "--target", t.TempDir())...)
	if code != ExitFindings || !strings.Contains(out, "[duplicate-entity]") {
		t.Fatalf("alongside: %d\n%s\n%s", code, out, errOut)
	}
}

func TestValidateStrictAndJSONShape(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "catalog-info.yaml"),
		[]byte("apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: g, tags: [Bad_Tag]}\nspec: {type: team}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "--root="+dir, "catalog", "validate"); code != ExitOK {
		t.Fatalf("warnings only should pass, got %d", code)
	}
	if code, _, _ := run(t, "--root="+dir, "catalog", "validate", "--strict"); code != ExitFindings {
		t.Fatalf("--strict should fail, got %d", code)
	}
	_, out, _ := run(t, "--root="+dir, "catalog", "validate", "--format", "json")
	var v struct {
		Stats struct {
			Warnings int `json:"warnings"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Stats.Warnings != 1 {
		t.Fatalf("json = %v %s", err, out)
	}
}

func TestEnvRootAndLogLevel(t *testing.T) {
	t.Setenv("PAVED_ROOT", fixtureRoot)
	code, out, errOut := run(t, "--log-level", "debug", "catalog", "stats")
	if code != ExitOK || !strings.Contains(out, "entities: 29") || !strings.Contains(errOut, "parsed descriptor") {
		t.Fatalf("env root: %d\n%s\n%s", code, out, errOut)
	}
	// An invalid level falls back to warn instead of failing.
	if code, _, _ := run(t, "--log-level", "loud", "catalog", "stats"); code != ExitOK {
		t.Fatalf("bad level exit = %d", code)
	}
}

func TestServeLifecycle(t *testing.T) {
	// Bad listen address: the server fails to start and the error surfaces.
	if code, _, errOut := run(t, "--root="+fixtureRoot, "serve", "--addr", "256.0.0.1:bad"); code != ExitError {
		t.Fatalf("bad addr exit = %d %s", code, errOut)
	}
	if code, _, _ := run(t, "--root=does-not-exist", "serve"); code != ExitError {
		t.Fatalf("bad root exit = %d", code)
	}
	// SIGINT triggers a graceful shutdown and a clean exit.
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	code, _, errOut := run(t, "--root="+fixtureRoot, "serve", "--addr", "127.0.0.1:0")
	if code != ExitOK || !strings.Contains(errOut, "listening") {
		t.Fatalf("serve exit = %d\n%s", code, errOut)
	}
}
