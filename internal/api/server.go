// Package api serves the catalog, graph queries, scorecards and the
// scaffolder over a read-mostly JSON REST API.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/guneshm03/paved/internal/catalog"
	"github.com/guneshm03/paved/internal/scorecard"
	"github.com/guneshm03/paved/internal/workspace"
)

// snapshot is an immutable, fully evaluated view of the workspace. Requests
// read one snapshot; Reload swaps in a new one atomically.
type snapshot struct {
	ws       *workspace.Workspace
	reports  map[string]*scorecard.Report
	evalErrs map[string]string
	loadedAt time.Time
}

// Server is the HTTP API.
type Server struct {
	root   string
	logger *slog.Logger
	now    func() time.Time

	mu   sync.RWMutex
	snap *snapshot
}

// New loads the workspace at root and returns a ready server.
func New(root string, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	s := &Server{root: root, logger: logger, now: time.Now}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload re-reads the workspace from disk and re-evaluates every scorecard.
// On failure the previous snapshot stays in service.
func (s *Server) Reload() error {
	ws, err := workspace.Open(s.root, s.logger)
	if err != nil {
		return err
	}
	snap := &snapshot{ws: ws, reports: map[string]*scorecard.Report{}, evalErrs: map[string]string{}, loadedAt: s.now()}
	ev := scorecard.NewEvaluator(ws.Collector())
	for _, sc := range ws.Scorecards {
		rep, err := ev.Evaluate(sc)
		if err != nil {
			snap.evalErrs[sc.Name] = err.Error()
			s.logger.Error("scorecard evaluation failed", "scorecard", sc.Name, "err", err)
			continue
		}
		snap.reports[sc.Name] = rep
	}
	s.mu.Lock()
	s.snap = snap
	s.mu.Unlock()
	return nil
}

func (s *Server) current() *snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// Handler returns the routed handler with logging and panic recovery.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	const ent = "/api/v1/entities/{kind}/{namespace}/{name}"
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	mux.HandleFunc("GET /api/v1/entities", s.handleEntities)
	mux.HandleFunc("GET "+ent, s.withEntity(s.handleEntity))
	mux.HandleFunc("GET "+ent+"/owner", s.withEntity(s.handleOwner))
	mux.HandleFunc("GET "+ent+"/dependents", s.withEntity(s.handleWalk(catalog.Upstream)))
	mux.HandleFunc("GET "+ent+"/dependencies", s.withEntity(s.handleWalk(catalog.Downstream)))
	mux.HandleFunc("GET "+ent+"/impact", s.withEntity(s.handleImpact))
	mux.HandleFunc("GET "+ent+"/facts", s.withEntity(s.handleFacts))
	mux.HandleFunc("GET "+ent+"/owned", s.withEntity(s.handleOwned))
	mux.HandleFunc("GET /api/v1/diagnostics", s.handleDiagnostics)
	mux.HandleFunc("GET /api/v1/orphans", s.handleOrphans)
	mux.HandleFunc("GET /api/v1/scorecards", s.handleScorecards)
	mux.HandleFunc("GET /api/v1/scorecards/{name}", s.handleScorecardReport)
	mux.HandleFunc("GET /api/v1/scorecards/{name}/rollup", s.handleRollup)
	mux.HandleFunc("GET /api/v1/templates", s.handleTemplates)
	mux.HandleFunc("GET /api/v1/templates/{name}", s.handleTemplate)
	mux.HandleFunc("POST /api/v1/templates/{name}/dry-run", s.handleDryRun)
	mux.HandleFunc("POST /api/v1/reload", s.handleReload)
	return s.recoverer(s.logRequests(jsonRouteErrors(mux)))
}

// jsonRouteErrors turns the mux's plain-text 404 and 405 responses into the
// JSON error envelope, keeping the Allow header for 405.
func jsonRouteErrors(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		probe := httptest.NewRecorder()
		mux.ServeHTTP(probe, r)
		if allow := probe.Header().Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		code := "not_found"
		if probe.Code == http.StatusMethodNotAllowed {
			code = "method_not_allowed"
		}
		writeError(w, probe.Code, code, "no route for "+r.Method+" "+r.URL.Path, nil)
	})
}

// apiError is the JSON error envelope.
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v) // the status line is already sent; nothing useful to do on failure
}

func writeError(w http.ResponseWriter, status int, code, msg string, details any) {
	var e apiError
	e.Error.Code, e.Error.Message, e.Error.Details = code, msg, details
	writeJSON(w, status, e)
}

// writeLookupError maps domain errors to HTTP statuses.
func writeLookupError(w http.ResponseWriter, err error) {
	var nf *catalog.NotFoundError
	var wnf *workspace.NotFoundError
	var re *catalog.RefError
	switch {
	case errors.As(err, &nf), errors.As(err, &wnf):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.As(err, &re):
		writeError(w, http.StatusBadRequest, "invalid_ref", err.Error(), nil)
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration_ms", float64(time.Since(start).Microseconds())/1000)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.logger.Error("panic serving request", "path", r.URL.Path, "panic", fmt.Sprint(v))
				writeError(w, http.StatusInternalServerError, "internal", "internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// queryBool parses an optional boolean query parameter.
func queryBool(r *http.Request, name string, def bool) (bool, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("query parameter %s: %q is not a boolean", name, v)
	}
	return b, nil
}

// queryInt parses an optional non-negative integer query parameter.
func queryInt(r *http.Request, name string, def int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("query parameter %s: %q is not a non-negative integer", name, v)
	}
	return n, nil
}
