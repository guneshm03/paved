// Package version parses and orders the version strings found in dependency
// manifests (Go modules, npm, PyPI). It implements semantic-version precedence
// for any number of numeric segments plus an optional pre-release suffix.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed version. Missing trailing segments compare as zero.
type Version struct {
	Raw        string
	Segments   []int
	Prerelease []string
}

// Parse accepts forms like "v1.2.3", "1.2", "2.31.0", "1.0.0-rc.1",
// "v0.0.0-20240101-abcdef" (Go pseudo-versions) and "1.2.3+build".
func Parse(s string) (Version, error) {
	raw := s
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i] // build metadata does not affect precedence
	}
	var pre []string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		if i+1 >= len(s) {
			return Version{}, fmt.Errorf("version %q: empty pre-release", raw)
		}
		pre = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	if s == "" {
		return Version{}, fmt.Errorf("version %q: no numeric part", raw)
	}
	parts := strings.Split(s, ".")
	segs := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("version %q: segment %q is not a number", raw, p)
		}
		segs[i] = n
	}
	return Version{Raw: raw, Segments: segs, Prerelease: pre}, nil
}

// Major returns the first segment.
func (v Version) Major() int { return v.seg(0) }

func (v Version) seg(i int) int {
	if i < len(v.Segments) {
		return v.Segments[i]
	}
	return 0
}

// IsPrerelease reports whether the version carries a pre-release suffix.
func (v Version) IsPrerelease() bool { return len(v.Prerelease) > 0 }

// Compare returns -1, 0 or +1. A pre-release sorts before its release.
func Compare(a, b Version) int {
	n := max(len(a.Segments), len(b.Segments))
	for i := range n {
		if c := cmpInt(a.seg(i), b.seg(i)); c != 0 {
			return c
		}
	}
	switch {
	case len(a.Prerelease) == 0 && len(b.Prerelease) == 0:
		return 0
	case len(a.Prerelease) == 0:
		return 1
	case len(b.Prerelease) == 0:
		return -1
	}
	for i := 0; i < len(a.Prerelease) && i < len(b.Prerelease); i++ {
		if c := cmpIdent(a.Prerelease[i], b.Prerelease[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a.Prerelease), len(b.Prerelease))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpIdent orders pre-release identifiers: numeric ones numerically and below
// alphanumeric ones, which compare lexically.
func cmpIdent(a, b string) int {
	na, ea := strconv.Atoi(a)
	nb, eb := strconv.Atoi(b)
	switch {
	case ea == nil && eb == nil:
		return cmpInt(na, nb)
	case ea == nil:
		return -1
	case eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// StripRange removes npm/PyPI range operators so "^1.2.3", "~1.2", ">=2.0"
// and "==2.31.0" yield the base version that the range starts from.
func StripRange(spec string) string {
	s := strings.TrimSpace(spec)
	for _, op := range []string{"==", ">=", "~=", "<=", "^", "~", "=", ">", "<"} {
		if strings.HasPrefix(s, op) {
			s = strings.TrimSpace(s[len(op):])
			break
		}
	}
	if i := strings.IndexAny(s, " ,|"); i >= 0 {
		s = s[:i]
	}
	return s
}
