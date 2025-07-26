package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// specFields lists every spec key paved understands, with the kinds allowed to use it.
var specFields = map[string][]Kind{
	"type":           {KindComponent, KindAPI, KindGroup, KindResource, KindSystem},
	"lifecycle":      {KindComponent, KindAPI},
	"owner":          {KindComponent, KindAPI, KindSystem, KindResource},
	"system":         {KindComponent, KindAPI, KindResource},
	"domain":         {KindSystem},
	"subcomponentOf": {KindComponent},
	"providesApis":   {KindComponent},
	"consumesApis":   {KindComponent},
	"dependsOn":      {KindComponent, KindResource},
	"definition":     {KindAPI},
	"parent":         {KindGroup},
	"children":       {KindGroup},
	"members":        {KindGroup},
	"profile":        {KindGroup},
}

type rawSpec struct {
	Type           string        `yaml:"type"`
	Lifecycle      string        `yaml:"lifecycle"`
	Owner          string        `yaml:"owner"`
	System         string        `yaml:"system"`
	Domain         string        `yaml:"domain"`
	SubcomponentOf string        `yaml:"subcomponentOf"`
	ProvidesAPIs   []string      `yaml:"providesApis"`
	ConsumesAPIs   []string      `yaml:"consumesApis"`
	DependsOn      []string      `yaml:"dependsOn"`
	Definition     any           `yaml:"definition"`
	Parent         string        `yaml:"parent"`
	Children       []string      `yaml:"children"`
	Members        []string      `yaml:"members"`
	Profile        *GroupProfile `yaml:"profile"`
}

type rawMetadata struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace"`
	Title       string            `yaml:"title"`
	Description string            `yaml:"description"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
	Tags        []string          `yaml:"tags"`
	Links       []Link            `yaml:"links"`
}

// ParseDescriptors parses every YAML document in data. file is the
// slash-separated path used in diagnostics. Parse problems are reported as
// diagnostics; documents that cannot be understood are skipped.
func ParseDescriptors(file string, data []byte) ([]*Entity, []Diagnostic) {
	var (
		out   []*Entity
		diags []Diagnostic
	)
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Code: "yaml-syntax", File: file,
				Line: yamlErrLine(err), Message: err.Error(),
			})
			break // the decoder cannot resynchronise after a syntax error
		}
		e, ds := parseDocument(file, &doc)
		diags = append(diags, ds...)
		if e != nil {
			out = append(out, e)
		}
	}
	return out, diags
}

func parseDocument(file string, doc *yaml.Node) (*Entity, []Diagnostic) {
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return nil, nil // empty document between separators
	}
	diag := func(sev Severity, code string, line int, format string, args ...any) Diagnostic {
		return Diagnostic{Severity: sev, Code: code, File: file, Line: line, Message: fmt.Sprintf(format, args...)}
	}
	if root.Kind != yaml.MappingNode {
		return nil, []Diagnostic{diag(SeverityError, "invalid-document", root.Line, "descriptor must be a mapping")}
	}
	src := Source{File: file, Line: root.Line, lines: map[string]int{}}
	recordLines(root, "", src.lines)

	var diags []Diagnostic
	e := &Entity{Source: src}
	fields := mappingFields(root)

	for key, n := range fields {
		switch key {
		case "apiVersion", "kind", "metadata", "spec", "relations", "status":
		default:
			diags = append(diags, diag(SeverityWarning, "unknown-field", n.Line, "unknown top-level field %q", key))
		}
	}

	if n, ok := fields["apiVersion"]; ok {
		e.APIVersion = n.Value
	}
	kindNode, ok := fields["kind"]
	if !ok || kindNode.Value == "" {
		return nil, append(diags, diag(SeverityError, "missing-kind", root.Line, "descriptor has no kind"))
	}
	kind, ok := ParseKind(kindNode.Value)
	if !ok {
		return nil, append(diags, diag(SeverityError, "unsupported-kind", kindNode.Line,
			"kind %q is not supported (supported: Component, API, System, Group, Resource)", kindNode.Value))
	}
	e.Kind = kind

	if n, ok := fields["metadata"]; ok {
		var m rawMetadata
		if err := n.Decode(&m); err != nil {
			diags = append(diags, decodeDiags(file, n.Line, "metadata", err)...)
		}
		e.Metadata = Metadata(m)
	}
	if e.Metadata.Namespace == "" {
		e.Metadata.Namespace = DefaultNamespace
	}

	if n, ok := fields["spec"]; ok {
		if n.Kind != yaml.MappingNode {
			diags = append(diags, diag(SeverityError, "invalid-field", n.Line, "spec must be a mapping"))
		} else {
			for key, v := range mappingFields(n) {
				kinds, known := specFields[key]
				switch {
				case !known:
					diags = append(diags, diag(SeverityWarning, "unknown-field", v.Line, "unknown spec field %q for kind %s", key, kind))
				case !containsKind(kinds, kind):
					diags = append(diags, diag(SeverityWarning, "unexpected-field", v.Line, "spec.%s is not used by kind %s", key, kind))
				}
			}
			var s rawSpec
			if err := n.Decode(&s); err != nil {
				diags = append(diags, decodeDiags(file, n.Line, "spec", err)...)
			}
			e.Spec = Spec{
				Type: s.Type, Lifecycle: s.Lifecycle, Owner: s.Owner, System: s.System, Domain: s.Domain,
				SubcomponentOf: s.SubcomponentOf, ProvidesAPIs: s.ProvidesAPIs, ConsumesAPIs: s.ConsumesAPIs,
				DependsOn: s.DependsOn, Definition: definitionString(s.Definition), Parent: s.Parent,
				Children: s.Children, Members: s.Members, Profile: s.Profile,
			}
		}
	}
	return e, diags
}

// definitionString accepts an inline string or a {$text|$yaml|$json: path} substitution.
func definitionString(v any) string {
	switch d := v.(type) {
	case string:
		return d
	case map[string]any:
		for _, k := range []string{"$text", "$yaml", "$json", "$openapi"} {
			if s, ok := d[k].(string); ok {
				return k + ":" + s
			}
		}
	}
	return ""
}

func containsKind(ks []Kind, k Kind) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

// mappingFields returns key -> value node for a mapping node.
func mappingFields(n *yaml.Node) map[string]*yaml.Node {
	out := make(map[string]*yaml.Node, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = n.Content[i+1]
	}
	return out
}

// recordLines stores the line of every mapping key and sequence item under a
// dotted path such as "spec.dependsOn.1".
func recordLines(n *yaml.Node, prefix string, lines map[string]int) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + "." + k
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			p := join(n.Content[i].Value)
			lines[p] = n.Content[i].Line
			recordLines(n.Content[i+1], p, lines)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			p := join(strconv.Itoa(i))
			lines[p] = c.Line
			recordLines(c, p, lines)
		}
	}
}

func decodeDiags(file string, fallbackLine int, section string, err error) []Diagnostic {
	var te *yaml.TypeError
	if errors.As(err, &te) {
		out := make([]Diagnostic, 0, len(te.Errors))
		for _, msg := range te.Errors {
			out = append(out, Diagnostic{
				Severity: SeverityError, Code: "invalid-field", File: file,
				Line: yamlErrLine(errors.New(msg)), Message: section + ": " + stripLinePrefix(msg),
			})
		}
		return out
	}
	return []Diagnostic{{Severity: SeverityError, Code: "invalid-field", File: file, Line: fallbackLine, Message: section + ": " + err.Error()}}
}

// yamlErrLine extracts "line N" from a yaml error message, or 0.
func yamlErrLine(err error) int {
	msg := err.Error()
	i := strings.Index(msg, "line ")
	if i < 0 {
		return 0
	}
	rest := msg[i+5:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	n, convErr := strconv.Atoi(rest[:end])
	if convErr != nil {
		return 0
	}
	return n
}

func stripLinePrefix(msg string) string {
	if strings.HasPrefix(msg, "line ") {
		if i := strings.Index(msg, ": "); i >= 0 {
			return msg[i+2:]
		}
	}
	return msg
}
