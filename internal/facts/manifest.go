package facts

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Ecosystem identifies a package registry.
type Ecosystem string

// Supported ecosystems.
const (
	EcosystemGo   Ecosystem = "go"
	EcosystemNPM  Ecosystem = "npm"
	EcosystemPyPI Ecosystem = "pypi"
)

// Dependency is one declared dependency.
type Dependency struct {
	Ecosystem Ecosystem `json:"ecosystem"`
	Name      string    `json:"name"`
	// Version is the pinned or range-base version ("" when unpinned).
	Version string `json:"version"`
	Dev     bool   `json:"dev,omitempty"`
}

// manifestParsers maps a manifest file name to its parser.
var manifestParsers = []struct {
	file  string
	eco   Ecosystem
	parse func([]byte) ([]Dependency, error)
}{
	{"go.mod", EcosystemGo, parseGoMod},
	{"package.json", EcosystemNPM, parsePackageJSON},
	{"requirements.txt", EcosystemPyPI, parseRequirements},
}

// ReadManifests parses every supported manifest in dir (non-recursive).
// It returns the dependencies sorted by ecosystem then name, and the
// ecosystems found.
func ReadManifests(dir string) ([]Dependency, []Ecosystem, error) {
	var (
		deps []Dependency
		ecos []Ecosystem
	)
	for _, m := range manifestParsers {
		data, err := os.ReadFile(filepath.Join(dir, m.file))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", m.file, err)
		}
		ds, err := m.parse(data)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", m.file, err)
		}
		deps = append(deps, ds...)
		ecos = append(ecos, m.eco)
	}
	sort.SliceStable(deps, func(i, j int) bool {
		if deps[i].Ecosystem != deps[j].Ecosystem {
			return deps[i].Ecosystem < deps[j].Ecosystem
		}
		return deps[i].Name < deps[j].Name
	})
	return deps, ecos, nil
}

// parseGoMod reads require directives (single-line and block form).
func parseGoMod(data []byte) ([]Dependency, error) {
	var deps []Dependency
	inBlock := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		indirect := strings.Contains(line, "// indirect")
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		switch {
		case line == "":
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "require ("):
			inBlock = true
			continue
		case strings.HasPrefix(line, "require "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
		case !inBlock:
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("line %d: malformed require %q", lineNo, line)
		}
		deps = append(deps, Dependency{Ecosystem: EcosystemGo, Name: f[0], Version: f[1], Dev: indirect})
	}
	return deps, sc.Err()
}

func parsePackageJSON(data []byte) ([]Dependency, error) {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}
	var deps []Dependency
	for name, v := range pkg.Dependencies {
		deps = append(deps, Dependency{Ecosystem: EcosystemNPM, Name: name, Version: v})
	}
	for name, v := range pkg.DevDependencies {
		deps = append(deps, Dependency{Ecosystem: EcosystemNPM, Name: name, Version: v, Dev: true})
	}
	return deps, nil
}

var reqLineRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)(\[[^\]]*\])?\s*(?:(==|>=|~=|<=|>|<)\s*([^\s;,]+))?`)

func parseRequirements(data []byte) ([]Dependency, error) {
	var deps []Dependency
	sc := bufio.NewScanner(bytes.NewReader(data))
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" || strings.HasPrefix(line, "-") {
			continue // blank, comment, or pip option such as -r / --index-url
		}
		m := reqLineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d: cannot parse requirement %q", lineNo, line)
		}
		d := Dependency{Ecosystem: EcosystemPyPI, Name: strings.ToLower(m[1])}
		if m[3] != "" {
			d.Version = m[3] + m[4]
		}
		deps = append(deps, d)
	}
	return deps, sc.Err()
}
