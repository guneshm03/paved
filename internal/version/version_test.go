package version

import (
	"sort"
	"testing"
)

func TestCompareOrdering(t *testing.T) {
	// Ascending semantic-version precedence.
	ordered := []string{
		"0.9", "v1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.2", "1.10.0", "2.0.0+build.5", "v2.0.1",
	}
	parsed := make([]Version, len(ordered))
	for i, s := range ordered {
		v, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		parsed[i] = v
	}
	for i := range parsed {
		for j := range parsed {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := Compare(parsed[i], parsed[j]); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
	shuffled := append([]Version{}, parsed...)
	sort.Slice(shuffled, func(i, j int) bool { return shuffled[i].Raw > shuffled[j].Raw })
	sort.Slice(shuffled, func(i, j int) bool { return Compare(shuffled[i], shuffled[j]) < 0 })
	for i := range shuffled {
		if shuffled[i].Raw != ordered[i] {
			t.Fatalf("sorted[%d] = %s, want %s", i, shuffled[i].Raw, ordered[i])
		}
	}
}

func TestParse(t *testing.T) {
	v, err := Parse("v0.0.0-20240101-abcdef")
	if err != nil || !v.IsPrerelease() || v.Major() != 0 {
		t.Fatalf("pseudo-version = %+v, %v", v, err)
	}
	if v, _ := Parse("1.2"); v.seg(2) != 0 || v.Major() != 1 {
		t.Fatalf("short version = %+v", v)
	}
	for _, bad := range []string{"", "v", "1.x", "1.0-", "-1", "1..2"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestStripRange(t *testing.T) {
	for in, want := range map[string]string{
		"^1.2.3": "1.2.3", "~1.2": "1.2", ">=2.0, <3": "2.0", "==2.31.0": "2.31.0", "1.0.0": "1.0.0",
		" ~= 3.1 ": "3.1", "<=4": "4", ">1 || <0": "1", "=5": "5",
	} {
		if got := StripRange(in); got != want {
			t.Errorf("StripRange(%q) = %q, want %q", in, got, want)
		}
	}
}
