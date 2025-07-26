package scaffold

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/guneshm03/paved/internal/catalog"
	"github.com/guneshm03/paved/internal/schema"
)

// TemplateSuffix marks skeleton files whose content is rendered; the suffix is
// stripped from the output name. All other files are copied byte-for-byte.
const TemplateSuffix = ".tmpl"

// File is one rendered output file.
type File struct {
	// Path is slash-separated and relative to the result's OutputDir.
	Path    string      `json:"path"`
	Content []byte      `json:"-"`
	Mode    fs.FileMode `json:"mode"`
}

// Result is the in-memory output of rendering a template.
type Result struct {
	Template string         `json:"template"`
	Values   map[string]any `json:"values"`
	// OutputDir is the rendered spec.output, relative to the target root.
	OutputDir string `json:"outputDir"`
	Files     []File `json:"files"`
}

// ParamError reports parameter values that do not satisfy the schema.
type ParamError struct {
	Template string
	Errors   []schema.ValidationError
}

func (e *ParamError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, ve := range e.Errors {
		parts[i] = ve.Error()
	}
	return fmt.Sprintf("template %s: invalid parameters: %s", e.Template, strings.Join(parts, "; "))
}

// PathError reports a rendered path that would escape the output directory.
type PathError struct {
	Source string
	Path   string
}

func (e *PathError) Error() string {
	return fmt.Sprintf("skeleton file %s renders to unsafe path %q", e.Source, e.Path)
}

// Render validates values against the template parameters (after applying
// defaults) and renders the skeleton in memory. File order is sorted by path.
func Render(t *Template, values map[string]any) (*Result, error) {
	vals := t.Parameters.ApplyDefaults(values)
	if errs := t.Parameters.Validate(vals); len(errs) > 0 {
		return nil, &ParamError{Template: t.Name, Errors: errs}
	}
	// Optional parameters without a default render as nil (falsy in {{ if }}),
	// while missingkey=error still catches references to undeclared names.
	scope := make(map[string]any, len(vals)+len(t.Parameters.Order))
	for _, name := range t.Parameters.Order {
		scope[name] = nil
	}
	for k, v := range vals {
		scope[k] = v
	}
	out, err := renderString("output", t.Output, scope)
	if err != nil {
		return nil, err
	}
	outDir, ok := safeRel(out)
	if !ok {
		return nil, &PathError{Source: "spec.output", Path: out}
	}
	res := &Result{Template: t.Name, Values: vals, OutputDir: outDir}

	skel := filepath.Join(t.Dir, SkeletonDir)
	seen := map[string]string{}
	err = filepath.WalkDir(skel, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skel, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		f, keep, err := renderFile(p, rel, d, scope)
		if err != nil || !keep {
			return err
		}
		if prev, dup := seen[f.Path]; dup {
			return fmt.Errorf("skeleton files %s and %s both render to %s", prev, rel, f.Path)
		}
		seen[f.Path] = rel
		res.Files = append(res.Files, f)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", t.Name, err)
	}
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	return res, nil
}

// renderFile renders one skeleton file. keep is false when a path segment
// renders empty, which is how templates express optional files.
func renderFile(abs, rel string, d fs.DirEntry, vals map[string]any) (File, bool, error) {
	segs := strings.Split(rel, "/")
	for i, seg := range segs {
		r, err := renderString(rel, seg, vals)
		if err != nil {
			return File{}, false, err
		}
		r = strings.TrimSpace(r)
		if r == "" {
			return File{}, false, nil
		}
		segs[i] = r
	}
	outPath := strings.Join(segs, "/")
	render := strings.HasSuffix(outPath, TemplateSuffix)
	outPath = strings.TrimSuffix(outPath, TemplateSuffix)
	clean, ok := safeRel(outPath)
	if !ok || clean == "." {
		return File{}, false, &PathError{Source: rel, Path: outPath}
	}
	info, err := d.Info()
	if err != nil {
		return File{}, false, err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return File{}, false, err
	}
	if render {
		s, err := renderString(rel, string(content), vals)
		if err != nil {
			return File{}, false, err
		}
		content = []byte(s)
	}
	return File{Path: clean, Content: content, Mode: info.Mode().Perm()}, true, nil
}

func parseText(name, text string) (*template.Template, error) {
	return template.New(name).Funcs(funcMap).Option("missingkey=error").Parse(text)
}

func renderString(name, text string, vals map[string]any) (string, error) {
	tpl, err := parseText(name, text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := tpl.Execute(&b, vals); err != nil {
		return "", err
	}
	return b.String(), nil
}

// safeRel cleans a slash path and reports whether it stays inside its root.
func safeRel(p string) (string, bool) {
	if p == "" || strings.Contains(p, "\\") || path.IsAbs(p) {
		return "", false
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	return c, true
}

// TargetPath returns a file's path relative to the target root.
func (r *Result) TargetPath(f File) string { return path.Join(r.OutputDir, f.Path) }

// CheckDescriptors parses every rendered catalog-info.yaml and validates the
// new entities together with an existing catalog (may be nil), so a dry run
// shows whether, e.g., the chosen owner actually exists. Only diagnostics about
// the rendered files are returned.
//
// It assumes the files are written into the existing catalog's root (the
// default target), so entities currently declared in a descriptor the result
// is about to overwrite are dropped first: re-scaffolding (or --force
// updating) an existing service replaces its declaration rather than
// duplicating it. Use CheckDescriptorsAlongside when writing elsewhere.
func (r *Result) CheckDescriptors(existing *catalog.Catalog) []catalog.Diagnostic {
	return r.checkDescriptors(existing, true)
}

// CheckDescriptorsAlongside validates the rendered descriptors against an
// existing catalog whose files they do not replace (e.g. a different
// --target directory): every existing entity is kept, so a name clash is a
// duplicate even when the relative descriptor path matches.
func (r *Result) CheckDescriptorsAlongside(existing *catalog.Catalog) []catalog.Diagnostic {
	return r.checkDescriptors(existing, false)
}

func (r *Result) checkDescriptors(existing *catalog.Catalog, replaces bool) []catalog.Diagnostic {
	var (
		newEntities []*catalog.Entity
		diags       []catalog.Diagnostic
		newFiles    = map[string]bool{}
	)
	for _, f := range r.Files {
		if path.Base(f.Path) != catalog.DefaultDescriptorName {
			continue
		}
		name := r.TargetPath(f)
		newFiles[name] = true
		es, ds := catalog.ParseDescriptors(name, f.Content)
		newEntities = append(newEntities, es...)
		diags = append(diags, ds...)
	}
	if len(newFiles) == 0 {
		return nil
	}
	var all []*catalog.Entity
	if existing != nil {
		for _, e := range existing.Entities() {
			if replaces && newFiles[e.Source.File] {
				continue // this declaration is overwritten by the rendered file
			}
			all = append(all, e)
		}
	}
	all = append(all, newEntities...)
	var out []catalog.Diagnostic
	for _, d := range catalog.Build(all, diags).Diagnostics() {
		if newFiles[d.File] {
			out = append(out, d)
		}
	}
	return out
}
