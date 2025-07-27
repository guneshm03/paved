package scorecard

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/guneshm03/paved/internal/facts"
	"go.yaml.in/yaml/v3"
)

// Condition is a compiled rule predicate over facts.
type Condition interface {
	// Eval returns whether the condition holds and a human-readable reason.
	Eval(f facts.Facts) (bool, string)
	// Facts lists the fact names the condition reads.
	Facts() []string
}

// Comparison operators accepted in a fact condition.
var operators = map[string]bool{
	"equals": true, "notEquals": true, "gt": true, "gte": true, "lt": true, "lte": true,
	"matches": true, "exists": true, "in": true, "contains": true,
}

// ParseCondition compiles a YAML condition node. Forms:
//
//	{fact: ci.present, equals: true}
//	{all: [cond, ...]} / {any: [cond, ...]} / {not: cond}
func ParseCondition(n *yaml.Node) (Condition, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: condition must be a mapping", n.Line)
	}
	fields := map[string]*yaml.Node{}
	var keys []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		fields[n.Content[i].Value] = n.Content[i+1]
		keys = append(keys, n.Content[i].Value)
	}
	if v, ok := fields["all"]; ok {
		return parseGroup("all", v, len(keys))
	}
	if v, ok := fields["any"]; ok {
		return parseGroup("any", v, len(keys))
	}
	if v, ok := fields["not"]; ok {
		if len(keys) != 1 {
			return nil, fmt.Errorf("line %d: 'not' must be the only key", n.Line)
		}
		inner, err := ParseCondition(v)
		if err != nil {
			return nil, err
		}
		return &notCond{inner: inner}, nil
	}
	factNode, ok := fields["fact"]
	if !ok || factNode.Value == "" {
		return nil, fmt.Errorf("line %d: condition needs 'fact' (or all/any/not)", n.Line)
	}
	var op string
	for _, k := range keys {
		if k == "fact" {
			continue
		}
		if !operators[k] {
			return nil, fmt.Errorf("line %d: unknown operator %q", fields[k].Line, k)
		}
		if op != "" {
			return nil, fmt.Errorf("line %d: condition has more than one operator (%s, %s)", n.Line, op, k)
		}
		op = k
	}
	if op == "" {
		return nil, fmt.Errorf("line %d: condition on %s has no operator", n.Line, factNode.Value)
	}
	c := &factCond{fact: factNode.Value, op: op}
	var raw any
	if err := fields[op].Decode(&raw); err != nil {
		return nil, fmt.Errorf("line %d: %w", fields[op].Line, err)
	}
	c.want = normalize(raw)
	if err := c.check(); err != nil {
		return nil, fmt.Errorf("line %d: %w", fields[op].Line, err)
	}
	return c, nil
}

func parseGroup(kind string, v *yaml.Node, nkeys int) (Condition, error) {
	if nkeys != 1 {
		return nil, fmt.Errorf("line %d: '%s' must be the only key", v.Line, kind)
	}
	if v.Kind != yaml.SequenceNode || len(v.Content) == 0 {
		return nil, fmt.Errorf("line %d: '%s' needs a non-empty list", v.Line, kind)
	}
	g := &groupCond{all: kind == "all"}
	for _, item := range v.Content {
		c, err := ParseCondition(item)
		if err != nil {
			return nil, err
		}
		g.items = append(g.items, c)
	}
	return g, nil
}

func normalize(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalize(x[i])
		}
		return out
	}
	return v
}

type factCond struct {
	fact string
	op   string
	want any
	re   *regexp.Regexp
}

// check validates operand types at compile time.
func (c *factCond) check() error {
	switch c.op {
	case "gt", "gte", "lt", "lte":
		if _, ok := c.want.(float64); !ok {
			return fmt.Errorf("%s needs a number, got %v", c.op, c.want)
		}
	case "matches":
		s, ok := c.want.(string)
		if !ok {
			return fmt.Errorf("matches needs a regular expression string")
		}
		re, err := regexp.Compile(s)
		if err != nil {
			return fmt.Errorf("matches: %w", err)
		}
		c.re = re
	case "exists":
		if _, ok := c.want.(bool); !ok {
			return fmt.Errorf("exists needs true or false")
		}
	case "in":
		if _, ok := c.want.([]any); !ok {
			return fmt.Errorf("in needs a list")
		}
	}
	return nil
}

func (c *factCond) Facts() []string { return []string{c.fact} }

func (c *factCond) Eval(f facts.Facts) (bool, string) {
	got, present := f[c.fact]
	if c.op == "exists" {
		want := c.want.(bool)
		if present == want {
			return true, fmt.Sprintf("%s exists=%v", c.fact, present)
		}
		return false, fmt.Sprintf("%s exists=%v, want %v", c.fact, present, want)
	}
	if !present {
		return false, fmt.Sprintf("%s is not available", c.fact)
	}
	if c.compare(got) {
		return true, fmt.Sprintf("%s = %s (%s %s)", c.fact, show(got), opSymbol(c.op), show(c.want))
	}
	return false, fmt.Sprintf("%s = %s, want %s %s", c.fact, show(got), opSymbol(c.op), show(c.want))
}

func (c *factCond) compare(got any) bool {
	switch c.op {
	case "equals":
		return equal(got, c.want)
	case "notEquals":
		return !equal(got, c.want)
	case "gt", "gte", "lt", "lte":
		g, ok := got.(float64)
		if !ok {
			return false
		}
		w := c.want.(float64)
		switch c.op {
		case "gt":
			return g > w
		case "gte":
			return g >= w
		case "lt":
			return g < w
		}
		return g <= w
	case "matches":
		s, ok := got.(string)
		return ok && c.re.MatchString(s)
	case "in":
		for _, w := range c.want.([]any) {
			if equal(got, w) {
				return true
			}
		}
		return false
	case "contains":
		switch g := got.(type) {
		case []string:
			for _, x := range g {
				if equal(x, c.want) {
					return true
				}
			}
		case string:
			w, ok := c.want.(string)
			return ok && strings.Contains(g, w)
		}
	}
	return false
}

func equal(a, b any) bool {
	if fa, ok := a.(float64); ok {
		fb, ok := b.(float64)
		return ok && fa == fb
	}
	return a == b
}

func opSymbol(op string) string {
	switch op {
	case "equals":
		return "=="
	case "notEquals":
		return "!="
	case "gt":
		return ">"
	case "gte":
		return ">="
	case "lt":
		return "<"
	case "lte":
		return "<="
	}
	return op
}

func show(v any) string {
	switch x := v.(type) {
	case string:
		if x == "" {
			return `""`
		}
		return strconv.Quote(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []string:
		return "[" + strings.Join(x, ", ") + "]"
	case []any:
		parts := make([]string, len(x))
		for i := range x {
			parts[i] = show(x[i])
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprint(v)
}

type groupCond struct {
	all   bool
	items []Condition
}

func (g *groupCond) Facts() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range g.items {
		for _, f := range c.Facts() {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Eval short-circuits: all stops at the first failure, any at the first pass.
// The reason names the deciding sub-condition(s).
func (g *groupCond) Eval(f facts.Facts) (bool, string) {
	var reasons []string
	for _, c := range g.items {
		ok, why := c.Eval(f)
		if g.all && !ok {
			return false, why
		}
		if !g.all && ok {
			return true, why
		}
		reasons = append(reasons, why)
	}
	if g.all {
		return true, strings.Join(reasons, "; ")
	}
	return false, "none matched: " + strings.Join(reasons, "; ")
}

type notCond struct{ inner Condition }

func (n *notCond) Facts() []string { return n.inner.Facts() }

func (n *notCond) Eval(f facts.Facts) (bool, string) {
	ok, why := n.inner.Eval(f)
	return !ok, "not(" + why + ")"
}
