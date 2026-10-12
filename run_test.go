package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"spell_splitter/internal/dict"
	"spell_splitter/internal/segment"
)

const runTestWords = `the 1000000
your 100000
bis 300000
new 200000
york 150000
ku 5000
ra 6000
or 300000
glass 50000
`

const runTestBigrams = "new\tyork\t100000\n"

func newTestSegmenter(t *testing.T) *segment.Segmenter {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en.txt"), []byte(runTestWords), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "en.bigrams"), []byte(runTestBigrams), 0o644); err != nil {
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
	return segment.New(uni, bi, segment.DefaultOptions())
}

func defaultRunConfig() runConfig {
	return runConfig{col: 1, maxForms: 8, maxVariants: 20}
}

func spells(rows []row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.spell
	}
	return out
}

func hasSpell(rows []row, want string) bool {
	for _, r := range rows {
		if r.spell == want {
			return true
		}
	}
	return false
}

func TestProcessLineGluedTokenHasSplits(t *testing.T) {
	seg := newTestSegmenter(t)
	rows, skipped := processLine("newyork", seg, defaultRunConfig())
	if skipped {
		t.Fatal("line should not be skipped")
	}
	if len(rows) == 0 || rows[0].src != srcAsIs || rows[0].spell != "newyork" {
		t.Fatalf("first row must be the as-is spell, got %v", spells(rows))
	}
	if !hasSpell(rows, "new york") {
		t.Errorf("glued token produced no splits: %v", spells(rows))
	}
}

func TestProcessLineSeparators(t *testing.T) {
	seg := newTestSegmenter(t)
	rows, _ := processLine("ku-ra", seg, defaultRunConfig())
	if !hasSpell(rows, "kura") {
		t.Errorf("missing glued form %q in %v", "kura", spells(rows))
	}
	if !hasSpell(rows, "ku ra") {
		t.Errorf("missing exploded form %q in %v", "ku ra", spells(rows))
	}
}

func TestProcessLineColumn(t *testing.T) {
	seg := newTestSegmenter(t)
	cfg := defaultRunConfig()
	cfg.col = 2
	rows, skipped := processLine("label\tnewyork", seg, cfg)
	if skipped {
		t.Fatal("line should not be skipped")
	}
	for _, r := range rows {
		if r.in != "newyork" {
			t.Errorf("input key = %q, want %q", r.in, "newyork")
		}
	}
}

func TestProcessLineSkips(t *testing.T) {
	seg := newTestSegmenter(t)
	if _, skipped := processLine("   ", seg, defaultRunConfig()); !skipped {
		t.Error("a blank line should be skipped")
	}
	cfg := defaultRunConfig()
	cfg.col = 3
	if _, skipped := processLine("onlyone", seg, cfg); !skipped {
		t.Error("a line without the requested column should be skipped")
	}
}

func TestProcessLineMaxVariants(t *testing.T) {
	seg := newTestSegmenter(t)
	cfg := defaultRunConfig()
	cfg.maxVariants = 2
	rows, _ := processLine("yourbis", seg, cfg)
	if len(rows) > 2 {
		t.Errorf("got %d rows, want at most 2", len(rows))
	}
}

func TestProcessLineSourceLabels(t *testing.T) {
	seg := newTestSegmenter(t)
	rows, _ := processLine("newyork", seg, defaultRunConfig())
	src := map[string]string{}
	for _, r := range rows {
		src[r.spell] = r.src
	}
	if src["newyork"] != srcAsIs {
		t.Errorf("as-is row source = %q", src["newyork"])
	}
	if src["new york"] != srcSplit {
		t.Errorf("all-known split source = %q, want %q", src["new york"], srcSplit)
	}
}

func TestRunPipelineOrderAndCounts(t *testing.T) {
	seg := newTestSegmenter(t)
	var buf strings.Builder
	c, err := runPipeline(strings.NewReader("aaa\nbbb\nccc\n"), &buf, seg, defaultRunConfig(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if c.lines != 3 {
		t.Errorf("lines = %d, want 3", c.lines)
	}
	got := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	want := []string{"aaa\taaa\tasis", "bbb\tbbb\tasis", "ccc\tccc\tasis"}
	if len(got) != len(want) {
		t.Fatalf("got %d output lines %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunPipelineSkips(t *testing.T) {
	seg := newTestSegmenter(t)
	var buf strings.Builder
	cfg := defaultRunConfig()
	cfg.col = 2
	c, err := runPipeline(strings.NewReader("ok\tnewyork\nbad\n"), &buf, seg, cfg, 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.skipped != 1 {
		t.Errorf("skipped = %d, want 1", c.skipped)
	}
}

func TestRunPipelineWriterError(t *testing.T) {
	seg := newTestSegmenter(t)
	_, err := runPipeline(strings.NewReader("newyork\n"), errWriter{}, seg, defaultRunConfig(), 1)
	if err == nil {
		t.Fatal("expected the write error to propagate")
	}
}

type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("boom") }

// TestGoldenListFull runs the whole pipeline over the canonical input list
// with the default flags and checks the SHA-256 of the output. It is the
// regression gate for every change that can affect output: loaders, scoring,
// search order, heap implementations. Update the constant deliberately (and
// say why) whenever an output change is intended.
//
// The hash was produced by `go run . run -in old/list.full -out out.tsv`
// with default flags: 764501 rows.
func TestGoldenListFull(t *testing.T) {
	f, err := os.Open(filepath.Join("old", "list.full"))
	if err != nil {
		t.Skipf("old/list.full: %v", err)
	}
	defer f.Close()

	uni, err := dict.LoadWords("dicts", 1, 1)
	if err != nil {
		t.Skipf("dicts: %v", err)
	}
	bi, err := dict.LoadBigrams("dicts", 1, 40)
	if err != nil {
		t.Skipf("bigrams: %v", err)
	}
	opts := segment.DefaultOptions()
	opts.MaxWordLen = 40 // mirrors the -max_len default of the run command
	seg := segment.New(uni, bi, opts)

	h := sha256.New()
	if _, err := runPipeline(f, h, seg, defaultRunConfig(), runtime.NumCPU()); err != nil {
		t.Fatal(err)
	}
	// Re-baselined when dicts/ was extended with old/corpus.txt (unigrams)
	// and old/1.txt + old/w3.txt (bigrams).
	const want = "952e99fe2f68a9246618adcfca1253ef51fc8ad03bd18382d3d564f58e9fe5b8"
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Errorf("golden output hash mismatch:\n got  %s\n want %s", got, want)
	}
}
