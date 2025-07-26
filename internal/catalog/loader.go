package catalog

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultDescriptorName is the file name discovered under the catalog root.
const DefaultDescriptorName = "catalog-info.yaml"

// skipDirs are never descended into during discovery, in addition to every
// hidden directory (.git, .paved, .github, ...).
var skipDirs = map[string]bool{"node_modules": true, "vendor": true}

// LoadOptions configures discovery.
type LoadOptions struct {
	// DescriptorNames are the file names treated as descriptors. Defaults to
	// catalog-info.yaml and catalog-info.yml.
	DescriptorNames []string
	// Logger receives debug output; nil discards it.
	Logger *slog.Logger
}

// Discover walks root and returns slash-separated paths (relative to root) of
// every descriptor file, sorted.
func Discover(root string, opts LoadOptions) ([]string, error) {
	names := opts.DescriptorNames
	if len(names) == 0 {
		names = []string{DefaultDescriptorName, "catalog-info.yml"}
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if want[d.Name()] {
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil {
				return relErr
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover descriptors under %s: %w", root, err)
	}
	sort.Strings(files)
	return files, nil
}

// Load discovers, parses and validates every descriptor under root. Only I/O
// failures return an error; descriptor problems become diagnostics.
func Load(root string, opts LoadOptions) (*Catalog, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("catalog root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("catalog root %s is not a directory", root)
	}
	files, err := Discover(root, opts)
	if err != nil {
		return nil, err
	}
	var (
		entities []*Entity
		diags    []Diagnostic
	)
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		es, ds := ParseDescriptors(f, data)
		if opts.Logger != nil {
			opts.Logger.Debug("parsed descriptor", "file", f, "entities", len(es), "diagnostics", len(ds))
		}
		entities = append(entities, es...)
		diags = append(diags, ds...)
	}
	c := Build(entities, diags)
	if opts.Logger != nil {
		st := c.Stats()
		opts.Logger.Info("catalog loaded", "root", root, "files", len(files), "entities", st.Entities,
			"errors", st.Errors, "warnings", st.Warnings)
	}
	return c, nil
}
