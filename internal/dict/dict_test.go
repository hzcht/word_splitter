package dict

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseWordCount(t *testing.T) {
	cases := []struct {
		in    string
		word  string
		count int
	}{
		{"the 23135851162", "the", 23135851162},
		{"12 word", "word", 12},
		{"word", "word", 1},
		{"word\t42", "word", 42},
		{"the 12 extra", "the", 12},
		{"1\t70533", "1", 70533},
		{"000000056\t1", "000000056", 1},
		{"12\t345", "12", 345},
		{"", "", 0},
		{"   ", "", 0},
	}
	for _, c := range cases {
		word, count := ParseWordCount(c.in)
		if word != c.word || count != c.count {
			t.Errorf("ParseWordCount(%q) = (%q,%d), want (%q,%d)", c.in, word, count, c.word, c.count)
		}
	}
}

func TestParseBigram(t *testing.T) {
	if a, b, n, ok := ParseBigram("new york 100"); !ok || a != "new" || b != "york" || n != 100 {
		t.Errorf("count-last: got (%q,%q,%d,%v)", a, b, n, ok)
	}
	if a, b, n, ok := ParseBigram("100 new york"); !ok || a != "new" || b != "york" || n != 100 {
		t.Errorf("count-first: got (%q,%q,%d,%v)", a, b, n, ok)
	}
	if a, b, n, ok := ParseBigram("new york"); !ok || a != "new" || b != "york" || n != 1 {
		t.Errorf("no-count: got (%q,%q,%d,%v)", a, b, n, ok)
	}
	if _, _, _, ok := ParseBigram("only-one"); ok {
		t.Error("expected failure for a single field")
	}
	if _, _, _, ok := ParseBigram("a b c d"); ok {
		t.Error("expected failure for four fields")
	}
}

func TestLoadWordsMergesBeforeFiltering(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "rare\t2\ncommon\t100\n")
	write(t, filepath.Join(dir, "b.txt"), "rare\t2\n")

	// rare sums to 4 across both files, so it survives minCount=3.
	w, err := LoadWords(dir, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.Lookup("rare"); !ok {
		t.Error("rare should survive the merge (2+2 >= 3)")
	}
	if _, ok := w.Lookup("common"); !ok {
		t.Error("common should survive")
	}
	if w.Total() != 104 {
		t.Errorf("Total() = %d, want 104", w.Total())
	}
}

func TestLoadWordsRejectsNonLatinAndShort(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "the\t10\nпривет\t5\nab\t3\na-b\t2\nx\ttoolong\n")
	w, err := LoadWords(dir, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"привет", "ab", "a-b", "x"} {
		if _, ok := w.Lookup(bad); ok {
			t.Errorf("%q should have been filtered out", bad)
		}
	}
	if _, ok := w.Lookup("the"); !ok {
		t.Error("the should be present")
	}
}

func TestLoadBigrams(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "words.txt"), "new\t10\nyork\t10\n")
	write(t, filepath.Join(dir, "b.bigrams"), "new\tyork\t100\nnew\tyork\t50\nrare\tpair\t2\n")

	b, err := LoadBigrams(dir, 100, 40)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil {
		t.Fatal("bigrams should have been loaded")
	}
	lp := b.Cond("new", "york", func(string) float64 { return 0.5 })
	if lp == 0 {
		t.Error("expected a finite conditional probability")
	}
}

func TestLoadBigramsMissingReturnsNil(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "words.txt"), "new\t10\n")
	b, err := LoadBigrams(dir, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	if b != nil {
		t.Error("expected nil bigrams when no *.bigrams file exists")
	}
}

func TestCondNilReceiver(t *testing.T) {
	var b *Bigrams
	if got := b.Cond("a", "b", func(string) float64 { return 0.25 }); got == 0 {
		t.Error("nil bigram should fall back to the unigram probability")
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
