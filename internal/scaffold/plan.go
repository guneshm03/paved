package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/guneshm03/paved/internal/textdiff"
)

// Action is what applying a change would do to one file.
type Action string

// Actions.
const (
	ActionCreate    Action = "create"
	ActionUpdate    Action = "update"
	ActionUnchanged Action = "unchanged"
)

// Change is the planned effect on one target file.
type Change struct {
	// Path is slash-separated, relative to the target root.
	Path    string      `json:"path"`
	Action  Action      `json:"action"`
	Added   int         `json:"added"`
	Deleted int         `json:"deleted"`
	Diff    string      `json:"diff,omitempty"`
	Binary  bool        `json:"binary,omitempty"`
	Mode    fs.FileMode `json:"mode"`
	content []byte
}

// Plan is the dry-run outcome of rendering into a target root.
type Plan struct {
	Root    string   `json:"-"`
	Changes []Change `json:"changes"`
}

// Summary counts changes per action.
func (p *Plan) Summary() map[Action]int {
	m := map[Action]int{}
	for _, c := range p.Changes {
		m[c.Action]++
	}
	return m
}

// Conflicts lists paths that already exist with different content.
func (p *Plan) Conflicts() []string {
	var out []string
	for _, c := range p.Changes {
		if c.Action == ActionUpdate {
			out = append(out, c.Path)
		}
	}
	return out
}

// MakePlan compares rendered files with what is on disk under root, producing
// a unified diff per created or modified file. It never writes.
func MakePlan(r *Result, root string) (*Plan, error) {
	p := &Plan{Root: root}
	for _, f := range r.Files {
		target := r.TargetPath(f)
		abs := filepath.Join(root, filepath.FromSlash(target))
		old, err := os.ReadFile(abs)
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read %s: %w", target, err)
		}
		ch := Change{Path: target, Mode: f.Mode, content: f.Content}
		switch {
		case !exists:
			ch.Action = ActionCreate
		case bytes.Equal(old, f.Content):
			ch.Action = ActionUnchanged
		default:
			ch.Action = ActionUpdate
		}
		if ch.Action != ActionUnchanged {
			if isText(old) && isText(f.Content) {
				oldName := "a/" + target
				if !exists {
					oldName = "/dev/null"
				}
				ops := textdiff.Lines(textdiff.SplitLines(string(old)), textdiff.SplitLines(string(f.Content)))
				ch.Added, ch.Deleted = textdiff.Stats(ops)
				ch.Diff = textdiff.Unified(oldName, "b/"+target, string(old), string(f.Content), 3)
			} else {
				ch.Binary = true
			}
		}
		p.Changes = append(p.Changes, ch)
	}
	return p, nil
}

func isText(b []byte) bool { return !bytes.Contains(b[:min(len(b), 8192)], []byte{0}) }

// ConflictError is returned by Apply when files would be overwritten without force.
type ConflictError struct{ Paths []string }

func (e *ConflictError) Error() string {
	return "refusing to overwrite existing files (use --force): " + strings.Join(e.Paths, ", ")
}

// Apply writes the plan's created and updated files. Updates require force.
// Nothing is written when a conflict is detected.
func Apply(p *Plan, force bool) (written int, err error) {
	if c := p.Conflicts(); len(c) > 0 && !force {
		return 0, &ConflictError{Paths: c}
	}
	for _, ch := range p.Changes {
		if ch.Action == ActionUnchanged {
			continue
		}
		abs := filepath.Join(p.Root, filepath.FromSlash(ch.Path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return written, fmt.Errorf("create directory for %s: %w", ch.Path, err)
		}
		mode := ch.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(abs, ch.content, mode); err != nil {
			return written, fmt.Errorf("write %s: %w", ch.Path, err)
		}
		written++
	}
	return written, nil
}
