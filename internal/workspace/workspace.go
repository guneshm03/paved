// Package workspace ties a catalog root to its scorecards, templates and
// registry snapshot, as configured by an optional paved.yaml at the root.
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/guneshm03/paved/internal/catalog"
	"github.com/guneshm03/paved/internal/facts"
	"github.com/guneshm03/paved/internal/scaffold"
	"github.com/guneshm03/paved/internal/scorecard"
	"go.yaml.in/yaml/v3"
)

// ConfigFileName is the optional workspace configuration file at the root.
const ConfigFileName = "paved.yaml"

// Config locates workspace assets. Relative paths resolve against the root.
type Config struct {
	Scorecards      string   `yaml:"scorecards" json:"scorecards"`
	Templates       string   `yaml:"templates" json:"templates"`
	Registry        string   `yaml:"registry" json:"registry"`
	DescriptorNames []string `yaml:"descriptorNames" json:"descriptorNames,omitempty"`
}

// DefaultConfig is used for any field paved.yaml leaves empty.
var DefaultConfig = Config{
	Scorecards: ".paved/scorecards",
	Templates:  ".paved/templates",
	Registry:   ".paved/registry.yaml",
}

// Workspace is a fully loaded catalog root.
type Workspace struct {
	Root       string
	Config     Config
	Catalog    *catalog.Catalog
	Scorecards []*scorecard.Scorecard
	Templates  []*scaffold.Template
	// Registry is nil when the configured snapshot file does not exist.
	Registry *facts.Registry
}

// LoadConfig reads root/paved.yaml (if present) over the defaults.
func LoadConfig(root string) (Config, error) {
	cfg := DefaultConfig
	data, err := os.ReadFile(filepath.Join(root, ConfigFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", ConfigFileName, err)
	}
	var file Config
	if err := yaml.Unmarshal(data, &file); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", ConfigFileName, err)
	}
	if file.Scorecards != "" {
		cfg.Scorecards = file.Scorecards
	}
	if file.Templates != "" {
		cfg.Templates = file.Templates
	}
	if file.Registry != "" {
		cfg.Registry = file.Registry
	}
	cfg.DescriptorNames = file.DescriptorNames
	return cfg, nil
}

// Open loads the catalog, scorecards, templates and registry under root.
func Open(root string, logger *slog.Logger) (*Workspace, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	cat, err := catalog.Load(root, catalog.LoadOptions{DescriptorNames: cfg.DescriptorNames, Logger: logger})
	if err != nil {
		return nil, err
	}
	w := &Workspace{Root: root, Config: cfg, Catalog: cat}
	if w.Scorecards, err = scorecard.LoadDir(w.path(cfg.Scorecards)); err != nil {
		return nil, err
	}
	if w.Templates, err = scaffold.LoadTemplates(w.path(cfg.Templates)); err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(w.path(cfg.Registry)); statErr == nil {
		if w.Registry, err = facts.LoadRegistry(w.path(cfg.Registry)); err != nil {
			return nil, err
		}
	}
	logger.Info("workspace opened", "scorecards", len(w.Scorecards), "templates", len(w.Templates), "registry", w.Registry != nil)
	return w, nil
}

func (w *Workspace) path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(w.Root, filepath.FromSlash(p))
}

// Collector returns a fact collector for this workspace.
func (w *Workspace) Collector() *facts.Collector {
	return &facts.Collector{Root: w.Root, Catalog: w.Catalog, Registry: w.Registry}
}

// NotFoundError reports an unknown scorecard or template name.
type NotFoundError struct {
	What string
	Name string
}

func (e *NotFoundError) Error() string { return fmt.Sprintf("%s %q not found", e.What, e.Name) }

// Scorecard finds a scorecard by name.
func (w *Workspace) Scorecard(name string) (*scorecard.Scorecard, error) {
	for _, s := range w.Scorecards {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, &NotFoundError{What: "scorecard", Name: name}
}

// Template finds a template by name.
func (w *Workspace) Template(name string) (*scaffold.Template, error) {
	for _, t := range w.Templates {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, &NotFoundError{What: "template", Name: name}
}
