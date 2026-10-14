// Command fetchdicts downloads public word-frequency lists, transliterates
// them to ASCII when needed and writes spell_splitter dictionaries:
// a "word<TAB>count" unigram file and, optionally from a corpus, a
// "w1<TAB>w2<TAB>count" bigram file.
//
// The output belongs in the dictionary directory ("dicts/" by default), whose
// loader merges and sums every *.txt and *.bigrams file it finds there.
//
// Example:
//
//	go run ./tools/fetchdicts -lang ru -romanize cyrillic \
//	    -url https://raw.githubusercontent.com/hermitdave/FrequencyWords/master/content/2018/ru/ru_50k.txt \
//	    -scale_total 12000000 -out dicts/ru.txt
//
// See SOURCES.md for downloads, formats and licenses.
package main

import (
	"bufio"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"spell_splitter/internal/dict"
	"spell_splitter/internal/text"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "fetchdicts: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("fetchdicts", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		lang     = fs.String("lang", "", "language code, e.g. ru (required)")
		romanize = fs.String("romanize", "none", "transliterate non-Latin sources: none|cyrillic|greek")
		minWord  = fs.Int("min_word", 1, "drop words shorter than this")
		maxLen   = fs.Int("max_len", 40, "drop words longer than this")
		minCount = fs.Int("min_count", 1, "drop words whose merged count is below this")
		scale    = fs.Int64("scale_total", 0, "if > 0, rescale counts so their sum equals this")
		out      = fs.String("out", "", "unigram output file (default dicts/<lang>.txt)")

		corpus    = fs.String("corpus", "", "optional corpus text, one sentence per line, to build bigrams")
		bigramOut = fs.String("bigram_out", "", "bigram output file (default dicts/<lang>.bigrams)")
		minBigram = fs.Int("min_bigram_count", 3, "drop pairs whose count is below this")
	)
	srcs := stringList{}
	fs.Var(&srcs, "in", "local source file, may be .gz (repeatable)")
	urls := stringList{}
	fs.Var(&urls, "url", "source URL, may be .gz (repeatable)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: fetchdicts -lang <code> [-in FILE | -url URL ...] [-corpus FILE] [flags]

outputs ASCII spell_splitter dictionaries from public word-frequency data:
  -lang         language code used in the default output names (required)
  -in           local "word [count]" source (repeatable)
  -url          "word [count]" source URL (repeatable)
  -romanize     none (Latin, default), cyrillic or greek
  -scale_total  rescale counts so the emitted total equals this value
  -corpus       plain text corpus (one sentence per line) for bigrams
  -out          unigram file (default dicts/<lang>.txt)
  -bigram_out   bigram file (default dicts/<lang>.bigrams)

examples:
  fetchdicts -lang ru -romanize cyrillic -url <ru_50k.txt URL> -scale_total 12000000
  fetchdicts -lang ru -in ru_50k.txt -corpus ru.opensubtitles.txt
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *lang == "" {
		fs.Usage()
		return errors.New("-lang is required")
	}
	if len(srcs) == 0 && len(urls) == 0 && *corpus == "" {
		fs.Usage()
		return errors.New("provide at least one -in, -url or -corpus")
	}

	normalize, err := normalizer(*romanize)
	if err != nil {
		return err
	}

	if len(srcs) > 0 || len(urls) > 0 {
		outPath := *out
		if outPath == "" {
			outPath = filepath.Join("dicts", *lang+".txt")
		}
		n, total, err := buildUnigrams(srcs, urls, normalize, *minWord, *maxLen, *minCount, *scale, outPath)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "fetchdicts: %s: %d words, total %d\n", outPath, n, total)
	}

	if *corpus != "" {
		outPath := *bigramOut
		if outPath == "" {
			outPath = filepath.Join("dicts", *lang+".bigrams")
		}
		n, err := buildBigrams(*corpus, normalize, *maxLen, *minBigram, outPath)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "fetchdicts: %s: %d pairs\n", outPath, n)
	}

	fmt.Fprintln(os.Stderr, "fetchdicts: attribution required for the source data; see SOURCES.md")
	return nil
}

// buildUnigrams reads every source, normalizes each "word [count]" line,
// merges and filters the counts and writes them sorted to outPath.
func buildUnigrams(srcs, urls []string, normalize func(string) string, minWord, maxLen, minCount int, scaleTotal int64, outPath string) (int, int64, error) {
	opens := make([]func() (io.ReadCloser, error), 0, len(srcs)+len(urls))
	for _, p := range srcs {
		p := p
		opens = append(opens, func() (io.ReadCloser, error) { return openFile(p) })
	}
	for _, u := range urls {
		u := u
		opens = append(opens, func() (io.ReadCloser, error) { return openURL(u) })
	}

	counts := make(map[string]int64)
	for i, open := range opens {
		rc, err := open()
		if err != nil {
			return 0, 0, err
		}
		err = scanLines(rc, func(line string) {
			word, count := dict.ParseWordCount(line)
			if word == "" {
				return
			}
			word = normalize(word)
			if len(word) < minWord || len(word) > maxLen || !text.IsSplittable(word) {
				return
			}
			counts[word] += int64(count)
		})
		rc.Close()
		if err != nil {
			return 0, 0, err
		}
		fmt.Fprintf(os.Stderr, "fetchdicts: read source %d/%d\n", i+1, len(opens))
	}

	words := make([]string, 0, len(counts))
	var total int64
	for w, c := range counts {
		if c < int64(minCount) {
			continue
		}
		words = append(words, w)
		total += c
	}
	if len(words) == 0 {
		return 0, 0, errors.New("no words survived the filters; check -min_word, -max_len and -min_count")
	}
	if scaleTotal > 0 && total > 0 && scaleTotal != total {
		factor := float64(scaleTotal) / float64(total)
		total = 0
		for _, w := range words {
			c := int64(math.Round(float64(counts[w]) * factor))
			if c < 1 {
				c = 1
			}
			counts[w] = c
			total += c
		}
	}
	sort.Strings(words)
	err := writeAtomic(outPath, func(bw *bufio.Writer) {
		for _, word := range words {
			fmt.Fprintf(bw, "%s\t%d\n", word, counts[word])
		}
	})
	return len(words), total, err
}

// buildBigrams tokenizes every corpus line, normalizes the tokens and counts
// adjacent pairs, then writes the pairs sorted to outPath.
func buildBigrams(corpusPath string, normalize func(string) string, maxLen, minCount int, outPath string) (int, error) {
	f, err := os.Open(corpusPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	pairs := make(map[[2]string]int64)
	err = scanLines(f, func(line string) {
		words := normalizeTokens(line, normalize, maxLen)
		for i := 1; i < len(words); i++ {
			key := [2]string{words[i-1], words[i]}
			pairs[key]++
		}
	})
	if err != nil {
		return 0, err
	}

	keys := make([][2]string, 0, len(pairs))
	for k, c := range pairs {
		if c < int64(minCount) {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return 0, errors.New("no pairs survived the filters; check -corpus and -min_bigram_count")
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	err = writeAtomic(outPath, func(bw *bufio.Writer) {
		for _, k := range keys {
			fmt.Fprintf(bw, "%s\t%s\t%d\n", k[0], k[1], pairs[k])
		}
	})
	return len(keys), err
}

// normalizeTokens splits a line into word tokens and returns only those that
// normalize to a splittable ASCII word.
func normalizeTokens(line string, normalize func(string) string, maxLen int) []string {
	var out []string
	start := -1
	runes := []rune(line)
	for i, r := range runes {
		if text.IsWordRune(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			if w := keepToken(string(runes[start:i]), normalize, maxLen); w != "" {
				out = append(out, w)
			}
			start = -1
		}
	}
	if start >= 0 {
		if w := keepToken(string(runes[start:]), normalize, maxLen); w != "" {
			out = append(out, w)
		}
	}
	return out
}

func keepToken(tok string, normalize func(string) string, maxLen int) string {
	w := normalize(tok)
	if w == "" || len(w) > maxLen || !text.IsSplittable(w) {
		return ""
	}
	return w
}

// normalizer returns the per-word normalizer for a transliteration scheme.
func normalizer(scheme string) (func(string) string, error) {
	switch scheme {
	case "none", "":
		return text.Fold, nil
	case "cyrillic":
		return func(s string) string { return text.Fold(romanizeCyrillic(s)) }, nil
	case "greek":
		return func(s string) string { return text.Fold(romanizeGreek(s)) }, nil
	default:
		return nil, fmt.Errorf("unknown -romanize scheme %q (want none, cyrillic or greek)", scheme)
	}
}

// romanizeCyrillic transliterates Cyrillic letters to Latin using a simplified
// BGN/PCGN-style mapping (the one most domain names use). Runes without a
// mapping are left as they are, so they are dropped later as non-splittable.
func romanizeCyrillic(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if to, ok := cyrillic[r]; ok {
			b.WriteString(to)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var cyrillic = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
	// Ukrainian, Belarusian, Serbian and other Cyrillic letters.
	'і': "i", 'ї': "yi", 'є': "ye", 'ґ': "g", 'ў': "u", 'ђ': "dj", 'ј': "j",
	'љ': "lj", 'њ': "nj", 'ћ': "c", 'џ': "dz", 'ѕ': "dz", 'ѓ': "g", 'ќ': "k",
	'ѐ': "e", 'ѝ': "i",
}

// romanizeGreek transliterates Greek to Latin. Digraphs are handled first,
// then single letters; it is approximate (transliteration is ambiguous) and
// intended for keyword matching, not for faithful spelling.
func romanizeGreek(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer(
		"ά", "α", "έ", "ε", "ή", "η", "ί", "ι", "ό", "ο", "ύ", "υ", "ώ", "ω",
		"ϊ", "ι", "ϋ", "υ", "ΐ", "ι", "ΰ", "υ",
	).Replace(s)
	s = strings.NewReplacer(
		"ου", "ou", "αι", "ai", "ει", "ei", "οι", "oi", "υι", "yi",
		"αυ", "av", "ευ", "ev", "ηυ", "iv", "γγ", "ng", "γκ", "g",
		"γξ", "nx", "γχ", "nch", "μπ", "b", "ντ", "d", "τσ", "ts", "τζ", "tz",
	).Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if to, ok := greek[r]; ok {
			b.WriteString(to)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var greek = map[rune]string{
	'α': "a", 'β': "v", 'γ': "g", 'δ': "d", 'ε': "e", 'ζ': "z", 'η': "i",
	'θ': "th", 'ι': "i", 'κ': "k", 'λ': "l", 'μ': "m", 'ν': "n", 'ξ': "x",
	'ο': "o", 'π': "p", 'ρ': "r", 'σ': "s", 'ς': "s", 'τ': "t", 'υ': "y",
	'φ': "f", 'χ': "ch", 'ψ': "ps", 'ω': "o", 'ϐ': "b",
}

func scanLines(r io.Reader, fn func(string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), dict.ScanBuffer)
	for sc.Scan() {
		fn(sc.Text())
	}
	return sc.Err()
}

func openFile(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &gzReadCloser{gz: gz, under: f}, nil
	}
	return f, nil
}

func openURL(u string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "spell_splitter-fetchdicts")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	if strings.HasSuffix(strings.ToLower(u), ".gz") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			resp.Body.Close()
			return nil, err
		}
		return &gzReadCloser{gz: gz, under: resp.Body}, nil
	}
	return resp.Body, nil
}

type gzReadCloser struct {
	gz    *gzip.Reader
	under io.Closer
}

func (g *gzReadCloser) Read(p []byte) (int, error) { return g.gz.Read(p) }

func (g *gzReadCloser) Close() error {
	g.gz.Close()
	return g.under.Close()
}

// writeAtomic writes through a temporary file that is renamed into place only
// on success, so a failed run never leaves a clobbered dictionary.
func writeAtomic(path string, write func(*bufio.Writer)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	write(bw)
	if err := bw.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
