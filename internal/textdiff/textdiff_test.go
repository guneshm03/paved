package textdiff

import (
	"math/rand"
	"strings"
	"testing"
)

// apply reconstructs both sides from an edit script.
func apply(ops []Op) (a, b []string) {
	for _, op := range ops {
		switch op.Kind {
		case Equal:
			a = append(a, op.Line)
			b = append(b, op.Line)
		case Delete:
			a = append(a, op.Line)
		case Insert:
			b = append(b, op.Line)
		}
	}
	return a, b
}

// lcs is the textbook O(NM) longest-common-subsequence length.
func lcs(a, b []string) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return dp[0][0]
}

func randLines(rng *rand.Rand, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = string(rune('a'+rng.Intn(4))) + "\n"
	}
	return out
}

// Property: the script reproduces both inputs and is minimal (its number of
// edits equals N+M-2·LCS).
func TestLinesIsCorrectAndMinimal(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := range 500 {
		a := randLines(rng, rng.Intn(12))
		b := randLines(rng, rng.Intn(12))
		ops := Lines(a, b)
		ga, gb := apply(ops)
		if strings.Join(ga, "") != strings.Join(a, "") || strings.Join(gb, "") != strings.Join(b, "") {
			t.Fatalf("case %d: script does not reproduce inputs\na=%q\nb=%q", i, a, b)
		}
		add, del := Stats(ops)
		if want := len(a) + len(b) - 2*lcs(a, b); add+del != want {
			t.Fatalf("case %d: %d edits, minimal is %d", i, add+del, want)
		}
	}
	if Lines(nil, nil) != nil {
		t.Fatal("empty diff should be nil")
	}
}

func TestUnified(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\n"
	newer := "a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n"
	got := Unified("a/x", "b/x", old, newer, 3)
	want := `--- a/x
+++ b/x
@@ -1,5 +1,5 @@
 a
-b
+B
 c
 d
 e
@@ -9,3 +9,4 @@
 i
 j
 k
+l
`
	if got != want {
		t.Fatalf("unified diff:\n%s\nwant:\n%s", got, want)
	}
	// Nearby changes merge into one hunk.
	merged := Unified("a", "b", "1\n2\n3\n4\n", "1\nX\n3\nY\n", 1)
	if strings.Count(merged, "@@ ") != 1 || !strings.Contains(merged, "@@ -1,4 +1,4 @@") {
		t.Fatalf("merged hunk:\n%s", merged)
	}
	if Unified("a", "b", "same\n", "same\n", 3) != "" {
		t.Fatal("equal texts should produce no diff")
	}
	created := Unified("/dev/null", "b", "", "x\ny", 3)
	if !strings.Contains(created, "@@ -0,0 +1,2 @@") || !strings.Contains(created, "\\ No newline at end of file") {
		t.Fatalf("creation diff:\n%s", created)
	}
	deleted := Unified("a", "b", "x\n", "", 3)
	if !strings.Contains(deleted, "@@ -1 +0,0 @@") {
		t.Fatalf("deletion diff:\n%s", deleted)
	}
}

func TestSplitLines(t *testing.T) {
	if SplitLines("") != nil {
		t.Fatal("empty")
	}
	if got := SplitLines("a\nb"); len(got) != 2 || got[1] != "b" {
		t.Fatalf("got %q", got)
	}
	if got := SplitLines("a\n"); len(got) != 1 {
		t.Fatalf("got %q", got)
	}
}

func BenchmarkLines1000Lines10Edits(b *testing.B) {
	a := make([]string, 1000)
	for i := range a {
		a[i] = strings.Repeat("x", i%17) + "\n"
	}
	c := append([]string{}, a...)
	for i := 0; i < 1000; i += 100 {
		c[i] = "changed\n"
	}
	for b.Loop() {
		Lines(a, c)
	}
}
