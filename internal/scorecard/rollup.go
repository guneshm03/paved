package scorecard

import (
	"sort"

	"github.com/guneshm03/paved/internal/catalog"
)

// Unowned is the rollup bucket for entities without an effective owner.
const Unowned = "(unowned)"

// Stats aggregates scores of a set of entities.
type Stats struct {
	Entities  int            `json:"entities"`
	MeanScore float64        `json:"meanScore"`
	MinScore  float64        `json:"minScore"`
	Levels    map[string]int `json:"levels"`
	// FailingRules counts failures per rule id, most frequent first.
	FailingRules []RuleCount `json:"failingRules,omitempty"`
}

// RuleCount is a rule id with a count.
type RuleCount struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// TeamRollup is one row of a team rollup.
type TeamRollup struct {
	Team string `json:"team"`
	// Depth is the group's depth in the hierarchy (0 = top-level).
	Depth int `json:"depth"`
	// Direct covers entities the team owns itself.
	Direct Stats `json:"direct"`
	// Total also includes every descendant team.
	Total Stats `json:"total"`
}

// Rollup groups a report by effective owner and aggregates scores up the group
// hierarchy. Rows are in depth-first tree order (siblings by name); groups with
// no scored entities anywhere beneath them are omitted. Unowned entities form
// a final row. O(G·D + N) for G groups of depth D and N results.
func Rollup(rep *Report, cat *catalog.Catalog) []TeamRollup {
	byOwner := map[string][]EntityResult{}
	for _, r := range rep.Entities {
		key := r.Owner
		if key == "" {
			key = Unowned
		}
		byOwner[key] = append(byOwner[key], r)
	}

	// Collect each group's subtree results (itself plus descendants).
	groups := cat.Find(catalog.Filter{Kind: catalog.KindGroup})
	children := map[catalog.Ref][]catalog.Ref{}
	var roots []catalog.Ref
	for _, g := range groups {
		ref := g.Ref()
		anc := cat.Ancestors(ref)
		if len(anc) == 0 {
			roots = append(roots, ref)
		} else {
			children[anc[0]] = append(children[anc[0]], ref)
		}
	}
	var rows []TeamRollup
	visited := map[catalog.Ref]bool{}
	var walk func(ref catalog.Ref, depth int) []EntityResult
	walk = func(ref catalog.Ref, depth int) []EntityResult {
		visited[ref] = true
		direct := byOwner[ref.String()]
		idx := len(rows)
		rows = append(rows, TeamRollup{Team: ref.String(), Depth: depth, Direct: stats(direct)})
		subtree := append([]EntityResult{}, direct...)
		kids := children[ref]
		sort.Slice(kids, func(i, j int) bool { return kids[i].String() < kids[j].String() })
		for _, ch := range kids {
			if !visited[ch] {
				subtree = append(subtree, walk(ch, depth+1)...)
			}
		}
		rows[idx].Total = stats(subtree)
		return subtree
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].String() < roots[j].String() })
	for _, r := range roots {
		walk(r, 0)
	}
	// Groups caught in a hierarchy cycle have no root; list them flat.
	for _, g := range groups {
		if !visited[g.Ref()] {
			walk(g.Ref(), 0)
		}
	}
	out := rows[:0]
	for _, r := range rows {
		if r.Total.Entities > 0 {
			out = append(out, r)
		}
	}
	if un := byOwner[Unowned]; len(un) > 0 {
		out = append(out, TeamRollup{Team: Unowned, Direct: stats(un), Total: stats(un)})
	}
	return out
}

func stats(rs []EntityResult) Stats {
	s := Stats{Entities: len(rs), Levels: map[string]int{}}
	if len(rs) == 0 {
		return s
	}
	sum := 0.0
	s.MinScore = rs[0].Score
	fails := map[string]int{}
	for _, r := range rs {
		sum += r.Score
		s.MinScore = min(s.MinScore, r.Score)
		s.Levels[levelKey(r.Level)]++
		for _, rr := range r.Rules {
			if !rr.Passed {
				fails[rr.ID]++
			}
		}
	}
	s.MeanScore = round1(sum / float64(len(rs)))
	for id, n := range fails {
		s.FailingRules = append(s.FailingRules, RuleCount{ID: id, Count: n})
	}
	sort.Slice(s.FailingRules, func(i, j int) bool {
		if s.FailingRules[i].Count != s.FailingRules[j].Count {
			return s.FailingRules[i].Count > s.FailingRules[j].Count
		}
		return s.FailingRules[i].ID < s.FailingRules[j].ID
	})
	return s
}
