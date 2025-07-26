package facts

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/guneshm03/paved/internal/version"
	"go.yaml.in/yaml/v3"
)

// Release is one published version of a package.
type Release struct {
	Version  string `yaml:"version" json:"version"`
	Released string `yaml:"released" json:"released"`

	parsed version.Version
	date   time.Time
}

// Registry is an offline snapshot of package release histories, used to judge
// dependency freshness without network access.
type Registry struct {
	// Snapshot is the date the registry data represents.
	Snapshot time.Time
	packages map[Ecosystem]map[string][]Release // releases sorted ascending
}

type registryDoc struct {
	Snapshot string                          `yaml:"snapshot"`
	Packages map[string]map[string][]Release `yaml:"packages"`
}

// LoadRegistry reads a registry snapshot file.
func LoadRegistry(file string) (*Registry, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	return ParseRegistry(data)
}

// ParseRegistry parses registry YAML and validates every entry.
func ParseRegistry(data []byte) (*Registry, error) {
	var doc registryDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	snap, err := time.Parse(time.DateOnly, doc.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("registry snapshot date: %w", err)
	}
	r := &Registry{Snapshot: snap, packages: map[Ecosystem]map[string][]Release{}}
	for eco, pkgs := range doc.Packages {
		e := Ecosystem(eco)
		r.packages[e] = map[string][]Release{}
		for name, rels := range pkgs {
			if len(rels) == 0 {
				return nil, fmt.Errorf("registry %s/%s: no releases", eco, name)
			}
			for i := range rels {
				v, err := version.Parse(rels[i].Version)
				if err != nil {
					return nil, fmt.Errorf("registry %s/%s: %w", eco, name, err)
				}
				d, err := time.Parse(time.DateOnly, rels[i].Released)
				if err != nil {
					return nil, fmt.Errorf("registry %s/%s %s: released: %w", eco, name, rels[i].Version, err)
				}
				rels[i].parsed, rels[i].date = v, d
			}
			sort.Slice(rels, func(i, j int) bool { return version.Compare(rels[i].parsed, rels[j].parsed) < 0 })
			r.packages[e][normalizeName(e, name)] = rels
		}
	}
	return r, nil
}

// goMajorSuffix matches a Go semantic-import-versioning suffix such as "/v2".
var goMajorSuffix = regexp.MustCompile(`/v[0-9]+$`)

func normalizeName(e Ecosystem, name string) string {
	switch e {
	case EcosystemPyPI:
		// PEP 503 normalisation: case-insensitive, runs of -_. are equivalent.
		return strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(name))
	case EcosystemGo:
		// example.com/mod/v2 and example.com/mod share one release history, so
		// "majors behind" can be measured across major-version module paths.
		return goMajorSuffix.ReplaceAllString(name, "")
	}
	return name
}

// Freshness statuses.
const (
	StatusCurrent  = "current"
	StatusOutdated = "outdated"
	StatusUnknown  = "unknown"  // package not in the registry snapshot
	StatusUnpinned = "unpinned" // no version constraint declared
)

// Freshness is the evaluation of one dependency against the registry.
type Freshness struct {
	Dependency
	Status       string  `json:"status"`
	Latest       string  `json:"latest,omitempty"`
	MajorsBehind int     `json:"majorsBehind"`
	Libyears     float64 `json:"libyears"`
}

// Check evaluates one dependency. Libyears is the time between the release in
// use (or the newest registry release not newer than it) and the latest
// stable release — the "libyear" metric.
func (r *Registry) Check(d Dependency) Freshness {
	f := Freshness{Dependency: d}
	if d.Version == "" {
		f.Status = StatusUnpinned
		return f
	}
	rels := r.packages[d.Ecosystem][normalizeName(d.Ecosystem, d.Name)]
	if len(rels) == 0 {
		f.Status = StatusUnknown
		return f
	}
	cur, err := version.Parse(version.StripRange(d.Version))
	if err != nil {
		f.Status = StatusUnknown
		return f
	}
	latest := latestStable(rels)
	f.Latest = latest.Version
	if version.Compare(cur, latest.parsed) >= 0 {
		f.Status = StatusCurrent
		return f
	}
	f.Status = StatusOutdated
	f.MajorsBehind = max(latest.parsed.Major()-cur.Major(), 0)
	if base, ok := floorRelease(rels, cur); ok {
		years := latest.date.Sub(base.date).Hours() / (24 * 365.25)
		f.Libyears = math.Round(max(years, 0)*100) / 100
	}
	return f
}

// latestStable returns the highest non-prerelease release, or the highest
// release when every release is a prerelease.
func latestStable(rels []Release) Release {
	for i := len(rels) - 1; i >= 0; i-- {
		if !rels[i].parsed.IsPrerelease() {
			return rels[i]
		}
	}
	return rels[len(rels)-1]
}

// floorRelease returns the newest release whose version is <= v.
func floorRelease(rels []Release, v version.Version) (Release, bool) {
	i := sort.Search(len(rels), func(i int) bool { return version.Compare(rels[i].parsed, v) > 0 })
	if i == 0 {
		return Release{}, false
	}
	return rels[i-1], true
}

// DependencySummary aggregates freshness over a component's dependencies.
type DependencySummary struct {
	Total        int         `json:"total"`
	Evaluated    int         `json:"evaluated"`
	Outdated     int         `json:"outdated"`
	Unknown      int         `json:"unknown"`
	Unpinned     int         `json:"unpinned"`
	MajorsBehind int         `json:"majorsBehind"`
	Libyears     float64     `json:"libyears"`
	Items        []Freshness `json:"items"`
}

// OutdatedRatio is outdated / evaluated (0 when nothing could be evaluated).
func (s DependencySummary) OutdatedRatio() float64 {
	if s.Evaluated == 0 {
		return 0
	}
	return math.Round(float64(s.Outdated)/float64(s.Evaluated)*1000) / 1000
}

// Summarize checks every dependency.
func (r *Registry) Summarize(deps []Dependency) DependencySummary {
	s := DependencySummary{Total: len(deps)}
	for _, d := range deps {
		f := r.Check(d)
		switch f.Status {
		case StatusUnknown:
			s.Unknown++
		case StatusUnpinned:
			s.Unpinned++
		default:
			s.Evaluated++
		}
		if f.Status == StatusOutdated {
			s.Outdated++
		}
		s.MajorsBehind += f.MajorsBehind
		s.Libyears += f.Libyears
		s.Items = append(s.Items, f)
	}
	s.Libyears = math.Round(s.Libyears*100) / 100
	return s
}
