// Package scorecard evaluates YAML-defined scorecards — weighted rules over
// repository facts — against catalog entities and rolls results up by team.
package scorecard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/guneshm03/paved/internal/catalog"
	"go.yaml.in/yaml/v3"
)

// Rule is one weighted check.
type Rule struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Remediation string    `json:"remediation,omitempty"`
	Weight      float64   `json:"weight"`
	Check       Condition `json:"-"`
}

// Level is a named maturity tier reached at a minimum score.
type Level struct {
	Name     string  `json:"name" yaml:"name"`
	MinScore float64 `json:"minScore" yaml:"minScore"`
}

// Selector restricts which entities a scorecard applies to. Empty lists match all.
type Selector struct {
	Kinds      []string `json:"kinds,omitempty" yaml:"kinds"`
	Types      []string `json:"types,omitempty" yaml:"types"`
	Lifecycles []string `json:"lifecycles,omitempty" yaml:"lifecycles"`
	Tags       []string `json:"tags,omitempty" yaml:"tags"`
}

// Matches reports whether e is in scope.
func (s Selector) Matches(e *catalog.Entity) bool {
	in := func(list []string, v string) bool {
		if len(list) == 0 {
			return true
		}
		for _, x := range list {
			if strings.EqualFold(x, v) {
				return true
			}
		}
		return false
	}
	if !in(s.Kinds, string(e.Kind)) || !in(s.Types, e.Spec.Type) || !in(s.Lifecycles, e.Spec.Lifecycle) {
		return false
	}
	if len(s.Tags) == 0 {
		return true
	}
	for _, t := range s.Tags {
		if e.HasTag(t) {
			return true
		}
	}
	return false
}

// Scorecard is a compiled scorecard definition.
type Scorecard struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	AppliesTo   Selector `json:"appliesTo"`
	// Levels are sorted by ascending MinScore.
	Levels []Level `json:"levels"`
	Rules  []Rule  `json:"rules"`
	File   string  `json:"file,omitempty"`
}

type scorecardDoc struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		Title       string `yaml:"title"`
		Description string `yaml:"description"`
	} `yaml:"metadata"`
	Spec struct {
		AppliesTo Selector `yaml:"appliesTo"`
		Levels    []Level  `yaml:"levels"`
		Rules     []struct {
			ID          string    `yaml:"id"`
			Title       string    `yaml:"title"`
			Description string    `yaml:"description"`
			Remediation string    `yaml:"remediation"`
			Weight      *float64  `yaml:"weight"`
			Check       yaml.Node `yaml:"check"`
		} `yaml:"rules"`
	} `yaml:"spec"`
}

// Parse compiles a scorecard document. file is used in error messages.
func Parse(file string, data []byte) (*Scorecard, error) {
	var doc scorecardDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%s: %s", file, fmt.Sprintf(format, args...))
	}
	if doc.APIVersion != "paved.dev/v1" || doc.Kind != "Scorecard" {
		return nil, fail("expected apiVersion paved.dev/v1 and kind Scorecard")
	}
	if doc.Metadata.Name == "" {
		return nil, fail("metadata.name is required")
	}
	if len(doc.Spec.Rules) == 0 {
		return nil, fail("spec.rules must not be empty")
	}
	sc := &Scorecard{
		Name: doc.Metadata.Name, Title: doc.Metadata.Title, Description: doc.Metadata.Description,
		AppliesTo: doc.Spec.AppliesTo, Levels: doc.Spec.Levels, File: file,
	}
	if sc.Title == "" {
		sc.Title = sc.Name
	}
	ids := map[string]bool{}
	for i, r := range doc.Spec.Rules {
		if r.ID == "" {
			return nil, fail("rule %d: id is required", i+1)
		}
		if ids[r.ID] {
			return nil, fail("rule %q is defined twice", r.ID)
		}
		ids[r.ID] = true
		w := 1.0
		if r.Weight != nil {
			w = *r.Weight
		}
		if w <= 0 {
			return nil, fail("rule %q: weight must be > 0", r.ID)
		}
		if r.Check.Kind == 0 {
			return nil, fail("rule %q: check is required", r.ID)
		}
		cond, err := ParseCondition(&r.Check)
		if err != nil {
			return nil, fail("rule %q: %v", r.ID, err)
		}
		title := r.Title
		if title == "" {
			title = r.ID
		}
		sc.Rules = append(sc.Rules, Rule{
			ID: r.ID, Title: title, Description: r.Description, Remediation: r.Remediation, Weight: w, Check: cond,
		})
	}
	sort.SliceStable(sc.Levels, func(i, j int) bool { return sc.Levels[i].MinScore < sc.Levels[j].MinScore })
	names := map[string]bool{}
	for _, l := range sc.Levels {
		if l.Name == "" || names[l.Name] {
			return nil, fail("levels need unique, non-empty names")
		}
		if l.MinScore < 0 || l.MinScore > 100 {
			return nil, fail("level %q: minScore must be within 0..100", l.Name)
		}
		names[l.Name] = true
	}
	return sc, nil
}

// LoadDir loads every *.yaml / *.yml scorecard in dir, sorted by name.
func LoadDir(dir string) ([]*Scorecard, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read scorecards dir: %w", err)
	}
	var out []*Scorecard
	seen := map[string]bool{}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read scorecard: %w", err)
		}
		sc, err := Parse(e.Name(), data)
		if err != nil {
			return nil, err
		}
		if seen[sc.Name] {
			return nil, fmt.Errorf("scorecard %q is defined twice", sc.Name)
		}
		seen[sc.Name] = true
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LevelFor returns the highest level whose MinScore <= score, or "" when none.
func (s *Scorecard) LevelFor(score float64) string {
	level := ""
	for _, l := range s.Levels {
		if score >= l.MinScore {
			level = l.Name
		}
	}
	return level
}

// TotalWeight sums rule weights.
func (s *Scorecard) TotalWeight() float64 {
	t := 0.0
	for _, r := range s.Rules {
		t += r.Weight
	}
	return t
}
