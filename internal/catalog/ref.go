// Package catalog loads Backstage-style catalog-info.yaml descriptors, validates
// them, and builds a typed relation graph that answers ownership and dependency
// questions.
package catalog

import (
	"fmt"
	"regexp"
	"strings"
)

// Kind is an entity kind. paved supports the Backstage subset Component, API,
// System, Group and Resource.
type Kind string

// Supported kinds.
const (
	KindComponent Kind = "Component"
	KindAPI       Kind = "API"
	KindSystem    Kind = "System"
	KindGroup     Kind = "Group"
	KindResource  Kind = "Resource"
)

// DefaultNamespace is used when a descriptor or reference omits a namespace.
const DefaultNamespace = "default"

// AllKinds lists the supported kinds in a stable order.
var AllKinds = []Kind{KindComponent, KindAPI, KindSystem, KindGroup, KindResource}

// ParseKind resolves a kind name case-insensitively.
func ParseKind(s string) (Kind, bool) {
	for _, k := range AllKinds {
		if strings.EqualFold(string(k), s) {
			return k, true
		}
	}
	return "", false
}

var (
	// Backstage name rules: 1-63 chars, alphanumeric separated by [-_.].
	nameRe      = regexp.MustCompile(`^[A-Za-z0-9]([-_.A-Za-z0-9]{0,61}[A-Za-z0-9])?$`)
	namespaceRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
)

// ValidName reports whether s is a valid metadata.name.
func ValidName(s string) bool { return nameRe.MatchString(s) && !strings.Contains(s, "--") }

// ValidNamespace reports whether s is a valid metadata.namespace.
func ValidNamespace(s string) bool { return namespaceRe.MatchString(s) }

// Ref uniquely identifies an entity: kind:namespace/name. Name and namespace
// are compared case-insensitively, as in Backstage, so they are stored lower-cased.
type Ref struct {
	Kind      Kind   `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// NewRef builds a normalized reference.
func NewRef(kind Kind, namespace, name string) Ref {
	if namespace == "" {
		namespace = DefaultNamespace
	}
	return Ref{Kind: kind, Namespace: strings.ToLower(namespace), Name: strings.ToLower(name)}
}

// String renders the canonical "kind:namespace/name" form with a lower-case kind.
func (r Ref) String() string {
	return strings.ToLower(string(r.Kind)) + ":" + r.Namespace + "/" + r.Name
}

// Short renders the reference relative to a context namespace, omitting the
// namespace when it matches.
func (r Ref) Short() string {
	if r.Namespace == DefaultNamespace {
		return strings.ToLower(string(r.Kind)) + ":" + r.Name
	}
	return r.String()
}

// IsZero reports whether r is the zero value.
func (r Ref) IsZero() bool { return r == Ref{} }

// MarshalText implements encoding.TextMarshaler so refs can be map keys in JSON.
func (r Ref) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

// RefError describes a malformed entity reference.
type RefError struct {
	Input  string
	Reason string
}

func (e *RefError) Error() string {
	return fmt.Sprintf("invalid entity reference %q: %s", e.Input, e.Reason)
}

// ParseRef parses "[kind:][namespace/]name". defaultKind is used when the kind
// is omitted; if it is empty, a kind is required. defaultNamespace is used when
// the namespace is omitted.
func ParseRef(s string, defaultKind Kind, defaultNamespace string) (Ref, error) {
	in := strings.TrimSpace(s)
	if in == "" {
		return Ref{}, &RefError{Input: s, Reason: "empty"}
	}
	kind := defaultKind
	rest := in
	if i := strings.Index(in, ":"); i >= 0 {
		k, ok := ParseKind(in[:i])
		if !ok {
			return Ref{}, &RefError{Input: s, Reason: fmt.Sprintf("unsupported kind %q", in[:i])}
		}
		kind = k
		rest = in[i+1:]
	}
	if kind == "" {
		return Ref{}, &RefError{Input: s, Reason: "kind is required (e.g. component:name)"}
	}
	ns, name, hasNS := strings.Cut(rest, "/")
	switch {
	case !hasNS:
		ns, name = defaultNamespace, rest
	case !ValidNamespace(strings.ToLower(ns)):
		return Ref{}, &RefError{Input: s, Reason: fmt.Sprintf("invalid namespace %q", ns)}
	}
	if !ValidName(name) {
		return Ref{}, &RefError{Input: s, Reason: fmt.Sprintf("invalid name %q", name)}
	}
	return NewRef(kind, ns, name), nil
}

// MustParseRef is ParseRef for trusted literals; it panics on error.
func MustParseRef(s string) Ref {
	r, err := ParseRef(s, "", DefaultNamespace)
	if err != nil {
		panic(err)
	}
	return r
}
