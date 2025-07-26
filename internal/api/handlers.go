package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/guneshm03/paved/internal/catalog"
	"github.com/guneshm03/paved/internal/scaffold"
	"github.com/guneshm03/paved/internal/schema"
	"github.com/guneshm03/paved/internal/scorecard"
)

// maxBodyBytes bounds request bodies (template parameters are small).
const maxBodyBytes = 1 << 20

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	snap := s.current()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "loadedAt": snap.loadedAt.UTC().Format(time.RFC3339), "entities": len(snap.ws.Catalog.Entities()),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.current().ws.Catalog.Stats())
}

func (s *Server) handleEntities(w http.ResponseWriter, r *http.Request) {
	cat := s.current().ws.Catalog
	q := r.URL.Query()
	f := catalog.Filter{Type: q.Get("type"), Lifecycle: q.Get("lifecycle"), Tag: q.Get("tag")}
	if k := q.Get("kind"); k != "" {
		kind, ok := catalog.ParseKind(k)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_kind", "unsupported kind "+k, nil)
			return
		}
		f.Kind = kind
	}
	for param, dst := range map[string]*catalog.Ref{"owner": &f.Owner, "system": &f.System} {
		v := q.Get(param)
		if v == "" {
			continue
		}
		def := catalog.KindGroup
		if param == "system" {
			def = catalog.KindSystem
		}
		ref, err := catalog.ParseRef(v, def, catalog.DefaultNamespace)
		if err != nil {
			writeLookupError(w, err)
			return
		}
		*dst = ref
	}
	items := cat.Find(f)
	if items == nil {
		items = []*catalog.Entity{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

type entityHandler func(w http.ResponseWriter, r *http.Request, snap *snapshot, e *catalog.Entity)

// withEntity resolves {kind}/{namespace}/{name} to an entity of the current snapshot.
func (s *Server) withEntity(h entityHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap := s.current()
		kind, ok := catalog.ParseKind(r.PathValue("kind"))
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_kind", "unsupported kind "+r.PathValue("kind"), nil)
			return
		}
		ref := catalog.NewRef(kind, r.PathValue("namespace"), r.PathValue("name"))
		e, found := snap.ws.Catalog.Get(ref)
		if !found {
			writeLookupError(w, &catalog.NotFoundError{Query: ref.String()})
			return
		}
		h(w, r, snap, e)
	}
}

func (s *Server) handleEntity(w http.ResponseWriter, _ *http.Request, snap *snapshot, e *catalog.Entity) {
	rels := snap.ws.Catalog.Graph().Relations(e.Ref())
	if rels == nil {
		rels = []catalog.Relation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ref": e.Ref(), "entity": e, "relations": rels})
}

func (s *Server) handleOwner(w http.ResponseWriter, _ *http.Request, snap *snapshot, e *catalog.Entity) {
	o, err := snap.ws.Catalog.OwnerOf(e.Ref())
	if err != nil {
		writeLookupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) handleWalk(dir catalog.Direction) entityHandler {
	return func(w http.ResponseWriter, r *http.Request, snap *snapshot, e *catalog.Entity) {
		transitive, err := queryBool(r, "transitive", true)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_query", err.Error(), nil)
			return
		}
		depth, err := queryInt(r, "depth", 0)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_query", err.Error(), nil)
			return
		}
		hits, err := snap.ws.Catalog.Walk(e.Ref(), dir, catalog.WalkOptions{Transitive: transitive, MaxDepth: depth})
		if err != nil {
			writeLookupError(w, err)
			return
		}
		if hits == nil {
			hits = []catalog.Hit{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"root": e.Ref(), "count": len(hits), "items": hits})
	}
}

func (s *Server) handleImpact(w http.ResponseWriter, _ *http.Request, snap *snapshot, e *catalog.Entity) {
	im, err := snap.ws.Catalog.ImpactOf(e.Ref())
	if err != nil {
		writeLookupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, im)
}

func (s *Server) handleFacts(w http.ResponseWriter, _ *http.Request, snap *snapshot, e *catalog.Entity) {
	f, err := snap.ws.Collector().Collect(e)
	if err != nil {
		writeLookupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ref": e.Ref(), "facts": f})
}

func (s *Server) handleOwned(w http.ResponseWriter, r *http.Request, snap *snapshot, e *catalog.Entity) {
	if e.Kind != catalog.KindGroup {
		writeError(w, http.StatusBadRequest, "not_a_group", e.Ref().String()+" is not a Group", nil)
		return
	}
	recursive, err := queryBool(r, "recursive", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error(), nil)
		return
	}
	items := snap.ws.Catalog.OwnedBy(e.Ref(), recursive)
	refs := make([]catalog.Ref, len(items))
	for i, it := range items {
		refs[i] = it.Ref()
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": e.Ref(), "recursive": recursive, "count": len(refs), "items": refs})
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	sev := r.URL.Query().Get("severity")
	if sev != "" && sev != string(catalog.SeverityError) && sev != string(catalog.SeverityWarning) {
		writeError(w, http.StatusBadRequest, "invalid_query", "severity must be error or warning", nil)
		return
	}
	out := []catalog.Diagnostic{}
	for _, d := range s.current().ws.Catalog.Diagnostics() {
		if sev == "" || string(d.Severity) == sev {
			out = append(out, d)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(out), "items": out})
}

func (s *Server) handleOrphans(w http.ResponseWriter, _ *http.Request) {
	o := s.current().ws.Catalog.FindOrphans()
	if o == nil {
		o = []catalog.Orphan{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(o), "items": o})
}

func (s *Server) handleScorecards(w http.ResponseWriter, _ *http.Request) {
	snap := s.current()
	type item struct {
		*scorecard.Scorecard
		MeanScore *float64 `json:"meanScore,omitempty"`
		Entities  int      `json:"entities"`
		Error     string   `json:"error,omitempty"`
	}
	items := []item{}
	for _, sc := range snap.ws.Scorecards {
		it := item{Scorecard: sc, Error: snap.evalErrs[sc.Name]}
		if rep, ok := snap.reports[sc.Name]; ok {
			m := rep.MeanScore
			it.MeanScore, it.Entities = &m, len(rep.Entities)
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

// report returns the evaluated report for {name}, writing an error if absent.
func (s *Server) report(w http.ResponseWriter, r *http.Request, snap *snapshot) (*scorecard.Report, bool) {
	name := r.PathValue("name")
	if _, err := snap.ws.Scorecard(name); err != nil {
		writeLookupError(w, err)
		return nil, false
	}
	rep, ok := snap.reports[name]
	if !ok {
		writeError(w, http.StatusInternalServerError, "evaluation_failed", snap.evalErrs[name], nil)
		return nil, false
	}
	return rep, true
}

func (s *Server) handleScorecardReport(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	rep, ok := s.report(w, r, snap)
	if !ok {
		return
	}
	if r.URL.Query().Get("format") == "markdown" {
		var b bytes.Buffer
		if err := scorecard.WriteMarkdown(&b, rep, scorecard.Rollup(rep, snap.ws.Catalog)); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write(b.Bytes())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleRollup(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	rep, ok := s.report(w, r, snap)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scorecard": rep.Scorecard, "teams": scorecard.Rollup(rep, snap.ws.Catalog)})
}

type templateView struct {
	*scaffold.Template
	Params []schema.ParamDoc `json:"parameters"`
}

func (s *Server) handleTemplates(w http.ResponseWriter, _ *http.Request) {
	items := []templateView{}
	for _, t := range s.current().ws.Templates {
		items = append(items, templateView{Template: t, Params: t.Parameters.Params()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

func (s *Server) handleTemplate(w http.ResponseWriter, r *http.Request) {
	t, err := s.current().ws.Template(r.PathValue("name"))
	if err != nil {
		writeLookupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, templateView{Template: t, Params: t.Parameters.Params()})
}

// dryRunRequest is the body of POST /templates/{name}/dry-run.
type dryRunRequest struct {
	Values map[string]any `json:"values"`
}

// handleDryRun renders a template against the catalog root and returns the
// planned changes with diffs. The API never writes files: applying is a
// deliberate, local CLI action.
func (s *Server) handleDryRun(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	t, err := snap.ws.Template(r.PathValue("name"))
	if err != nil {
		writeLookupError(w, err)
		return
	}
	var req dryRunRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "body must be JSON {\"values\": {...}}: "+err.Error(), nil)
		return
	}
	res, err := scaffold.Render(t, req.Values)
	if err != nil {
		var pe *scaffold.ParamError
		if errors.As(err, &pe) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_parameters", pe.Error(), pe.Errors)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "render_failed", err.Error(), nil)
		return
	}
	plan, err := scaffold.MakePlan(res, snap.ws.Root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	checks := res.CheckDescriptors(snap.ws.Catalog)
	if checks == nil {
		checks = []catalog.Diagnostic{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"template": t.Name, "values": res.Values, "outputDir": res.OutputDir,
		"summary": plan.Summary(), "changes": plan.Changes, "checks": checks,
	})
}

func (s *Server) handleReload(w http.ResponseWriter, _ *http.Request) {
	if err := s.Reload(); err != nil {
		writeError(w, http.StatusInternalServerError, "reload_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "reloaded", "stats": s.current().ws.Catalog.Stats()})
}
