package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureRoot = "../../fixtures/acme-monorepo"

func newTestServer(t *testing.T, root string) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func do(t *testing.T, ts *httptest.Server, method, path, body string) (int, map[string]any, string) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode %s: %v\n%s", path, err, raw)
		}
	}
	return resp.StatusCode, m, string(raw)
}

func items(m map[string]any) []any { v, _ := m["items"].([]any); return v }

func TestEndpoints(t *testing.T) {
	_, ts := newTestServer(t, fixtureRoot)
	tests := []struct {
		method, path, body string
		status             int
		check              func(t *testing.T, m map[string]any, raw string)
	}{
		{"GET", "/healthz", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["status"] != "ok" || m["entities"] != 29.0 {
				t.Errorf("health = %v", m)
			}
		}},
		{"GET", "/api/v1/stats", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["entities"] != 29.0 || m["errors"] != 1.0 {
				t.Errorf("stats = %v", m)
			}
		}},
		{"GET", "/api/v1/entities?kind=component&owner=team-payments", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["count"] != 3.0 {
				t.Errorf("count = %v", m["count"])
			}
		}},
		{"GET", "/api/v1/entities?system=billing&lifecycle=production&type=openapi", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["count"] != 1.0 {
				t.Errorf("count = %v", m["count"])
			}
		}},
		{"GET", "/api/v1/entities?tag=nope", "", 200, func(t *testing.T, m map[string]any, raw string) {
			if m["count"] != 0.0 || !strings.Contains(raw, `"items": []`) {
				t.Errorf("empty list = %s", raw)
			}
		}},
		{"GET", "/api/v1/entities?kind=domain", "", 400, nil},
		{"GET", "/api/v1/entities?owner=bad name", "", 400, nil},
		{"GET", "/api/v1/entities/component/default/payments-service", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["ref"] != "component:default/payments-service" || len(m["relations"].([]any)) != 8 {
				t.Errorf("entity = %v", m)
			}
		}},
		{"GET", "/api/v1/entities/Component/default/ghost", "", 404, nil},
		{"GET", "/api/v1/entities/user/default/x", "", 400, nil},
		{"GET", "/api/v1/entities/component/default/payments-sdk/owner", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["effective"] != "group:default/team-payments" || m["via"] != "spec.owner" {
				t.Errorf("owner = %v", m)
			}
		}},
		{"GET", "/api/v1/entities/resource/default/payments-db/dependents", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["count"] != 3.0 {
				t.Errorf("dependents = %v", m)
			}
		}},
		{"GET", "/api/v1/entities/resource/default/payments-db/dependents?transitive=false", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["count"] != 1.0 {
				t.Errorf("direct dependents = %v", m)
			}
		}},
		{"GET", "/api/v1/entities/resource/default/legacy-ftp/dependents", "", 200, func(t *testing.T, m map[string]any, raw string) {
			if m["count"] != 0.0 || !strings.Contains(raw, `"items": []`) {
				t.Errorf("no dependents = %s", raw)
			}
		}},
		{"GET", "/api/v1/entities/component/default/checkout-web/dependencies?depth=1", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["count"] != 3.0 {
				t.Errorf("dependencies = %v", m)
			}
		}},
		{"GET", "/api/v1/entities/component/default/checkout-web/dependencies?depth=-1", "", 400, nil},
		{"GET", "/api/v1/entities/component/default/checkout-web/dependencies?transitive=maybe", "", 400, nil},
		{"GET", "/api/v1/entities/component/default/ledger-service/impact", "", 200, func(t *testing.T, m map[string]any, _ string) {
			teams := m["teams"].(map[string]any)
			if len(teams) != 2 || len(m["unowned"].([]any)) != 1 {
				t.Errorf("impact = %v", m)
			}
		}},
		{"GET", "/api/v1/entities/component/default/payments-service/facts", "", 200, func(t *testing.T, m map[string]any, _ string) {
			f := m["facts"].(map[string]any)
			if f["ci.present"] != true || f["deps.outdatedRatio"] != 0.2 {
				t.Errorf("facts = %v", f)
			}
		}},
		{"GET", "/api/v1/entities/group/default/team-growth/owned?recursive=true", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["count"] != 6.0 {
				t.Errorf("owned = %v", m)
			}
		}},
		{"GET", "/api/v1/entities/group/default/team-growth/owned?recursive=x", "", 400, nil},
		{"GET", "/api/v1/entities/component/default/checkout-web/owned", "", 400, nil},
		{"GET", "/api/v1/diagnostics", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["count"] != 1.0 {
				t.Errorf("diagnostics = %v", m)
			}
		}},
		{"GET", "/api/v1/diagnostics?severity=warning", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["count"] != 0.0 {
				t.Errorf("warnings = %v", m)
			}
		}},
		{"GET", "/api/v1/diagnostics?severity=fatal", "", 400, nil},
		{"GET", "/api/v1/orphans", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["count"] != 6.0 {
				t.Errorf("orphans = %v", m)
			}
		}},
		{"GET", "/api/v1/scorecards", "", 200, func(t *testing.T, m map[string]any, _ string) {
			first := items(m)[0].(map[string]any)
			if m["count"] != 2.0 || first["name"] != "hygiene" || first["entities"] != 9.0 {
				t.Errorf("scorecards = %v", m)
			}
		}},
		{"GET", "/api/v1/scorecards/production-readiness", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["meanScore"] != 78.9 || len(m["entities"].([]any)) != 6 {
				t.Errorf("report = %v", m["meanScore"])
			}
		}},
		{"GET", "/api/v1/scorecards/production-readiness?format=markdown", "", 200, func(t *testing.T, _ map[string]any, raw string) {
			if !strings.HasPrefix(raw, "# Scorecard: Production readiness") {
				t.Errorf("markdown = %.80s", raw)
			}
		}},
		{"GET", "/api/v1/scorecards/production-readiness/rollup", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if len(m["teams"].([]any)) != 4 {
				t.Errorf("rollup = %v", m)
			}
		}},
		{"GET", "/api/v1/scorecards/nope", "", 404, nil},
		{"GET", "/api/v1/scorecards/nope/rollup", "", 404, nil},
		{"GET", "/api/v1/templates", "", 200, func(t *testing.T, m map[string]any, _ string) {
			first := items(m)[0].(map[string]any)
			if m["count"] != 2.0 || first["name"] != "go-service" || len(first["parameters"].([]any)) != 9 {
				t.Errorf("templates = %v", m)
			}
		}},
		{"GET", "/api/v1/templates/python-worker", "", 200, nil},
		{"GET", "/api/v1/templates/nope", "", 404, nil},
		{"POST", "/api/v1/templates/go-service/dry-run",
			`{"values": {"name": "orders-api", "description": "Accepts and tracks orders.", "owner": "team-growth"}}`, 200,
			func(t *testing.T, m map[string]any, _ string) {
				summary := m["summary"].(map[string]any)
				if summary["create"] != 7.0 || m["outputDir"] != "services/orders-api" || len(m["checks"].([]any)) != 0 {
					t.Errorf("dry-run = %v", m)
				}
				first := m["changes"].([]any)[0].(map[string]any)
				if !strings.HasPrefix(first["diff"].(string), "--- /dev/null") {
					t.Errorf("diff = %v", first["diff"])
				}
			}},
		{"POST", "/api/v1/templates/go-service/dry-run",
			`{"values": {"name": "orders-api", "description": "Accepts and tracks orders.", "owner": "team-ghost"}}`, 200,
			func(t *testing.T, m map[string]any, _ string) {
				checks := m["checks"].([]any)
				if len(checks) != 1 || checks[0].(map[string]any)["code"] != "dangling-ref" {
					t.Errorf("checks = %v", checks)
				}
			}},
		{"POST", "/api/v1/templates/go-service/dry-run",
			`{"values": {"name": "ledger-service", "description": "Regenerated double-entry ledger.", "owner": "team-payments"}}`, 200,
			func(t *testing.T, m map[string]any, _ string) {
				// Diffing an existing service replaces its descriptor: no self-duplicate.
				updates, _ := m["summary"].(map[string]any)["update"].(float64)
				if updates == 0 || len(m["checks"].([]any)) != 0 {
					t.Errorf("existing-service dry-run = %v", m)
				}
			}},
		{"POST", "/api/v1/templates/go-service/dry-run", `{"values": {"name": "X"}}`, 422, func(t *testing.T, m map[string]any, _ string) {
			e := m["error"].(map[string]any)
			if e["code"] != "invalid_parameters" || len(e["details"].([]any)) != 3 {
				t.Errorf("error = %v", e)
			}
		}},
		{"POST", "/api/v1/templates/go-service/dry-run", `{"vals": {}}`, 400, nil},
		{"POST", "/api/v1/templates/go-service/dry-run", `not json`, 400, nil},
		{"POST", "/api/v1/templates/nope/dry-run", `{}`, 404, nil},
		{"POST", "/api/v1/reload", "", 200, func(t *testing.T, m map[string]any, _ string) {
			if m["status"] != "reloaded" {
				t.Errorf("reload = %v", m)
			}
		}},
		{"GET", "/no/such/route", "", 404, func(t *testing.T, m map[string]any, _ string) {
			if m["error"].(map[string]any)["code"] != "not_found" {
				t.Errorf("not found = %v", m)
			}
		}},
		{"DELETE", "/api/v1/stats", "", 405, func(t *testing.T, m map[string]any, _ string) {
			if m["error"].(map[string]any)["code"] != "method_not_allowed" {
				t.Errorf("405 = %v", m)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			status, m, raw := do(t, ts, tt.method, tt.path, tt.body)
			if status != tt.status {
				t.Fatalf("status = %d, want %d\n%s", status, tt.status, raw)
			}
			if tt.check != nil {
				tt.check(t, m, raw)
			}
		})
	}
}

func TestReloadPicksUpChangesAndKeepsSnapshotOnFailure(t *testing.T) {
	root := t.TempDir()
	write := func(p, content string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("team/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: t}\nspec: {type: team}\n")
	s, ts := newTestServer(t, root)
	if _, m, _ := do(t, ts, "GET", "/api/v1/stats", ""); m["entities"] != 1.0 {
		t.Fatalf("stats = %v", m)
	}
	write("svc/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: c}\nspec: {type: service, lifecycle: production, owner: t}\n")
	if status, m, _ := do(t, ts, "POST", "/api/v1/reload", ""); status != 200 || m["stats"].(map[string]any)["entities"] != 2.0 {
		t.Fatalf("reload = %d %v", status, m)
	}
	// A broken scorecard makes reload fail; the previous snapshot keeps serving.
	write(".paved/scorecards/bad.yaml", "kind: nope\n")
	if status, _, _ := do(t, ts, "POST", "/api/v1/reload", ""); status != 500 {
		t.Fatalf("reload with bad scorecard = %d", status)
	}
	if _, m, _ := do(t, ts, "GET", "/api/v1/stats", ""); m["entities"] != 2.0 {
		t.Fatalf("stats after failed reload = %v", m)
	}
	if err := s.Reload(); err == nil {
		t.Fatal("Reload should report the error")
	}
}

func TestScorecardEvaluationErrorIsReported(t *testing.T) {
	root := t.TempDir()
	write := func(p, content string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("team/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: t}\nspec: {type: team}\n")
	write("svc/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: c}\nspec: {type: service, lifecycle: production, owner: t}\n")
	write("svc/go.mod", "require (\n\tbroken\n)\n") // facts collection fails for c
	write(".paved/scorecards/s.yaml", "apiVersion: paved.dev/v1\nkind: Scorecard\nmetadata: {name: s}\nspec: {rules: [{id: a, check: {fact: ci.present, equals: true}}]}\n")
	_, ts := newTestServer(t, root)
	status, m, _ := do(t, ts, "GET", "/api/v1/scorecards/s", "")
	if status != 500 || m["error"].(map[string]any)["code"] != "evaluation_failed" {
		t.Fatalf("report = %d %v", status, m)
	}
	_, m, _ = do(t, ts, "GET", "/api/v1/scorecards", "")
	if !strings.Contains(items(m)[0].(map[string]any)["error"].(string), "go.mod") {
		t.Fatalf("list = %v", m)
	}
	if status, _, _ := do(t, ts, "GET", "/api/v1/entities/component/default/c/facts", ""); status != 500 {
		t.Fatalf("facts status = %d", status)
	}
}

func TestNewFailsOnMissingRoot(t *testing.T) {
	if _, err := New("does-not-exist", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestRecoverer(t *testing.T) {
	s, err := New(fixtureRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := s.recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "internal server error") {
		t.Fatalf("recover = %d %s", rec.Code, rec.Body.String())
	}
}
