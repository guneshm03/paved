package catalog

import (
	"fmt"
	"path"
	"sort"
)

// Supported apiVersion values for descriptors.
var supportedAPIVersions = map[string]bool{
	"backstage.io/v1alpha1": true,
	"backstage.io/v1beta1":  true,
}

// Link is an entry of metadata.links.
type Link struct {
	URL   string `json:"url" yaml:"url"`
	Title string `json:"title,omitempty" yaml:"title"`
	Icon  string `json:"icon,omitempty" yaml:"icon"`
	Type  string `json:"type,omitempty" yaml:"type"`
}

// Metadata is the metadata block shared by all kinds.
type Metadata struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Links       []Link            `json:"links,omitempty"`
}

// GroupProfile is spec.profile of a Group.
type GroupProfile struct {
	DisplayName string `json:"displayName,omitempty" yaml:"displayName"`
	Email       string `json:"email,omitempty" yaml:"email"`
}

// Spec is the union of the spec fields of the supported kinds. Which fields are
// allowed or required for a given kind is enforced by validation, not by the type.
type Spec struct {
	Type           string        `json:"type,omitempty"`
	Lifecycle      string        `json:"lifecycle,omitempty"`
	Owner          string        `json:"owner,omitempty"`
	System         string        `json:"system,omitempty"`
	Domain         string        `json:"domain,omitempty"`
	SubcomponentOf string        `json:"subcomponentOf,omitempty"`
	ProvidesAPIs   []string      `json:"providesApis,omitempty"`
	ConsumesAPIs   []string      `json:"consumesApis,omitempty"`
	DependsOn      []string      `json:"dependsOn,omitempty"`
	Definition     string        `json:"definition,omitempty"`
	Parent         string        `json:"parent,omitempty"`
	Children       []string      `json:"children,omitempty"`
	Members        []string      `json:"members,omitempty"`
	Profile        *GroupProfile `json:"profile,omitempty"`
}

// Source records where an entity was declared.
type Source struct {
	// File is the descriptor path relative to the catalog root, slash-separated.
	File string `json:"file"`
	// Line is the 1-based line of the document start.
	Line int `json:"line"`
	// lines maps dotted field paths (e.g. "spec.owner") to their line numbers.
	lines map[string]int
}

// Dir is the directory holding the descriptor, relative to the catalog root.
func (s Source) Dir() string { return path.Dir(s.File) }

// LineOf returns the line of a dotted field path, falling back to the document line.
func (s Source) LineOf(field string) int {
	if l, ok := s.lines[field]; ok {
		return l
	}
	return s.Line
}

// Entity is one parsed catalog descriptor.
type Entity struct {
	APIVersion string   `json:"apiVersion"`
	Kind       Kind     `json:"kind"`
	Metadata   Metadata `json:"metadata"`
	Spec       Spec     `json:"spec"`
	Source     Source   `json:"source"`
}

// Ref returns the entity's own reference.
func (e *Entity) Ref() Ref { return NewRef(e.Kind, e.Metadata.Namespace, e.Metadata.Name) }

// DisplayName returns the title if set, else the name.
func (e *Entity) DisplayName() string {
	if e.Metadata.Title != "" {
		return e.Metadata.Title
	}
	return e.Metadata.Name
}

// Annotation returns an annotation value.
func (e *Entity) Annotation(key string) string { return e.Metadata.Annotations[key] }

// HasTag reports whether the entity carries a tag.
func (e *Entity) HasTag(tag string) bool {
	for _, t := range e.Metadata.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// Severity grades a diagnostic.
type Severity string

// Severities.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is a validation or parse finding tied to a source location.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	File     string   `json:"file,omitempty"`
	Line     int      `json:"line,omitempty"`
	Entity   string   `json:"entity,omitempty"`
}

func (d Diagnostic) String() string {
	loc := d.File
	if d.Line > 0 {
		loc = fmt.Sprintf("%s:%d", d.File, d.Line)
	}
	if loc == "" {
		loc = "<catalog>"
	}
	return fmt.Sprintf("%s: %s [%s] %s", loc, d.Severity, d.Code, d.Message)
}

// SortDiagnostics orders diagnostics by file, line, then code for stable output.
func SortDiagnostics(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
}

// CountBySeverity returns (errors, warnings).
func CountBySeverity(ds []Diagnostic) (errs, warns int) {
	for _, d := range ds {
		if d.Severity == SeverityError {
			errs++
		} else {
			warns++
		}
	}
	return errs, warns
}
