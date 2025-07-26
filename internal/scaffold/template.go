// Package scaffold implements the software-template scaffolder: templates
// declare JSON-Schema parameters and a skeleton directory; rendering produces
// an in-memory file set that can be diffed against a target tree (dry run) or
// written to it.
package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/guneshm03/paved/internal/schema"
	"go.yaml.in/yaml/v3"
)

// TemplateFileName is the descriptor file of a template directory.
const TemplateFileName = "template.yaml"

// SkeletonDir is the directory, next to template.yaml, holding the files to render.
const SkeletonDir = "skeleton"

// Template is a loaded software template.
type Template struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Tags        []string       `json:"tags,omitempty"`
	Output      string         `json:"output"`
	Parameters  *schema.Schema `json:"-"`
	// Dir is the template directory on disk.
	Dir string `json:"-"`
}

type templateDoc struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string   `yaml:"name"`
		Title       string   `yaml:"title"`
		Description string   `yaml:"description"`
		Tags        []string `yaml:"tags"`
	} `yaml:"metadata"`
	Spec struct {
		Output     string    `yaml:"output"`
		Parameters yaml.Node `yaml:"parameters"`
	} `yaml:"spec"`
}

// LoadTemplate reads dir/template.yaml and compiles its parameter schema.
func LoadTemplate(dir string) (*Template, error) {
	data, err := os.ReadFile(filepath.Join(dir, TemplateFileName))
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}
	var doc templateDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: parse: %w", dir, err)
	}
	if doc.APIVersion != "paved.dev/v1" || doc.Kind != "Template" {
		return nil, fmt.Errorf("%s: expected apiVersion paved.dev/v1 and kind Template, got %s %s", dir, doc.APIVersion, doc.Kind)
	}
	if doc.Metadata.Name == "" {
		return nil, fmt.Errorf("%s: metadata.name is required", dir)
	}
	if doc.Spec.Parameters.Kind == 0 {
		return nil, fmt.Errorf("%s: spec.parameters is required", dir)
	}
	params, err := schema.CompileYAML(&doc.Spec.Parameters)
	if err != nil {
		return nil, fmt.Errorf("%s: spec.parameters: %w", dir, err)
	}
	if len(params.Types) != 1 || params.Types[0] != "object" {
		return nil, fmt.Errorf("%s: spec.parameters must be a schema of type object", dir)
	}
	if st, err := os.Stat(filepath.Join(dir, SkeletonDir)); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s: missing %s/ directory", dir, SkeletonDir)
	}
	t := &Template{
		Name: doc.Metadata.Name, Title: doc.Metadata.Title, Description: doc.Metadata.Description,
		Tags: doc.Metadata.Tags, Output: doc.Spec.Output, Parameters: params, Dir: dir,
	}
	if t.Output == "" {
		t.Output = "."
	}
	// Fail fast on syntax errors in the output path template.
	if _, err := parseText("output", t.Output); err != nil {
		return nil, fmt.Errorf("%s: spec.output: %w", dir, err)
	}
	return t, nil
}

// LoadTemplates loads every immediate sub-directory of dir that contains a
// template.yaml, sorted by name. Duplicate names are an error.
func LoadTemplates(dir string) ([]*Template, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read templates dir: %w", err)
	}
	var out []*Template
	seen := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(sub, TemplateFileName)); err != nil {
			continue
		}
		t, err := LoadTemplate(sub)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[t.Name]; dup {
			return nil, fmt.Errorf("template %q is defined in both %s and %s", t.Name, prev, sub)
		}
		seen[t.Name] = sub
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
