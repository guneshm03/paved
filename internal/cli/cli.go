// Package cli implements the paved command-line interface.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/guneshm03/paved/internal/scaffold"
	"github.com/guneshm03/paved/internal/workspace"
)

// Version is the CLI version, overridable at link time.
var Version = "0.1.0"

// Exit codes.
const (
	ExitOK       = 0
	ExitFindings = 1 // the command ran and found problems (validation errors, ...)
	ExitUsage    = 2
	ExitError    = 3
)

// usageError marks errors caused by bad invocation.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return &usageError{msg: fmt.Sprintf(format, args...)} }

// findingsError means the command succeeded but reports a failing result.
type findingsError struct{ msg string }

func (e *findingsError) Error() string { return e.msg }

// app carries per-invocation state.
type app struct {
	stdout, stderr io.Writer
	root           string
	logLevel       string
	logger         *slog.Logger
}

type command struct {
	name    string
	summary string
	run     func(a *app, args []string) error
	subs    []command
}

func commands() []command {
	return []command{
		{name: "catalog", summary: "inspect and validate the software catalog", subs: catalogCommands()},
		{name: "graph", summary: "query ownership and dependencies", subs: graphCommands()},
		{name: "scaffold", summary: "list and run software templates", subs: scaffoldCommands()},
		{name: "score", summary: "evaluate scorecards and team rollups", subs: scoreCommands()},
		{name: "serve", summary: "run the REST API", run: runServe},
		{name: "version", summary: "print the version", run: func(a *app, _ []string) error {
			fmt.Fprintln(a.stdout, "paved", Version)
			return nil
		}},
	}
}

// Run executes the CLI and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	a := &app{stdout: stdout, stderr: stderr}
	global := flag.NewFlagSet("paved", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	a.registerGlobal(global)
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			a.printUsage(nil)
			return ExitOK
		}
		fmt.Fprintln(stderr, "paved:", err)
		return ExitUsage
	}
	rest := global.Args()
	if len(rest) == 0 || rest[0] == "help" {
		a.printUsage(nil)
		if len(rest) == 0 {
			return ExitUsage
		}
		return ExitOK
	}
	err := a.dispatch(commands(), rest, nil)
	return a.exitCode(err)
}

func (a *app) exitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ue *usageError
	var fe *findingsError
	var pe *scaffold.ParamError
	switch {
	case errors.As(err, &pe):
		fmt.Fprintln(a.stderr, "paved:", err)
		return ExitUsage
	case errors.As(err, &fe):
		fmt.Fprintln(a.stderr, "paved:", err)
		return ExitFindings
	case errors.As(err, &ue):
		fmt.Fprintln(a.stderr, "paved:", err)
		return ExitUsage
	case errors.Is(err, flag.ErrHelp):
		return ExitOK
	}
	fmt.Fprintln(a.stderr, "paved: error:", err)
	return ExitError
}

func (a *app) dispatch(cmds []command, args []string, path []string) error {
	name := args[0]
	for _, c := range cmds {
		if c.name != name {
			continue
		}
		path = append(path, name)
		if c.run != nil {
			return c.run(a, args[1:])
		}
		if len(args) < 2 || args[1] == "help" || args[1] == "-h" || args[1] == "--help" {
			a.printUsage(&c)
			if len(args) < 2 {
				return usagef("%s needs a subcommand", strings.Join(path, " "))
			}
			return nil
		}
		return a.dispatch(c.subs, args[1:], path)
	}
	return usagef("unknown command %q (run 'paved help')", strings.Join(append(path, name), " "))
}

func (a *app) printUsage(c *command) {
	w := a.stderr
	if c == nil {
		fmt.Fprintf(w, "paved %s — internal developer platform core\n\nUsage: paved [--root DIR] <command> [args]\n\nCommands:\n", Version)
		for _, cmd := range commands() {
			fmt.Fprintf(w, "  %-10s %s\n", cmd.name, cmd.summary)
		}
		fmt.Fprintln(w, "\nGlobal flags:\n  --root DIR        catalog root (default $PAVED_ROOT or .)\n  --log-level LVL   debug|info|warn|error (default warn)")
		return
	}
	fmt.Fprintf(w, "Usage: paved %s <subcommand> [flags]\n\nSubcommands:\n", c.name)
	for _, s := range c.subs {
		fmt.Fprintf(w, "  %-14s %s\n", s.name, s.summary)
	}
}

func (a *app) registerGlobal(fs *flag.FlagSet) {
	def := os.Getenv("PAVED_ROOT")
	if def == "" {
		def = "."
	}
	if a.root == "" {
		a.root = def
	}
	fs.StringVar(&a.root, "root", a.root, "catalog root directory")
	fs.StringVar(&a.logLevel, "log-level", a.logLevel, "log level: debug|info|warn|error (default warn; info for serve)")
}

// newFlags returns a sub-command flag set that also accepts the global flags.
func (a *app) newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	a.registerGlobal(fs)
	return fs
}

// parse parses flags allowing them before or after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func (a *app) log() *slog.Logger {
	if a.logger != nil {
		return a.logger
	}
	lvl := slog.LevelWarn
	if a.logLevel != "" {
		if err := lvl.UnmarshalText([]byte(a.logLevel)); err != nil {
			lvl = slog.LevelWarn
		}
	}
	a.logger = slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: lvl}))
	return a.logger
}

func (a *app) open() (*workspace.Workspace, error) {
	return workspace.Open(a.root, a.log())
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// checkFormat validates a --format value.
func checkFormat(format string, allowed ...string) error {
	for _, f := range allowed {
		if f == format {
			return nil
		}
	}
	sort.Strings(allowed)
	return usagef("--format must be one of %s", strings.Join(allowed, ", "))
}

func needArgs(args []string, n int, usage string) error {
	if len(args) != n {
		return usagef("usage: paved %s", usage)
	}
	return nil
}
