package scorecard

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/guneshm03/paved/internal/catalog"
)

// WriteText renders a report as aligned plain-text tables. With details set,
// every failing rule of every entity is listed with its reason.
func WriteText(w io.Writer, rep *Report, details bool) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Scorecard: %s (%s)\n", rep.Title, rep.Scorecard)
	fmt.Fprintf(&b, "Entities: %d   Mean score: %.1f   Levels: %s\n\n", len(rep.Entities), rep.MeanScore, levelSummary(rep))

	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ENTITY\tOWNER\tSCORE\tLEVEL\tFAILED RULES")
	for _, e := range rep.Entities {
		var failed []string
		for _, r := range e.Rules {
			if !r.Passed {
				failed = append(failed, r.ID)
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%5.1f\t%s\t%s\n", e.Ref.Short(), dash(shortRef(e.Owner)), e.Score, levelKey(e.Level), dash(strings.Join(failed, ",")))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	b.WriteString("\n")
	tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tWEIGHT\tPASSED\tFAILED\tPASS RATE")
	for _, r := range rep.Rules {
		fmt.Fprintf(tw, "%s\t%g\t%d\t%d\t%5.1f%%\n", r.ID, r.Weight, r.Passed, r.Failed, r.PassRate)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if details {
		for _, e := range rep.Entities {
			if e.Failed == 0 {
				continue
			}
			fmt.Fprintf(&b, "\n%s (%.1f):\n", e.Ref.Short(), e.Score)
			for _, r := range e.Rules {
				if r.Passed {
					continue
				}
				fmt.Fprintf(&b, "  x %s: %s\n", r.ID, r.Reason)
				if r.Remediation != "" {
					fmt.Fprintf(&b, "    fix: %s\n", r.Remediation)
				}
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteRollupText renders a team rollup as an indented tree table.
func WriteRollupText(w io.Writer, rep *Report, rows []TeamRollup) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Team rollup: %s\n\n", rep.Title)
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TEAM\tOWNED\tMEAN\tSUBTREE\tSUBTREE MEAN\tMIN\tLEVELS\tTOP FAILING")
	for _, r := range rows {
		directMean := "-"
		if r.Direct.Entities > 0 {
			directMean = fmt.Sprintf("%.1f", r.Direct.MeanScore)
		}
		fmt.Fprintf(tw, "%s%s\t%d\t%s\t%d\t%.1f\t%.1f\t%s\t%s\n",
			strings.Repeat("  ", r.Depth), shortRef(r.Team), r.Direct.Entities, directMean,
			r.Total.Entities, r.Total.MeanScore, r.Total.MinScore,
			levelsString(r.Total.Levels, rep.Levels), topFailing(r.Total.FailingRules, 2))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteMarkdown renders a report plus team rollup as a Markdown document.
func WriteMarkdown(w io.Writer, rep *Report, rows []TeamRollup) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Scorecard: %s\n\n", rep.Title)
	fmt.Fprintf(&b, "- Entities evaluated: **%d**\n- Mean score: **%.1f**\n- Levels: %s\n\n", len(rep.Entities), rep.MeanScore, levelSummary(rep))

	b.WriteString("## Entities\n\n| Entity | Owner | Score | Level | Failing rules |\n|---|---|---:|---|---|\n")
	for _, e := range rep.Entities {
		var failed []string
		for _, r := range e.Rules {
			if !r.Passed {
				failed = append(failed, "`"+r.ID+"`")
			}
		}
		fmt.Fprintf(&b, "| `%s` | %s | %.1f | %s | %s |\n", e.Ref.Short(), mdDash(e.Owner), e.Score, levelKey(e.Level), dash(strings.Join(failed, ", ")))
	}

	b.WriteString("\n## Rules\n\n| Rule | Weight | Passed | Failed | Pass rate |\n|---|---:|---:|---:|---:|\n")
	for _, r := range rep.Rules {
		fmt.Fprintf(&b, "| %s (`%s`) | %g | %d | %d | %.1f%% |\n", r.Title, r.ID, r.Weight, r.Passed, r.Failed, r.PassRate)
	}

	if len(rows) > 0 {
		b.WriteString("\n## Team rollup\n\n| Team | Owned | Mean | Subtree | Subtree mean | Min | Top failing |\n|---|---:|---:|---:|---:|---:|---|\n")
		for _, r := range rows {
			mean := "-"
			if r.Direct.Entities > 0 {
				mean = fmt.Sprintf("%.1f", r.Direct.MeanScore)
			}
			fmt.Fprintf(&b, "| %s`%s` | %d | %s | %d | %.1f | %.1f | %s |\n",
				strings.Repeat("&nbsp;&nbsp;", r.Depth), r.Team, r.Direct.Entities, mean,
				r.Total.Entities, r.Total.MeanScore, r.Total.MinScore, topFailing(r.Total.FailingRules, 3))
		}
	}

	b.WriteString("\n## Findings\n")
	for _, e := range rep.Entities {
		if e.Failed == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### `%s` — %.1f\n\n", e.Ref.Short(), e.Score)
		for _, r := range e.Rules {
			if r.Passed {
				continue
			}
			fmt.Fprintf(&b, "- **%s**: %s", r.Title, r.Reason)
			if r.Remediation != "" {
				fmt.Fprintf(&b, " — _%s_", r.Remediation)
			}
			b.WriteString("\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func levelSummary(rep *Report) string {
	return levelsString(rep.LevelCounts, rep.Levels)
}

// levelsString prints counts from the highest level down, then "none".
func levelsString(counts map[string]int, levels []Level) string {
	var parts []string
	for i := len(levels) - 1; i >= 0; i-- {
		if n := counts[levels[i].Name]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", levels[i].Name, n))
		}
	}
	if n := counts[NoLevel]; n > 0 {
		parts = append(parts, fmt.Sprintf("%s=%d", NoLevel, n))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

// topFailing renders the first n entries of an already-sorted failure list.
func topFailing(rs []RuleCount, n int) string {
	if len(rs) == 0 {
		return "-"
	}
	parts := make([]string, 0, n)
	for _, r := range rs[:min(n, len(rs))] {
		parts = append(parts, fmt.Sprintf("%s(%d)", r.ID, r.Count))
	}
	return strings.Join(parts, " ")
}

// shortRef drops the default namespace from a canonical reference string.
func shortRef(s string) string {
	if ref, err := catalog.ParseRef(s, "", catalog.DefaultNamespace); err == nil {
		return ref.Short()
	}
	return s
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func mdDash(s string) string {
	if s == "" {
		return "_unowned_"
	}
	return "`" + s + "`"
}
