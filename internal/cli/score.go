package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/guneshm03/paved/internal/api"
	"github.com/guneshm03/paved/internal/scorecard"
)

func scoreCommands() []command {
	return []command{
		{name: "list", summary: "list scorecards", run: runScorecards},
		{name: "run", summary: "evaluate a scorecard (text, md or json)", run: runScore},
		{name: "rollup", summary: "aggregate a scorecard by team hierarchy", run: runRollup},
		{name: "facts", summary: "show the facts collected for an entity", run: runFacts},
		{name: "deps", summary: "dependency freshness report for an entity", run: runDeps},
	}
}

func runScorecards(a *app, args []string) error {
	fs := a.newFlags("score list")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SCORECARD\tTITLE\tRULES\tLEVELS")
	for _, s := range ws.Scorecards {
		var lv []string
		for _, l := range s.Levels {
			lv = append(lv, fmt.Sprintf("%s>=%g", l.Name, l.MinScore))
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%v\n", s.Name, s.Title, len(s.Rules), lv)
	}
	return tw.Flush()
}

// evaluate loads the workspace and evaluates the named scorecard.
func (a *app) evaluate(name string) (*scorecard.Report, []scorecard.TeamRollup, error) {
	ws, err := a.open()
	if err != nil {
		return nil, nil, err
	}
	sc, err := ws.Scorecard(name)
	if err != nil {
		return nil, nil, err
	}
	rep, err := scorecard.NewEvaluator(ws.Collector()).Evaluate(sc)
	if err != nil {
		return nil, nil, err
	}
	return rep, scorecard.Rollup(rep, ws.Catalog), nil
}

func runScore(a *app, args []string) error {
	fs := a.newFlags("score run")
	format := fs.String("format", "text", "text|md|json")
	details := fs.Bool("details", false, "list failing rules with reasons (text format)")
	minScore := fs.Float64("min-score", -1, "exit 1 if any entity scores below this")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "score run <scorecard>"); err != nil {
		return err
	}
	if err := checkFormat(*format, "text", "md", "json"); err != nil {
		return err
	}
	rep, rows, err := a.evaluate(pos[0])
	if err != nil {
		return err
	}
	switch *format {
	case "json":
		err = a.printJSON(map[string]any{"report": rep, "rollup": rows})
	case "md":
		err = scorecard.WriteMarkdown(a.stdout, rep, rows)
	default:
		err = scorecard.WriteText(a.stdout, rep, *details)
	}
	if err != nil {
		return err
	}
	if *minScore >= 0 {
		below := 0
		for _, e := range rep.Entities {
			if e.Score < *minScore {
				below++
			}
		}
		if below > 0 {
			return &findingsError{msg: fmt.Sprintf("%d entities score below %g", below, *minScore)}
		}
	}
	return nil
}

func runRollup(a *app, args []string) error {
	fs := a.newFlags("score rollup")
	format := fs.String("format", "text", "text|json")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "score rollup <scorecard>"); err != nil {
		return err
	}
	if err := checkFormat(*format, "text", "json"); err != nil {
		return err
	}
	rep, rows, err := a.evaluate(pos[0])
	if err != nil {
		return err
	}
	if *format == "json" {
		return a.printJSON(rows)
	}
	return scorecard.WriteRollupText(a.stdout, rep, rows)
}

func runFacts(a *app, args []string) error {
	fs := a.newFlags("score facts")
	format := fs.String("format", "text", "text|json")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "score facts <ref>"); err != nil {
		return err
	}
	if err := checkFormat(*format, "text", "json"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	e, err := ws.Catalog.Lookup(pos[0])
	if err != nil {
		return err
	}
	f, err := ws.Collector().Collect(e)
	if err != nil {
		return err
	}
	if *format == "json" {
		return a.printJSON(f)
	}
	fmt.Fprintf(a.stdout, "facts for %s (%s)\n", e.Ref(), e.Source.Dir())
	for _, k := range f.Keys() {
		fmt.Fprintf(a.stdout, "  %-22s %v\n", k, f[k])
	}
	return nil
}

func runDeps(a *app, args []string) error {
	fs := a.newFlags("score deps")
	format := fs.String("format", "text", "text|json")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := needArgs(pos, 1, "score deps <ref>"); err != nil {
		return err
	}
	if err := checkFormat(*format, "text", "json"); err != nil {
		return err
	}
	ws, err := a.open()
	if err != nil {
		return err
	}
	e, err := ws.Catalog.Lookup(pos[0])
	if err != nil {
		return err
	}
	sum, err := ws.Collector().Dependencies(e)
	if err != nil {
		return err
	}
	if *format == "json" {
		return a.printJSON(sum)
	}
	fmt.Fprintf(a.stdout, "dependencies of %s (registry snapshot %s)\n", e.Ref(), ws.Registry.Snapshot.Format(time.DateOnly))
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ECOSYSTEM\tPACKAGE\tUSING\tLATEST\tSTATUS\tMAJORS BEHIND\tLIBYEARS")
	for _, it := range sum.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%.2f\n", it.Ecosystem, it.Name, dash(it.Version), dash(it.Latest), it.Status, it.MajorsBehind, it.Libyears)
	}
	fmt.Fprintf(tw, "\ntotal %d, outdated %d (ratio %.3f), unknown %d, unpinned %d, libyears %.2f\n",
		sum.Total, sum.Outdated, sum.OutdatedRatio(), sum.Unknown, sum.Unpinned, sum.Libyears)
	return tw.Flush()
}

func runServe(a *app, args []string) error {
	fs := a.newFlags("serve")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if a.logLevel == "" {
		a.logLevel = "info" // a server logs requests unless told otherwise
	}
	srv, err := api.New(a.root, a.log())
	if err != nil {
		return err
	}
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	a.log().Info("listening", "addr", *addr, "root", a.root)
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(shutdownCtx)
	}
}
