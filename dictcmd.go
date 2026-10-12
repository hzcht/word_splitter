package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"spell_splitter/internal/dict"
	"spell_splitter/internal/text"
)

// cmdMakeDict builds a "word [count]" dictionary from one or more word lists.
func cmdMakeDict(args []string) error {
	fs := flag.NewFlagSet("makedict", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		outPath  = fs.String("out", "", "dictionary file to write (required)")
		minWord  = fs.Int("min_word", 1, "drop words shorter than this")
		minCount = fs.Int("min_count", 1, "drop words whose merged count is below this")
		maxLen   = fs.Int("max_len", 40, "drop words longer than this")
	)
	srcs := stringList{}
	fs.Var(&srcs, "src", "source word list file (repeatable)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: spell_splitter makedict -out dicts/en.txt -src words.txt [-src ...]")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outPath == "" || len(srcs) == 0 {
		fs.Usage()
		return fmt.Errorf("makedict: -out and -src are required")
	}

	counts := make(map[string]int64)
	for _, src := range srcs {
		lines, err := mergeWordCounts(src, *minWord, *maxLen, counts)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "makedict: %s: %d lines\n", src, lines)
	}

	words := make([]string, 0, len(counts))
	for w, c := range counts {
		// Filter after the merge so a word spread over several sources is
		// kept when the merged count survives.
		if c < int64(*minCount) {
			continue
		}
		words = append(words, w)
	}
	sort.Strings(words)
	if len(words) == 0 {
		return fmt.Errorf("makedict: no words left after filtering; check -src, -min_count and -max_len")
	}

	err := writeAtomic(*outPath, func(w *bufio.Writer) {
		for _, word := range words {
			fmt.Fprintf(w, "%s\t%d\n", word, counts[word])
		}
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "makedict: wrote %d words to %s\n", len(words), *outPath)
	return nil
}

// mergeWordCounts merges a "word [count]" list into counts and returns the
// number of accepted lines.
func mergeWordCounts(path string, minWord, maxLen int, counts map[string]int64) (int, error) {
	lines := 0
	err := scanFile(path, func(line string) {
		word, count := dict.ParseWordCount(line)
		if word == "" {
			return
		}
		word = text.Fold(word)
		if len(word) < minWord || len(word) > maxLen || !text.IsSplittable(word) {
			return
		}
		counts[word] += int64(count)
		lines++
	})
	return lines, err
}

// cmdMakeBigrams builds a "w1<TAB>w2<TAB>count" dictionary from word-pair
// lists in any of the "w1 w2 count", "count w1 w2" or "w1 w2" layouts.
func cmdMakeBigrams(args []string) error {
	fs := flag.NewFlagSet("makebigrams", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		outPath  = fs.String("out", "", "bigram file to write (required)")
		minCount = fs.Int("min_count", 1, "drop pairs whose merged count is below this")
		maxLen   = fs.Int("max_len", 40, "drop words longer than this")
	)
	srcs := stringList{}
	fs.Var(&srcs, "src", "source word-pair list file (repeatable)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: spell_splitter makebigrams -out dicts/en.bigrams -src pairs.txt [-src ...]")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outPath == "" || len(srcs) == 0 {
		fs.Usage()
		return fmt.Errorf("makebigrams: -out and -src are required")
	}

	counts := make(map[bigramKey]int64)
	for _, src := range srcs {
		lines, err := mergeBigramCounts(src, *maxLen, counts)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "makebigrams: %s: %d lines\n", src, lines)
	}

	keys := make([]bigramKey, 0, len(counts))
	for k, c := range counts {
		if c < int64(*minCount) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	if len(keys) == 0 {
		return fmt.Errorf("makebigrams: no pairs left after filtering; check -src, -min_count and -max_len")
	}

	err := writeAtomic(*outPath, func(w *bufio.Writer) {
		for _, k := range keys {
			fmt.Fprintf(w, "%s\t%s\t%d\n", k.a, k.b, counts[k])
		}
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "makebigrams: wrote %d pairs to %s\n", len(keys), *outPath)
	return nil
}

func mergeBigramCounts(path string, maxLen int, counts map[bigramKey]int64) (int, error) {
	lines := 0
	err := scanFile(path, func(line string) {
		w1, w2, count, ok := dict.ParseBigram(line)
		if !ok {
			return
		}
		w1, w2 = text.Fold(w1), text.Fold(w2)
		if len(w1) > maxLen || len(w2) > maxLen || !text.IsSplittable(w1) || !text.IsSplittable(w2) {
			return
		}
		counts[bigramKey{w1, w2}] += int64(count)
		lines++
	})
	return lines, err
}

// bigramKey identifies a word pair.
type bigramKey struct{ a, b string }

// scanFile scans path line by line and calls fn for every line.
func scanFile(path string, fn func(line string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), dict.ScanBuffer)
	for sc.Scan() {
		fn(sc.Text())
	}
	return sc.Err()
}

// writeAtomic writes the output through a temporary file that is renamed into
// place only on success, so a failure never leaves a clobbered dictionary.
func writeAtomic(path string, write func(w *bufio.Writer)) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()

	w := bufio.NewWriterSize(f, 1<<20)
	write(w)
	if err = w.Flush(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// stringList collects a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
