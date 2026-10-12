package segment

import (
	"os"
	"path/filepath"
	"testing"

	"spell_splitter/internal/dict"
)

const testWords = `the 1000000
of 900000
a 800000
is 500000
you 400000
bis 300000
or 300000
new 200000
york 150000
your 100000
card 90000
credit 80000
apr 70000
glass 50000
ku 5000
ra 6000
`

const testBigrams = "new\tyork\t100000\ncredit\tcard\t50000\nor\tglass\t1000\n"

func testSegmenter(t *testing.T, opts Options) *Segmenter {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en.txt"), []byte(testWords), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "en.bigrams"), []byte(testBigrams), 0o644); err != nil {
		t.Fatal(err)
	}
	uni, err := dict.LoadWords(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := dict.LoadBigrams(dir, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	return New(uni, bi, opts)
}

func hasCandidate(cands []Candidate, want string) bool {
	for _, c := range cands {
		if c.String() == want {
			return true
		}
	}
	return false
}

func TestSegmentBigramRanksFirst(t *testing.T) {
	seg := testSegmenter(t, Options{})
	got := seg.Segment("newyork")
	if len(got) == 0 {
		t.Fatal("no candidates")
	}
	if got[0].String() != "new york" {
		t.Errorf("top candidate = %q, want %q (all: %v)", got[0].String(), "new york", candStrings(got))
	}
}

func TestSegmentKnownSplit(t *testing.T) {
	seg := testSegmenter(t, Options{})
	got := seg.Segment("yourbis")
	if !hasCandidate(got, "your bis") {
		t.Errorf("missing %q in %v", "your bis", candStrings(got))
	}
}

func TestSegmentPartialUnknownRun(t *testing.T) {
	seg := testSegmenter(t, Options{})
	got := seg.Segment("zumglass")
	if !hasCandidate(got, "zum glass") {
		t.Errorf("missing partial split %q in %v", "zum glass", candStrings(got))
	}
}

func TestSegmentKeepsWholeToken(t *testing.T) {
	seg := testSegmenter(t, Options{})
	got := seg.Segment("the")
	if len(got) != 1 {
		t.Fatalf("expected one candidate for a known word, got %v", candStrings(got))
	}
	if got[0].Split {
		t.Error("a whole dictionary word must not be marked as split")
	}
}

func TestSegmentUnknownOnly(t *testing.T) {
	seg := testSegmenter(t, Options{})
	got := seg.Segment("qxzwv")
	if len(got) != 1 || got[0].String() != "qxzwv" {
		t.Errorf("expected only the whole token, got %v", candStrings(got))
	}
}

func TestSegmentMaxTokenLen(t *testing.T) {
	seg := testSegmenter(t, Options{MaxTokenLen: 5})
	got := seg.Segment("newyork") // 7 runes > 5
	if len(got) != 1 || got[0].String() != "newyork" {
		t.Errorf("token longer than MaxTokenLen must be kept whole, got %v", candStrings(got))
	}
}

func TestSegmentNonSplittable(t *testing.T) {
	seg := testSegmenter(t, Options{})
	got := seg.Segment("don't")
	if len(got) != 1 || got[0].String() != "don't" {
		t.Errorf("non-splittable token must only be folded and kept, got %v", candStrings(got))
	}
}

func TestCombineBigramAcrossTokens(t *testing.T) {
	seg := testSegmenter(t, Options{})
	sets := [][]Candidate{seg.Segment("new"), seg.Segment("york")}
	got := seg.Combine(sets, 3)
	if len(got) == 0 {
		t.Fatal("no combinations")
	}
	if got[0].String() != "new york" {
		t.Errorf("top combination = %q, want %q", got[0].String(), "new york")
	}
}

func TestCombineCapAndEmpty(t *testing.T) {
	seg := testSegmenter(t, Options{})
	sets := [][]Candidate{seg.Segment("new"), seg.Segment("york")}
	if got := seg.Combine(sets, 1); len(got) != 1 {
		t.Errorf("k=1 returned %d candidates", len(got))
	}
	if got := seg.Combine([][]Candidate{{}, seg.Segment("york")}, 3); got != nil {
		t.Errorf("a set with no candidates must yield nil, got %v", got)
	}
	if got := seg.Combine(nil, 3); got != nil {
		t.Errorf("no sets must yield nil, got %v", got)
	}
}

func TestSepRunsAndForms(t *testing.T) {
	runs := SepRuns("0-apr-credit-card")
	if len(runs) != 3 {
		t.Fatalf("SepRuns returned %d runs, want 3", len(runs))
	}

	forms := Forms("0-apr-credit-card", 8)
	if len(forms) == 0 {
		t.Fatal("no forms")
	}
	if forms[0] != "0 apr credit card" {
		t.Errorf("first form = %q, want the fully exploded one", forms[0])
	}
	if !contains(forms, "0aprcreditcard") {
		t.Errorf("missing glued form in %v", forms)
	}
	if got := Forms("nospace", 8); got != nil {
		t.Errorf("a spell without separators must yield no forms, got %v", got)
	}
	if got := Forms("a-b", 0); got != nil {
		t.Errorf("maxForms=0 must yield no forms, got %v", got)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func candStrings(cands []Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.String()
	}
	return out
}
