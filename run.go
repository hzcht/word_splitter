package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"spell_splitter/internal/dict"
	"spell_splitter/internal/segment"
)

// Spell sources reported in the third output column.
const (
	srcAsIs    = "asis"    // the line as it came in
	srcSep     = "sep"     // separators normalized, nothing split
	srcSplit   = "split"   // every word is in the dictionary
	srcPartial = "partial" // at least one word is not in the dictionary
)

type runConfig struct {
	col         int
	maxForms    int
	maxVariants int
	minGain     float64
	window      float64
	score       bool
	noAsis      bool
	rank        string
}

type row struct {
	in         string
	spell      string
	src        string
	quality    float64
	confidence float64
}

func cmdRun(args []string) (err error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		inPath      = fs.String("in", "", "input file with spells, one per line (required; \"-\" for stdin)")
		outPath     = fs.String("out", "-", "output file (default stdout)")
		dictDir     = fs.String("dicts", "dicts", "dictionary directory")
		minCount    = fs.Int("min_count", 1, "drop dictionary words rarer than this")
		minWord     = fs.Int("min_word", 1, "shortest dictionary word considered")
		maxLen      = fs.Int("max_len", 40, "longest dictionary word considered")
		maxVariant  = fs.Int("max_variants", 8, "max rows per input line, including the as-is row")
		maxForms    = fs.Int("max_forms", 8, "max separator forms per input line")
		maxToken    = fs.Int("max_token", 8, "max candidate splits per token")
		maxNodes    = fs.Int("max_nodes", 20000, "max search steps per token")
		maxUnknown  = fs.Int("max_unknown", 12, "max runes of an unknown run inside a token")
		maxTokenLen = fs.Int("max_token_len", 64, "tokens longer than this are not split")
		maxWords    = fs.Int("max_words", 6, "max words in one candidate")
		wordPenalty = fs.Float64("word_penalty", 1.5, "penalty per extra word boundary")
		shortPen    = fs.Float64("short_word_penalty", 2.0, "penalty per missing letter of a short word")
		minGain     = fs.Float64("min_gain", 0, "drop candidates below the best unsplit form by less than this")
		window      = fs.Float64("window", 6.0, "keep candidates within this score of the best candidate")
		col         = fs.Int("col", 1, "1-based input column holding the spell")
		workers     = fs.Int("workers", runtime.NumCPU(), "parallel workers")
		backoff     = fs.String("bigram_backoff", "unigram", "unseen bigram rule: unigram|legacy")
		score       = fs.Bool("score", false, "append quality and confidence columns")
		rank        = fs.String("rank", "asis", "row order: asis|quality")
		noAsis      = fs.Bool("no_asis", false, "do not emit the as-is row")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inPath == "" {
		return errors.New("run: -in is required")
	}
	for _, nv := range []struct {
		name string
		val  int
	}{
		{"-col", *col}, {"-min_word", *minWord}, {"-min_count", *minCount},
		{"-max_len", *maxLen}, {"-max_variants", *maxVariant}, {"-max_forms", *maxForms},
		{"-max_token", *maxToken}, {"-max_nodes", *maxNodes}, {"-max_unknown", *maxUnknown},
		{"-max_token_len", *maxTokenLen}, {"-max_words", *maxWords}, {"-workers", *workers},
	} {
		if nv.val < 1 {
			return fmt.Errorf("run: %s must be 1 or greater", nv.name)
		}
	}
	if *wordPenalty < 0 || *shortPen < 0 {
		return errors.New("run: -word_penalty and -short_word_penalty must not be negative")
	}
	if *window < 0 {
		return errors.New("run: -window must not be negative")
	}
	if *rank != "asis" && *rank != "quality" {
		return fmt.Errorf("run: -rank must be asis or quality, got %q", *rank)
	}
	if *backoff != "unigram" && *backoff != "legacy" {
		return fmt.Errorf("run: -bigram_backoff must be unigram or legacy, got %q", *backoff)
	}

	in, err := openIn(*inPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, closeOut, err := openOut(*outPath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := closeOut(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	uni, err := dict.LoadWords(*dictDir, *minWord, *minCount)
	if err != nil {
		return err
	}
	bi, err := dict.LoadBigrams(*dictDir, *minCount, *maxLen)
	if err != nil {
		return err
	}

	opts := segment.DefaultOptions()
	opts.MinWordLen = *minWord
	opts.MaxWordLen = *maxLen
	opts.MaxTokenLen = *maxTokenLen
	opts.MaxCandidates = *maxToken
	opts.MaxNodes = *maxNodes
	opts.MaxUnknownLen = *maxUnknown
	opts.MaxWords = *maxWords
	opts.WordPenalty = *wordPenalty
	opts.ShortWordPenalty = *shortPen
	opts.BigramBackoff = *backoff

	cfg := runConfig{
		col:         *col,
		maxForms:    *maxForms,
		maxVariants: *maxVariant,
		minGain:     *minGain,
		window:      *window,
		score:       *score,
		noAsis:      *noAsis,
		rank:        *rank,
	}

	fmt.Fprintf(os.Stderr, "spell_splitter: %d words", uni.Len())
	if bi != nil {
		fmt.Fprintf(os.Stderr, ", %d bigrams", bi.Len())
	}
	fmt.Fprintf(os.Stderr, " from %s, %d workers\n", *dictDir, *workers)

	c, err := runPipeline(in, out, segment.New(uni, bi, opts), cfg, *workers)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "spell_splitter: %d lines -> %d spells", c.lines, c.rows)
	if c.skipped > 0 {
		fmt.Fprintf(os.Stderr, " (%d lines skipped)", c.skipped)
	}
	fmt.Fprintln(os.Stderr)
	return nil
}

func openIn(path string) (io.ReadCloser, error) {
	if path == "-" {
		return io.NopCloser(os.Stdin), nil
	}
	return os.Open(path)
}

func openOut(path string) (io.Writer, func() error, error) {
	if path == "-" {
		return os.Stdout, func() error { return nil }, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}
