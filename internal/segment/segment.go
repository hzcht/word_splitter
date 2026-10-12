// Package segment splits glued words (spells) back into their word forms
// using the dictionaries loaded by the dict package.
package segment

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"spell_splitter/internal/dict"
	"spell_splitter/internal/text"
)

// Options tunes the segmentation search. Zero values are replaced by the
// defaults when a Segmenter is built, so a zero Options is safe.
type Options struct {
	// MinWordLen is the shortest dictionary word considered.
	MinWordLen int
	// MaxWordLen is the longest dictionary word considered; 0 means "no
	// limit beyond the dictionary".
	MaxWordLen int
	// MaxTokenLen is the length beyond which a token is not split at all.
	MaxTokenLen int
	// MaxCandidates caps the number of candidates returned per token.
	MaxCandidates int
	// MaxNodes caps the search effort per token.
	MaxNodes int
	// MaxUnknownLen caps the length of an unknown run inside a token.
	MaxUnknownLen int
	// UnknownPerRune is the log probability of a single rune of a word
	// missing from the dictionary.
	UnknownPerRune float64
}

// DefaultOptions returns the options used by the run command.
func DefaultOptions() Options {
	return Options{
		MinWordLen:     1,
		MaxWordLen:     0,
		MaxTokenLen:    64,
		MaxCandidates:  8,
		MaxNodes:       20000,
		MaxUnknownLen:  12,
		UnknownPerRune: math.Log(0.01),
	}
}

func (o Options) withDefaults() Options {
	def := DefaultOptions()
	if o.MinWordLen < 1 {
		o.MinWordLen = def.MinWordLen
	}
	if o.MaxTokenLen < 1 {
		o.MaxTokenLen = def.MaxTokenLen
	}
	if o.MaxCandidates < 1 {
		o.MaxCandidates = def.MaxCandidates
	}
	if o.MaxNodes < 1 {
		o.MaxNodes = def.MaxNodes
	}
	if o.MaxUnknownLen < 1 {
		o.MaxUnknownLen = def.MaxUnknownLen
	}
	if o.UnknownPerRune == 0 {
		o.UnknownPerRune = def.UnknownPerRune
	}
	return o
}

// Candidate is one way of spelling out a token (or a whole line).
type Candidate struct {
	Words []string
	Score float64
	// Split reports whether the token was actually broken into several
	// words, as opposed to being kept whole.
	Split bool
}

// String renders the candidate as a spell.
func (c Candidate) String() string { return strings.Join(c.Words, " ") }

// Segmenter splits tokens into words.
type Segmenter struct {
	Uni *dict.Words
	Bi  *dict.Bigrams
	Opt Options
}

// New builds a segmenter. bi may be nil, in which case only unigram
// probabilities are used. Zero-valued options fall back to defaults.
func New(uni *dict.Words, bi *dict.Bigrams, opt Options) *Segmenter {
	return &Segmenter{Uni: uni, Bi: bi, Opt: opt.withDefaults()}
}

type edge struct {
	j    int
	logp float64
	word string
}

type pathItem struct {
	words []string
	score float64
}

// unknownScore scores an unknown word of l runes.
func (s *Segmenter) unknownScore(l int) float64 {
	return s.Opt.UnknownPerRune * float64(l)
}

// wordLogProb returns the log probability of a word, known or not.
func (s *Segmenter) wordLogProb(word string) float64 {
	if p, ok := s.Uni.LogProb(word); ok {
		return p
	}
	return s.unknownScore(utf8.RuneCountInString(word))
}

// uniProb is the (linear) probability of a word, used to smooth bigrams.
func (s *Segmenter) uniProb(word string) float64 {
	return math.Exp(s.wordLogProb(word))
}

// score is the Markov score of a whole sequence of words:
//
//	log P(w0) + sum_i log P(wi | w(i-1))
//
// Missing bigrams fall back to the unigram probability of wi, and missing
// words to UnknownPerRune per rune, so every candidate is compared on the
// same scale.
func (s *Segmenter) score(words []string) float64 {
	if len(words) == 0 {
		return 0
	}
	total := s.wordLogProb(words[0])
	for i := 1; i < len(words); i++ {
		total += s.Bi.Cond(words[i-1], words[i], s.uniProb)
	}
	return total
}

// Segment returns the best-first candidate spellings of tok.
func (s *Segmenter) Segment(tok string) []Candidate {
	low := text.Fold(tok)
	if low == "" {
		return nil
	}

	whole := Candidate{Words: []string{low}, Score: s.wordLogProb(low)}
	cands := []Candidate{whole}
	if !text.IsSplittable(low) || utf8.RuneCountInString(low) > s.Opt.MaxTokenLen {
		return s.finalize(cands)
	}

	var arena wordArena
	tab := s.buildEdges(low)
	splits := s.enumDict(tab, &arena, 0, len(low), s.Opt.MaxCandidates, s.Opt.MaxNodes)
	for _, p := range splits {
		cands = append(cands, Candidate{Words: p.words, Split: len(p.words) > 1})
	}
	// Partial splits always rank below the dictionary ones, so only look
	// for them when dictionary splits cannot fill the candidate list.
	if len(splits) < s.Opt.MaxCandidates {
		cands = append(cands, s.partialPaths(tab, &arena, low, s.Opt.MaxCandidates-len(splits))...)
	}
	return s.finalize(cands)
}

// finalize scores, deduplicates, sorts and caps candidates.
func (s *Segmenter) finalize(cands []Candidate) []Candidate {
	for i := range cands {
		cands[i].Score = s.score(cands[i].Words)
	}

	seen := make(map[string]int, len(cands))
	uniq := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		key := strings.Join(c.Words, " ")
		if i, ok := seen[key]; ok {
			if c.Score > uniq[i].Score {
				uniq[i].Score = c.Score
			}
			uniq[i].Split = uniq[i].Split || c.Split
			continue
		}
		seen[key] = len(uniq)
		uniq = append(uniq, c)
	}
	sort.SliceStable(uniq, func(i, j int) bool { return uniq[i].Score > uniq[j].Score })
	if len(uniq) > s.Opt.MaxCandidates {
		uniq = uniq[:s.Opt.MaxCandidates]
	}
	return uniq
}

// enumDict enumerates up to k complete dictionary segmentations of t[lo:hi]
// from the token's prebuilt edge table, best first, spending at most budget
// search steps. Accepted paths copy their words into ar. It returns nil when
// k <= 0 or the range is empty.
func (s *Segmenter) enumDict(tab edgeTable, ar *wordArena, lo, hi, k, budget int) []pathItem {
	if k <= 0 || lo >= hi {
		return nil
	}
	n := hi - lo

	// best[i] bounds the score of any continuation from relative position i,
	// taking both dictionary words and one unknown word into account. Because
	// bigram terms and unknown words only ever lower a score, the plain
	// unigram sum is admissible for pruning.
	best := make([]float64, n+1)
	best[n] = 0
	for i := n - 1; i >= 0; i-- {
		p := lo + i
		v := math.Inf(-1)
		for ei := tab.starts[p]; ei < tab.starts[p+1]; ei++ {
			e := tab.edges[ei]
			if e.j > hi {
				break // edges are sorted by end position
			}
			if c := e.logp + best[e.j-lo]; c > v {
				v = c
			}
		}
		for j := i + 1; j <= n && j-i <= s.Opt.MaxUnknownLen; j++ {
			if c := s.unknownScore(j-i) + best[j]; c > v {
				v = c
			}
		}
		best[i] = v
	}

	res := &pathHeap{k: k}
	words := make([]string, 0, 16)
	stopped := false

	// rec walks dictionary edges; the pathHeap keeps the best k while the
	// threshold prunes branches that cannot reach it.
	var rec func(i int, score float64)
	rec = func(i int, score float64) {
		if stopped {
			return
		}
		if i == n {
			// Replicates pathHeap.add's acceptance test: a path that cannot
			// enter the heap never pays for copying its words.
			if len(res.items) < res.k || score > res.items[0].score {
				res.add(pathItem{words: ar.put(words), score: score})
			}
			return
		}
		if score+best[i] < res.threshold() {
			return
		}
		p := lo + i
		for ei := tab.starts[p]; ei < tab.starts[p+1]; ei++ {
			e := tab.edges[ei]
			if e.j > hi {
				break
			}
			if budget <= 0 {
				stopped = true
				return
			}
			budget--
			words = append(words, e.word)
			rec(e.j-lo, score+e.logp)
			words = words[:len(words)-1]
		}
	}
	rec(0, 0)
	return res.sorted()
}

// edgeTable holds every dictionary edge of one token in a single flat slice
// grouped by start position: starts[p]..starts[p+1] are the edges leaving
// position p, sorted by end position. Building it once per token replaces the
// per-position (and per-prefix/suffix) allocations of edgesAt.
type edgeTable struct {
	starts []int
	edges  []edge
}

// wordArena hands out copies of word sequences as sub-slices of chunked
// backing arrays. Accepted search paths share the chunks instead of each
// allocating their own words slice; chunks stay alive as long as any holder
// references them, and nothing ever writes through the handed-out slices
// (they are capped to their length for that reason).
type wordArena struct {
	chunks [][]string
	used   int
}

func (a *wordArena) put(words []string) []string {
	n := len(words)
	if len(a.chunks) == 0 || cap(a.chunks[len(a.chunks)-1])-a.used < n {
		size := 16
		if n > size {
			size = n
		}
		a.chunks = append(a.chunks, make([]string, size))
		a.used = 0
	}
	cur := a.chunks[len(a.chunks)-1]
	out := cur[a.used : a.used+n : a.used+n]
	copy(out, words)
	a.used += n
	return out
}

// buildEdges collects the dictionary edges of t.
func (s *Segmenter) buildEdges(t string) edgeTable {
	n := len(t)
	maxLen := s.Opt.MaxWordLen
	if maxLen <= 0 || maxLen > s.Uni.MaxLen() {
		maxLen = s.Uni.MaxLen()
	}
	tab := edgeTable{starts: make([]int, n+1), edges: make([]edge, 0, n*4)}
	for i := 0; i < n; i++ {
		tab.starts[i] = len(tab.edges)
		m := maxLen
		if m > n-i {
			m = n - i
		}
		for l := s.Opt.MinWordLen; l <= m; l++ {
			word := t[i : i+l]
			if p, ok := s.Uni.LogProb(word); ok {
				tab.edges = append(tab.edges, edge{j: i + l, logp: p, word: word})
			}
		}
	}
	tab.starts[n] = len(tab.edges)
	return tab
}

// coverable marks the positions from which the prefix and the suffix of t can
// be covered by whole dictionary words.
func (s *Segmenter) coverable(tab edgeTable) (fwd, bwd []bool) {
	n := len(tab.starts) - 1
	fwd = make([]bool, n+1)
	bwd = make([]bool, n+1)
	fwd[0] = true
	for i := 0; i < n; i++ {
		if !fwd[i] {
			continue
		}
		for ei := tab.starts[i]; ei < tab.starts[i+1]; ei++ {
			fwd[tab.edges[ei].j] = true
		}
	}
	bwd[n] = true
	for i := n - 1; i >= 0; i-- {
		for ei := tab.starts[i]; ei < tab.starts[i+1]; ei++ {
			if bwd[tab.edges[ei].j] {
				bwd[i] = true
				break
			}
		}
	}
	return fwd, bwd
}

// partialPaths enumerates up to k segmentations carrying exactly one unknown
// run; the text around the run is covered by dictionary words.
func (s *Segmenter) partialPaths(tab edgeTable, ar *wordArena, t string, k int) []Candidate {
	n := len(t)
	if n < 2 || k <= 0 {
		return nil
	}
	fwd, bwd := s.coverable(tab)

	// pre[a] enumerates the covers of t[:a], suf[b] those of t[b:]. Only
	// positions that can actually be covered are searched, which keeps the
	// search linear for glued tokens that are mostly unknown.
	pre := make([][]pathItem, n+1)
	suf := make([][]pathItem, n+1)
	pre[0] = []pathItem{{score: 0}}
	suf[n] = []pathItem{{score: 0}}
	for i := 1; i <= n; i++ {
		if fwd[i] {
			pre[i] = s.enumDict(tab, ar, 0, i, 2, s.Opt.MaxNodes)
		}
		if bwd[i] {
			suf[i] = s.enumDict(tab, ar, i, n, 2, s.Opt.MaxNodes)
		}
	}

	var out []Candidate
	for a := 0; a <= n; a++ {
		if len(pre[a]) == 0 {
			continue
		}
		for b := a + 1; b <= n && b-a <= s.Opt.MaxUnknownLen; b++ {
			if len(suf[b]) == 0 {
				continue
			}
			for _, p := range pre[a] {
				for _, q := range suf[b] {
					words := make([]string, 0, len(p.words)+len(q.words)+1)
					words = append(words, p.words...)
					words = append(words, t[a:b])
					words = append(words, q.words...)
					out = append(out, Candidate{Words: words, Split: len(words) > 1})
				}
			}
		}
	}

	for i := range out {
		out[i].Score = s.score(out[i].Words)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// pathHeap keeps the k best paths; the worst of them stays at the root.
//
// This is a hand-rolled binary min-heap (root = lowest score) rather than
// container/heap, which boxes every push/pop through `any` and dominates the
// per-token allocation count. The sift order matches container/heap exactly.
type pathHeap struct {
	items []pathItem
	k     int
}

func (h *pathHeap) push(p pathItem) {
	h.items = append(h.items, p)
	i := len(h.items) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if h.items[i].score >= h.items[parent].score {
			break
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

func (h *pathHeap) pop() pathItem {
	n := len(h.items) - 1
	h.items[0], h.items[n] = h.items[n], h.items[0]
	it := h.items[n]
	h.items = h.items[:n]
	h.down(0)
	return it
}

func (h *pathHeap) down(i int) {
	n := len(h.items)
	for {
		left, right := 2*i+1, 2*i+2
		small := i
		if left < n && h.items[left].score < h.items[small].score {
			small = left
		}
		if right < n && h.items[right].score < h.items[small].score {
			small = right
		}
		if small == i {
			return
		}
		h.items[i], h.items[small] = h.items[small], h.items[i]
		i = small
	}
}

func (h *pathHeap) add(p pathItem) {
	if h.k <= 0 {
		return
	}
	if len(h.items) < h.k {
		h.push(p)
		return
	}
	if p.score > h.items[0].score {
		h.pop()
		h.push(p)
	}
}

// threshold is the score of the worst kept path; -Inf while the heap is not
// full, which disables pruning.
func (h *pathHeap) threshold() float64 {
	if h.k <= 0 || len(h.items) < h.k {
		return math.Inf(-1)
	}
	return h.items[0].score
}

func (h *pathHeap) sorted() []pathItem {
	out := append([]pathItem(nil), h.items...)
	sort.Slice(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

// Combine enumerates combinations of per-token candidates, best first, and
// returns at most k. Every element of sets must be sorted best first.
//
// The combinations are walked in the order of their additive (per-token)
// scores; a pool of the best ones is then re-scored as whole lines so that
// bigrams spanning token boundaries are taken into account, and the top k of
// that pool is returned.
func (s *Segmenter) Combine(sets [][]Candidate, k int) []Candidate {
	if len(sets) == 0 || k <= 0 {
		return nil
	}
	for _, set := range sets {
		if len(set) == 0 {
			return nil
		}
	}

	idx := make([]int, len(sets))
	score := 0.0
	split := false
	for _, set := range sets {
		score += set[0].Score
		split = split || set[0].Split
	}

	pool := 2000
	if size := productSize(sets); size < pool {
		pool = size
	}

	h := &comboHeap{}
	seen := map[string]bool{keyOf(idx): true}
	h.push(comboItem{idx: idx, score: score, split: split})

	running := make([]Candidate, 0, min(pool, 256))
	for len(h.items) > 0 && len(running) < pool {
		it := h.pop()
		words := make([]string, 0, len(sets)+2)
		for i, ci := range it.idx {
			words = append(words, sets[i][ci].Words...)
		}
		running = append(running, Candidate{Words: words, Split: it.split})
		for i := range sets {
			if it.idx[i]+1 >= len(sets[i]) {
				continue
			}
			next := append([]int(nil), it.idx...)
			next[i]++
			key := keyOf(next)
			if seen[key] {
				continue
			}
			seen[key] = true
			h.push(comboItem{
				idx:   next,
				score: it.score - sets[i][it.idx[i]].Score + sets[i][next[i]].Score,
				split: it.split || sets[i][next[i]].Split,
			})
		}
	}

	for i := range running {
		running[i].Score = s.score(running[i].Words)
	}
	sort.SliceStable(running, func(i, j int) bool { return running[i].Score > running[j].Score })

	// Two different index paths always produce different Words: the number
	// of words each token contributes is fixed by the path, so equal Words
	// would force equal paths. No string-level dedup is needed here;
	// processLine dedups across forms by the rendered spell.
	out := make([]Candidate, 0, min(k, len(running)))
	for _, c := range running {
		out = append(out, c)
		if len(out) >= k {
			break
		}
	}
	return out
}

// productSize is the number of combinations in sets, saturating at 1<<31.
func productSize(sets [][]Candidate) int {
	n := 1
	for _, set := range sets {
		if len(set) == 0 {
			return 0
		}
		n *= len(set)
		if n >= 1<<31 {
			return 1 << 31
		}
	}
	return n
}

func keyOf(idx []int) string {
	var b strings.Builder
	for _, v := range idx {
		b.WriteString(strconv.Itoa(v))
		b.WriteByte(',')
	}
	return b.String()
}

type comboItem struct {
	idx   []int
	score float64
	split bool
}

// comboHeap is a hand-rolled binary max-heap (root = highest score). It
// replaces container/heap, which boxes every push/pop in `any`; the sift
// order matches container/heap exactly.
type comboHeap struct{ items []comboItem }

func (h *comboHeap) push(c comboItem) {
	h.items = append(h.items, c)
	i := len(h.items) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if h.items[i].score <= h.items[parent].score {
			break
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

func (h *comboHeap) pop() comboItem {
	n := len(h.items) - 1
	h.items[0], h.items[n] = h.items[n], h.items[0]
	it := h.items[n]
	h.items = h.items[:n]
	h.down(0)
	return it
}

func (h *comboHeap) down(i int) {
	n := len(h.items)
	for {
		left, right := 2*i+1, 2*i+2
		best := i
		if left < n && h.items[left].score > h.items[best].score {
			best = left
		}
		if right < n && h.items[right].score > h.items[best].score {
			best = right
		}
		if best == i {
			return
		}
		h.items[i], h.items[best] = h.items[best], h.items[i]
		i = best
	}
}
