package main

import (
	"os"
	"path/filepath"
	"testing"

	"spell_splitter/internal/text"
)

func TestRomanizeCyrillic(t *testing.T) {
	cases := map[string]string{
		"москва":    "moskva",
		"ёлка":      "elka",
		"україна":   "ukrayina",
		"крым":      "krym",
		"улица":     "ulitsa",
		"объект":    "obekt",
		"привет123": "privet123",
	}
	for in, want := range cases {
		if got := romanizeCyrillic(in); got != want {
			t.Errorf("romanizeCyrillic(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRomanizeGreek(t *testing.T) {
	cases := map[string]string{
		"αθήνα":   "athina",
		"ουρανός": "ouranos",
		"κόσμος":  "kosmos",
		"μπάλα":   "bala",
	}
	for in, want := range cases {
		if got := romanizeGreek(in); got != want {
			t.Errorf("romanizeGreek(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizerRejectsUnknownScheme(t *testing.T) {
	if _, err := normalizer("klingon"); err == nil {
		t.Fatal("normalizer(\"klingon\") = nil error, want error")
	}
}

func TestBuildUnigramsMergeAndScale(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "words.txt")
	if err := os.WriteFile(src, []byte("the 100\nto 50\nthe 25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.txt")

	n, total, err := buildUnigrams([]string{src}, nil, text.Fold, 1, 40, 1, 0, out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || total != 175 {
		t.Fatalf("got n=%d total=%d, want n=2 total=175", n, total)
	}
	assertFile(t, out, "the\t125\nto\t50\n")

	n, total, err = buildUnigrams([]string{src}, nil, text.Fold, 1, 40, 1, 350, out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || total != 350 {
		t.Fatalf("scaled: got n=%d total=%d, want n=2 total=350", n, total)
	}
	assertFile(t, out, "the\t250\nto\t100\n")
}

func TestBuildBigrams(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "corpus.txt")
	if err := os.WriteFile(corpus, []byte("the quick brown fox\nquick fox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.bigrams")

	n, err := buildBigrams(corpus, text.Fold, 40, 1, out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("got n=%d, want 4", n)
	}
	assertFile(t, out, "brown\tfox\t1\nquick\tbrown\t1\nquick\tfox\t1\nthe\tquick\t1\n")
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s =\n%q\nwant\n%q", path, got, want)
	}
}
