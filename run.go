package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"

	"spell_splitter/internal/dict"
	"spell_splitter/internal/segment"
	"spell_splitter/internal/text"
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
}

type row struct {
	in    string
	spell string
	src   string
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
		maxVariant  = fs.Int("max_variants", 20, "max rows per input line, including the as-is row")
		maxForms    = fs.Int("max_forms", 8, "max separator forms per input line")
		maxToken    = fs.Int("max_token", 8, "max candidate splits per token")
		maxNodes    = fs.Int("max_nodes", 20000, "max search steps per token")
		maxUnknown  = fs.Int("max_unknown", 12, "max runes of an unknown run inside a token")
		maxTokenLen = fs.Int("max_token_len", 64, "tokens longer than this are not split")
		col         = fs.Int("col", 1, "1-based input column holding the spell")
		workers     = fs.Int("workers", runtime.NumCPU(), "parallel workers")
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
		{"-max_token_len", *maxTokenLen}, {"-workers", *workers},
	} {
		if nv.val < 1 {
			return fmt.Errorf("run: %s must be 1 or greater", nv.name)
		}
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

	cfg := runConfig{col: *col, maxForms: *maxForms, maxVariants: *maxVariant}

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

type counters struct {
	lines   int64
	rows    int64
	skipped int64
}

func runPipeline(in io.Reader, out io.Writer, seg *segment.Segmenter, cfg runConfig, workers int) (counters, error) {
	if workers < 1 {
		workers = 1
	}

	type job struct {
		n    int64
		line string
	}
	type result struct {
		n       int64
		rows    []row
		skipped bool
	}

	jobs := make(chan job, 1024)
	results := make(chan result, 1024)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				rows, skipped := processLine(j.line, seg, cfg)
				results <- result{n: j.n, rows: rows, skipped: skipped}
			}
		}()
	}

	w := bufio.NewWriterSize(out, 1<<16)
	done := make(chan counters)
	writeErr := make(chan error, 1)
	go func() {
		var c counters
		var err error
		pending := make(map[int64]result)
		next := int64(0)
		flush := func() {
			if err != nil {
				return
			}
			if ferr := w.Flush(); ferr != nil {
				err = ferr
			}
		}
		for res := range results {
			pending[res.n] = res
			for {
				r, ok := pending[next]
				if !ok {
					break
				}
				delete(pending, next)
				next++
				c.lines++
				if r.skipped {
					c.skipped++
				}
				for _, row := range r.rows {
					if _, ferr := fmt.Fprintf(w, "%s\t%s\t%s\n", row.in, row.spell, row.src); ferr != nil {
						err = ferr
						break
					}
					c.rows++
				}
				if err != nil {
					break
				}
				if c.lines%100000 == 0 {
					fmt.Fprintf(os.Stderr, "spell_splitter: %d lines\n", c.lines)
					flush()
				}
			}
			if err != nil {
				break
			}
		}
		flush()
		done <- c
		writeErr <- err
	}()

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), dict.ScanBuffer)
	var n int64
	for sc.Scan() {
		n++
		jobs <- job{n: n - 1, line: sc.Text()}
	}
	scanErr := sc.Err()
	close(jobs)
	wg.Wait()
	close(results)
	c := <-done
	if err := <-writeErr; err != nil {
		return c, fmt.Errorf("writing output: %w", err)
	}
	if scanErr != nil {
		return c, fmt.Errorf("reading input: %w", scanErr)
	}
	return c, nil
}

// processLine turns one input line into its spell rows. The boolean reports
// whether the line was skipped because it had no usable column.
func processLine(raw string, seg *segment.Segmenter, cfg runConfig) ([]row, bool) {
	fields := strings.Split(raw, "\t")
	if cfg.col > len(fields) {
		return nil, true
	}
	key := strings.TrimSpace(fields[cfg.col-1])
	if key == "" {
		return nil, true
	}

	rows := []row{{in: key, spell: key, src: srcAsIs}}
	forms := segment.Forms(key, cfg.maxForms)
	if len(forms) == 0 {
		// A glued token is its own form.
		forms = []string{key}
	}

	var pool []segment.Candidate
	for _, form := range forms {
		tokens := strings.Fields(text.Fold(form))
		if len(tokens) == 0 {
			continue
		}
		sets := make([][]segment.Candidate, 0, len(tokens))
		for _, tok := range tokens {
			sets = append(sets, seg.Segment(tok))
		}
		pool = append(pool, seg.Combine(sets, cfg.maxVariants)...)
	}

	sort.SliceStable(pool, func(i, j int) bool { return pool[i].Score > pool[j].Score })
	seen := map[string]bool{key: true}
	for _, cand := range pool {
		spell := cand.String()
		if seen[spell] {
			continue
		}
		seen[spell] = true
		rows = append(rows, row{in: key, spell: spell, src: sourceOf(cand, seg)})
		if len(rows) >= cfg.maxVariants {
			break
		}
	}
	return rows, false
}

// sourceOf labels how a candidate was produced.
func sourceOf(cand segment.Candidate, seg *segment.Segmenter) string {
	if !cand.Split {
		return srcSep
	}
	for _, w := range cand.Words {
		if _, ok := seg.Uni.Lookup(w); !ok {
			return srcPartial
		}
	}
	return srcSplit
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
