package scorecard

import (
	"math"
	"sort"

	"github.com/guneshm03/paved/internal/catalog"
	"github.com/guneshm03/paved/internal/facts"
)

// RuleResult is the outcome of one rule for one entity.
type RuleResult struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Weight      float64 `json:"weight"`
	Passed      bool    `json:"passed"`
	Reason      string  `json:"reason"`
	Remediation string  `json:"remediation,omitempty"`
}

// EntityResult is one entity's score on a scorecard.
type EntityResult struct {
	Ref   catalog.Ref `json:"ref"`
	Name  string      `json:"name"`
	Owner string      `json:"owner"`
	// Score is the passed weight as a percentage of total weight (0..100).
	Score  float64      `json:"score"`
	Level  string       `json:"level"`
	Passed int          `json:"passed"`
	Failed int          `json:"failed"`
	Rules  []RuleResult `json:"rules"`
}

// RuleSummary aggregates one rule across all evaluated entities.
type RuleSummary struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Weight   float64 `json:"weight"`
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	PassRate float64 `json:"passRate"`
}

// Report is the evaluation of one scorecard over a catalog.
type Report struct {
	Scorecard   string         `json:"scorecard"`
	Title       string         `json:"title"`
	Levels      []Level        `json:"levels"`
	Rules       []RuleSummary  `json:"rules"`
	Entities    []EntityResult `json:"entities"`
	MeanScore   float64        `json:"meanScore"`
	LevelCounts map[string]int `json:"levelCounts"`
}

// Evaluator scores entities, caching collected facts so several scorecards
// can be evaluated over one catalog without re-reading the repositories.
type Evaluator struct {
	Collector *facts.Collector
	cache     map[catalog.Ref]facts.Facts
}

// NewEvaluator returns an evaluator over the collector's catalog.
func NewEvaluator(c *facts.Collector) *Evaluator {
	return &Evaluator{Collector: c, cache: map[catalog.Ref]facts.Facts{}}
}

// FactsFor returns (and caches) the facts of e.
func (ev *Evaluator) FactsFor(e *catalog.Entity) (facts.Facts, error) {
	if f, ok := ev.cache[e.Ref()]; ok {
		return f, nil
	}
	f, err := ev.Collector.Collect(e)
	if err != nil {
		return nil, err
	}
	ev.cache[e.Ref()] = f
	return f, nil
}

// EvaluateEntity scores one entity.
func (ev *Evaluator) EvaluateEntity(sc *Scorecard, e *catalog.Entity) (EntityResult, error) {
	f, err := ev.FactsFor(e)
	if err != nil {
		return EntityResult{}, err
	}
	owner, _ := f["owner.effective"].(string)
	res := EntityResult{Ref: e.Ref(), Name: e.Metadata.Name, Owner: owner}
	passedWeight := 0.0
	for _, r := range sc.Rules {
		ok, why := r.Check.Eval(f)
		rr := RuleResult{ID: r.ID, Title: r.Title, Weight: r.Weight, Passed: ok, Reason: why}
		if ok {
			passedWeight += r.Weight
			res.Passed++
		} else {
			rr.Remediation = r.Remediation
			res.Failed++
		}
		res.Rules = append(res.Rules, rr)
	}
	res.Score = round1(100 * passedWeight / sc.TotalWeight())
	res.Level = sc.LevelFor(res.Score)
	return res, nil
}

// Evaluate scores every in-scope entity of the catalog. Entities are ordered
// by ascending score (worst first), then by reference.
func (ev *Evaluator) Evaluate(sc *Scorecard) (*Report, error) {
	rep := &Report{Scorecard: sc.Name, Title: sc.Title, Levels: sc.Levels, LevelCounts: map[string]int{}}
	summaries := make([]RuleSummary, len(sc.Rules))
	for i, r := range sc.Rules {
		summaries[i] = RuleSummary{ID: r.ID, Title: r.Title, Weight: r.Weight}
	}
	total := 0.0
	for _, e := range ev.Collector.Catalog.Entities() {
		if !sc.AppliesTo.Matches(e) {
			continue
		}
		res, err := ev.EvaluateEntity(sc, e)
		if err != nil {
			return nil, err
		}
		for i, rr := range res.Rules {
			if rr.Passed {
				summaries[i].Passed++
			} else {
				summaries[i].Failed++
			}
		}
		total += res.Score
		rep.LevelCounts[levelKey(res.Level)]++
		rep.Entities = append(rep.Entities, res)
	}
	for i := range summaries {
		if n := summaries[i].Passed + summaries[i].Failed; n > 0 {
			summaries[i].PassRate = round1(100 * float64(summaries[i].Passed) / float64(n))
		}
	}
	rep.Rules = summaries
	if len(rep.Entities) > 0 {
		rep.MeanScore = round1(total / float64(len(rep.Entities)))
	}
	sort.SliceStable(rep.Entities, func(i, j int) bool {
		if rep.Entities[i].Score != rep.Entities[j].Score {
			return rep.Entities[i].Score < rep.Entities[j].Score
		}
		return rep.Entities[i].Ref.String() < rep.Entities[j].Ref.String()
	})
	return rep, nil
}

// NoLevel is the bucket name for entities below the lowest level.
const NoLevel = "none"

func levelKey(l string) string {
	if l == "" {
		return NoLevel
	}
	return l
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
