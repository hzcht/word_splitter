// Package dict loads the unigram and bigram word models stored in a
// dictionary directory (see the makedict / makebigrams commands).
package dict

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"spell_splitter/internal/text"
)

// ScanBuffer is the largest dictionary or input line read at once.
const ScanBuffer = 1 << 20

// Words is a case-folded unigram word model.
type Words struct {
	count  map[string]wordStat
	total  int64
	maxLen int
}

// wordStat keeps a word's corpus count and its log probability in one map
// entry, so a lookup is a single hash instead of two.
type wordStat struct {
	count int64
	logp  float64
}

// LoadWords reads every "*.txt" file in dir as a "word [count]" list and
// merges them into a single vocabulary. The merge happens first: a word
// spread over several files with a small count survives minCount when its
// merged count does. The vocabulary deliberately mixes languages, so a spell
// may combine words from different files.
//
// Counts are streamed straight into the model and the minCount filter runs on
// the merged totals, so no second "raw" map is needed.
func LoadWords(dir string, minWord, minCount int) (*Words, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no *.txt dictionaries in %s", dir)
	}

	fp := fingerprint(files)
	if body, ok := readCache(dir, wordsCacheFile); ok {
		if w, ok := readWordsCache(body, minWord, minCount, fp); ok {
			return w, nil
		}
	}

	w := &Words{count: make(map[string]wordStat, sizeHint(files, 12))}
	for _, f := range files {
		if err := readFile(f, func(line string) {
			word, count := ParseWordCount(line)
			if word == "" {
				return
			}
			word = text.Fold(word)
			if len(word) < minWord || !text.IsSplittable(word) {
				return
			}
			st := w.count[word]
			st.count += int64(count)
			w.count[word] = st
		}); err != nil {
			return nil, err
		}
	}

	for word, st := range w.count {
		if st.count < int64(minCount) {
			delete(w.count, word)
			continue
		}
		w.total += st.count
		if len(word) > w.maxLen {
			w.maxLen = len(word)
		}
	}
	if w.total == 0 {
		return nil, fmt.Errorf("no words loaded from %s", dir)
	}
	total := float64(w.total + 1)
	for word, st := range w.count {
		st.logp = logProb(st.count, total)
		w.count[word] = st
	}
	saveCache(dir, wordsCacheFile, wordsCache(w, minWord, minCount, fp))
	return w, nil
}

// logProb is the smoothed log probability of a word with the given count
// over the summed corpus total. The text load path and the cache path share
// it so both produce bit-identical values.
func logProb(count int64, total float64) float64 {
	return math.Log(float64(count+1) / total)
}

// Lookup returns the corpus count of word.
func (w *Words) Lookup(word string) (int64, bool) {
	st, ok := w.count[word]
	return st.count, ok
}

// LogProb returns the smoothed log probability of word.
func (w *Words) LogProb(word string) (float64, bool) {
	st, ok := w.count[word]
	return st.logp, ok
}

// MaxLen is the length of the longest known word.
func (w *Words) MaxLen() int { return w.maxLen }

// Len is the number of distinct words.
func (w *Words) Len() int { return len(w.count) }

// Total is the sum of all corpus counts.
func (w *Words) Total() int { return int(w.total) }

// pairRec is a bigram as it is read, before duplicates are merged.
type pairRec struct {
	w1, w2 string
	c      int64
}

// bpair is one (right word, count) entry inside a group.
type bpair struct {
	w2 string
	c  int64
}

// Bigrams is a case-folded bigram word model.
//
// Pairs are kept in one flat slice, grouped by left word and sorted by right
// word inside each group; a small map points from the left word to its group.
// Only one map is built (one entry per distinct left word, ~50k for the
// shipped model) instead of a map per left word, which makes loading several
// times cheaper while Cond stays one hash lookup plus a short binary search.
type Bigrams struct {
	index map[string]int32 // left word -> group id
	start []int32          // first pair of group g
	end   []int32          // one past the last pair of group g
	total []int64          // summed count of the left word
	pairs []bpair          // all groups concatenated
}

// LoadBigrams reads every "*.bigrams" file in dir as a
// "w1<TAB>w2<TAB>count" list. It returns nil when the directory holds no
// bigram file or when nothing survives minCount.
func LoadBigrams(dir string, minCount int, maxLen int) (*Bigrams, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.bigrams"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	sort.Strings(files)

	fp := fingerprint(files)
	if body, ok := readCache(dir, bigramsCacheFile); ok {
		if b, ok := readBigramsCache(body, minCount, maxLen, fp); ok {
			return b, nil
		}
	}

	var recs []pairRec
	for _, f := range files {
		if err := readFile(f, func(line string) {
			w1, w2, count, ok := ParseBigram(line)
			if !ok {
				return
			}
			w1, w2 = text.Fold(w1), text.Fold(w2)
			if len(w1) > maxLen || len(w2) > maxLen || !text.IsSplittable(w1) || !text.IsSplittable(w2) {
				return
			}
			recs = append(recs, pairRec{w1: w1, w2: w2, c: int64(count)})
		}); err != nil {
			return nil, err
		}
	}

	// Merge duplicates and group: sort by (w1, w2), then fold each run of
	// equal pairs into one entry. The count filter runs on the merged count,
	// which is the same rule the word model applies.
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].w1 != recs[j].w1 {
			return recs[i].w1 < recs[j].w1
		}
		return recs[i].w2 < recs[j].w2
	})

	b := &Bigrams{index: make(map[string]int32, len(recs)/4)}
	for i := 0; i < len(recs); {
		w1 := recs[i].w1
		group := int32(len(b.start))
		first := int32(len(b.pairs))
		var sum int64
		for i < len(recs) && recs[i].w1 == w1 {
			w2 := recs[i].w2
			var merged int64
			for i < len(recs) && recs[i].w1 == w1 && recs[i].w2 == w2 {
				merged += recs[i].c
				i++
			}
			if merged < int64(minCount) {
				continue
			}
			b.pairs = append(b.pairs, bpair{w2: w2, c: merged})
			sum += merged
		}
		if len(b.pairs) == int(first) {
			// Every continuation of w1 was filtered out.
			continue
		}
		b.index[w1] = group
		b.start = append(b.start, first)
		b.end = append(b.end, int32(len(b.pairs)))
		b.total = append(b.total, sum)
	}
	if len(b.pairs) == 0 {
		saveCache(dir, bigramsCacheFile, bigramsCache(nil, minCount, maxLen, fp))
		return nil, nil
	}
	saveCache(dir, bigramsCacheFile, bigramsCache(b, minCount, maxLen, fp))
	return b, nil
}

// Cond returns log P(w2|w1) smoothed towards the unigram distribution.
// uni must return the unigram probability of a known word. When nothing is
// known about the pair it falls back to that unigram probability.
func (b *Bigrams) Cond(w1, w2 string, uni func(string) float64) float64 {
	if b == nil {
		return math.Log(uni(w2))
	}
	// Interpolated: (c(w1,w2) + K*p(w2)) / (c(w1) + K)
	const k = 5.0
	var c, left int64
	if g, ok := b.index[w1]; ok {
		lo, hi := int(b.start[g]), int(b.end[g])
		left = b.total[g]
		// Binary search the right word inside the group.
		for lo < hi {
			m := int(uint(lo+hi) >> 1)
			if b.pairs[m].w2 < w2 {
				lo = m + 1
			} else {
				hi = m
			}
		}
		if lo < int(b.end[g]) && b.pairs[lo].w2 == w2 {
			c = b.pairs[lo].c
		}
	}
	return math.Log((float64(c) + k*uni(w2)) / (float64(left) + k))
}

// Len is the number of distinct bigrams.
func (b *Bigrams) Len() int { return len(b.pairs) }

// ParseWordCount accepts "word", "word count" and "word\tcount", plus the
// "count word" layout when the first field is numeric and the second is not.
// When both leading fields are numeric the line is read as word-first, so
// "1\t70533" (a digit token with its count) is not mistaken for
// "count=1 word=70533"; the count defaults to 1.
func ParseWordCount(line string) (string, int) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", 0
	}
	if len(fields) > 1 {
		if _, ok := atoiField(fields[0]); ok {
			if _, ok := atoiField(fields[1]); !ok {
				return fields[1], atoiFieldMust(fields[0])
			}
		}
	}
	word := fields[0]
	count := 1
	for i := len(fields) - 1; i > 0; i-- {
		if n, ok := atoiField(fields[i]); ok {
			count = n
			break
		}
	}
	return word, count
}

// atoiFieldMust parses a decimal integer that is known to be parseable.
func atoiFieldMust(s string) int {
	n, _ := atoiField(s)
	return n
}

// ParseBigram accepts "w1 w2 count", "count w1 w2" and "w1 w2".
func ParseBigram(line string) (string, string, int, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || len(fields) > 3 {
		return "", "", 0, false
	}
	if len(fields) == 2 {
		return fields[0], fields[1], 1, true
	}
	if n, ok := atoiField(fields[0]); ok {
		return fields[1], fields[2], n, true
	}
	if n, ok := atoiField(fields[2]); ok {
		return fields[0], fields[1], n, true
	}
	return "", "", 0, false
}

// atoiField parses a non-negative decimal integer. It is a hand-rolled
// replacement for strconv.Atoi: dictionary counts are never negative and
// always fit in an int, and avoiding strconv keeps the parser allocation-free
// (strconv.Atoi allocates an error value for every non-numeric field).
func atoiField(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n < 0 {
			// Overflowed an int; treat as not-a-count.
			return 0, false
		}
	}
	return n, true
}

// readFile scans path line by line and calls fn for every line, tolerating
// lines longer than ScanBuffer by skipping them.
func readFile(path string, fn func(line string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), ScanBuffer)
	for sc.Scan() {
		fn(sc.Text())
	}
	return sc.Err()
}

// sizeHint estimates how many entries a "word count" line list of the given
// file sizes holds, so the merge map can be allocated at its final capacity
// instead of growing while it is filled.
func sizeHint(files []string, bytesPerEntry int) int {
	var n int
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			n += int(st.Size()) / bytesPerEntry
		}
	}
	if n < 1024 {
		n = 1024
	}
	return n
}
