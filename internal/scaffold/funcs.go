package scaffold

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"unicode"
)

// funcMap is the deterministic helper set available inside templates. No
// helper reads the clock, environment or filesystem, so renders are
// reproducible (which the golden tests rely on).
var funcMap = template.FuncMap{
	"lower":     strings.ToLower,
	"upper":     strings.ToUpper,
	"title":     titleCase,
	"kebab":     func(s string) string { return joinWords(words(s), "-", strings.ToLower) },
	"snake":     func(s string) string { return joinWords(words(s), "_", strings.ToLower) },
	"camel":     camelCase,
	"pascal":    pascalCase,
	"trim":      strings.TrimSpace,
	"replace":   func(old, replacement, s string) string { return strings.ReplaceAll(s, old, replacement) },
	"hasPrefix": func(prefix, s string) bool { return strings.HasPrefix(s, prefix) },
	"contains":  func(sub, s string) bool { return strings.Contains(s, sub) },
	"quote":     strconv.Quote,
	"join":      joinAny,
	"default":   defaultValue,
	"indent":    indent,
	"toInt":     toInt,
	"has":       has,
}

// words splits identifiers like "payments-api", "PaymentsAPI", "payments_api v2"
// into lower-level words.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && len(cur) > 0 &&
			(unicode.IsLower(cur[len(cur)-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

func joinWords(ws []string, sep string, f func(string) string) string {
	for i := range ws {
		ws[i] = f(ws[i])
	}
	return strings.Join(ws, sep)
}

func capitalize(w string) string {
	if w == "" {
		return w
	}
	r := []rune(strings.ToLower(w))
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func titleCase(s string) string { return joinWords(words(s), " ", capitalize) }

func pascalCase(s string) string { return joinWords(words(s), "", capitalize) }

func camelCase(s string) string {
	ws := words(s)
	for i := range ws {
		if i == 0 {
			ws[i] = strings.ToLower(ws[i])
		} else {
			ws[i] = capitalize(ws[i])
		}
	}
	return strings.Join(ws, "")
}

func joinAny(sep string, v any) (string, error) {
	switch x := v.(type) {
	case []string:
		return strings.Join(x, sep), nil
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = fmt.Sprint(e)
		}
		return strings.Join(parts, sep), nil
	case nil:
		return "", nil
	}
	return "", fmt.Errorf("join: expected a list, got %T", v)
}

// defaultValue returns v unless it is nil or an empty string/list.
func defaultValue(def, v any) any {
	switch x := v.(type) {
	case nil:
		return def
	case string:
		if x == "" {
			return def
		}
	case []any:
		if len(x) == 0 {
			return def
		}
	}
	return v
}

func indent(n int, s string) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

// toInt renders JSON numbers (float64) as integers, e.g. ports.
func toInt(v any) (int64, error) {
	switch x := v.(type) {
	case float64:
		return int64(x), nil
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case string:
		return strconv.ParseInt(x, 10, 64)
	}
	return 0, fmt.Errorf("toInt: unsupported type %T", v)
}

// has reports whether a list contains a value (compared by string form).
func has(needle any, list any) bool {
	xs, ok := list.([]any)
	if !ok {
		return false
	}
	for _, x := range xs {
		if fmt.Sprint(x) == fmt.Sprint(needle) {
			return true
		}
	}
	return false
}
