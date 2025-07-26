// Package facts gathers repository facts about catalog entities (CI, README,
// on-call link, ownership, dependency freshness, ...) for scorecards to
// evaluate. Facts are collected from the directory holding the entity's
// catalog-info.yaml, entirely offline.
package facts

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/guneshm03/paved/internal/catalog"
)

// Facts maps a dotted fact name to its value: bool, float64, string or []string.
type Facts map[string]any

// Keys returns fact names sorted.
func (f Facts) Keys() []string {
	ks := make([]string, 0, len(f))
	for k := range f {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Annotation keys paved understands.
const (
	AnnotationOnCall       = "paved.dev/oncall-url"
	AnnotationPagerDuty    = "pagerduty.com/service-id"
	AnnotationPagerDutyKey = "pagerduty.com/integration-key"
	AnnotationTechDocs     = "backstage.io/techdocs-ref"
	linkTypeOnCall         = "oncall"
	readmeName             = "README.md"
	githubWorkflowsDir     = ".github/workflows"
	gitlabCIFile           = ".gitlab-ci.yml"
	jenkinsFile            = "Jenkinsfile"
)

// Collector gathers facts for entities of one catalog.
type Collector struct {
	// Root is the on-disk catalog root that descriptor paths are relative to.
	Root    string
	Catalog *catalog.Catalog
	// Registry enables dependency freshness facts; nil leaves deps.* limited
	// to counts.
	Registry *Registry
}

// Collect gathers all facts for e.
func (c *Collector) Collect(e *catalog.Entity) (Facts, error) {
	dir := filepath.Join(c.Root, filepath.FromSlash(e.Source.Dir()))
	f := Facts{}
	c.entityFacts(e, f)
	if err := c.ownerFacts(e, f); err != nil {
		return nil, err
	}
	if err := ciFacts(dir, f); err != nil {
		return nil, err
	}
	if err := readmeFacts(dir, f); err != nil {
		return nil, err
	}
	onCallFacts(e, f)
	f["techdocs.present"] = e.Annotation(AnnotationTechDocs) != ""
	f["codeowners.present"] = anyExists(dir, "CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS")
	if err := c.depFacts(dir, f); err != nil {
		return nil, err
	}
	return f, nil
}

func (c *Collector) entityFacts(e *catalog.Entity, f Facts) {
	ref := e.Ref()
	tags := append([]string{}, e.Metadata.Tags...)
	f["entity.kind"] = string(e.Kind)
	f["entity.type"] = e.Spec.Type
	f["entity.lifecycle"] = e.Spec.Lifecycle
	f["entity.tags"] = tags
	f["entity.hasDescription"] = strings.TrimSpace(e.Metadata.Description) != ""
	g := c.Catalog.Graph()
	f["system.present"] = len(g.Targets(ref, catalog.RelPartOf)) > 0
	f["api.provides"] = float64(len(g.Targets(ref, catalog.RelProvidesAPI)))
	f["api.consumes"] = float64(len(g.Targets(ref, catalog.RelConsumesAPI)))
}

func (c *Collector) ownerFacts(e *catalog.Entity, f Facts) error {
	f["owner.declared"] = strings.TrimSpace(e.Spec.Owner) != ""
	o, err := c.Catalog.OwnerOf(e.Ref())
	if err != nil {
		return err
	}
	f["owner.resolved"] = o.Direct != nil
	f["owner.effective"] = ""
	f["owner.via"] = o.Via
	if o.Effective != nil {
		f["owner.effective"] = o.Effective.String()
	}
	return nil
}

var testCommandRe = regexp.MustCompile(`(?m)\b(go test|npm (run )?test|pytest|cargo test|make (test|check)|yarn test|pnpm test)\b`)

func ciFacts(dir string, f Facts) error {
	provider := "none"
	workflows := 0
	runsTests := false
	wfDir := filepath.Join(dir, filepath.FromSlash(githubWorkflowsDir))
	entries, err := os.ReadDir(wfDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read workflows: %w", err)
	}
	for _, en := range entries {
		ext := filepath.Ext(en.Name())
		if en.IsDir() || (ext != ".yml" && ext != ".yaml") {
			continue
		}
		workflows++
		provider = "github-actions"
		data, err := os.ReadFile(filepath.Join(wfDir, en.Name()))
		if err != nil {
			return fmt.Errorf("read workflow %s: %w", en.Name(), err)
		}
		runsTests = runsTests || testCommandRe.Match(data)
	}
	for _, alt := range []struct{ file, name string }{{gitlabCIFile, "gitlab-ci"}, {jenkinsFile, "jenkins"}} {
		data, err := os.ReadFile(filepath.Join(dir, alt.file))
		if err == nil {
			workflows++
			if provider == "none" {
				provider = alt.name
			}
			runsTests = runsTests || testCommandRe.Match(data)
		}
	}
	f["ci.present"] = workflows > 0
	f["ci.provider"] = provider
	f["ci.workflows"] = float64(workflows)
	f["ci.runsTests"] = runsTests
	return nil
}

func readmeFacts(dir string, f Facts) error {
	data, err := os.ReadFile(filepath.Join(dir, readmeName))
	if os.IsNotExist(err) {
		f["readme.present"] = false
		f["readme.words"] = float64(0)
		f["readme.headings"] = float64(0)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read README: %w", err)
	}
	words, headings := 0, 0
	inFence := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue // code samples do not count as documentation prose
		}
		if strings.HasPrefix(trimmed, "#") {
			headings++
		}
		words += len(strings.Fields(strings.TrimLeft(trimmed, "#")))
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("scan README: %w", err)
	}
	f["readme.present"] = true
	f["readme.words"] = float64(words)
	f["readme.headings"] = float64(headings)
	return nil
}

func onCallFacts(e *catalog.Entity, f Facts) {
	link := e.Annotation(AnnotationOnCall)
	if link == "" {
		for _, l := range e.Metadata.Links {
			if strings.EqualFold(l.Type, linkTypeOnCall) {
				link = l.URL
				break
			}
		}
	}
	f["oncall.link"] = link
	f["oncall.present"] = link != ""
	f["pagerduty.present"] = e.Annotation(AnnotationPagerDuty) != "" || e.Annotation(AnnotationPagerDutyKey) != ""
}

func (c *Collector) depFacts(dir string, f Facts) error {
	deps, ecos, err := ReadManifests(dir)
	if err != nil {
		return err
	}
	names := make([]string, len(ecos))
	for i, e := range ecos {
		names[i] = string(e)
	}
	f["deps.manifests"] = names
	f["deps.present"] = len(ecos) > 0
	f["deps.total"] = float64(len(deps))
	if c.Registry == nil || len(ecos) == 0 {
		return nil
	}
	s := c.Registry.Summarize(deps)
	f["deps.evaluated"] = float64(s.Evaluated)
	f["deps.outdated"] = float64(s.Outdated)
	f["deps.unknown"] = float64(s.Unknown)
	f["deps.unpinned"] = float64(s.Unpinned)
	f["deps.outdatedRatio"] = s.OutdatedRatio()
	f["deps.majorsBehind"] = float64(s.MajorsBehind)
	f["deps.libyears"] = s.Libyears
	return nil
}

func anyExists(dir string, rels ...string) bool {
	for _, r := range rels {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(r))); err == nil {
			return true
		}
	}
	return false
}

// Dependencies returns the per-dependency freshness report for e. It fails
// when no registry is configured.
func (c *Collector) Dependencies(e *catalog.Entity) (DependencySummary, error) {
	if c.Registry == nil {
		return DependencySummary{}, fmt.Errorf("no package registry snapshot configured")
	}
	deps, _, err := ReadManifests(filepath.Join(c.Root, filepath.FromSlash(e.Source.Dir())))
	if err != nil {
		return DependencySummary{}, err
	}
	return c.Registry.Summarize(deps), nil
}
