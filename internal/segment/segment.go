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

// Options tunes the segmentation search. Length and budget fields use the
// defaults when zero; scoring fields are used as given, so a zero penalty
// disables that term. Use DefaultOptions for the recommended model.
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
	// MaxWords caps the number of words in one candidate.
	MaxWords int
	// UnknownPerRune is the log probability of a single rune of a word
	// missing from the dictionary.
	UnknownPerRune float64
	// WordPenalty is subtracted once per word boundary of a multi-word
	// candidate. It is the prior against gratuitous splitting.
	WordPenalty float64
	// ShortWordPenalty is multiplied by the missing letters of every
	// non-numeric word shorter than ShortWordLen.
	ShortWordPenalty float64
	// ShortWordLen is the length below which ShortWordPenalty applies.
	ShortWordLen int
	// BigramBackoff selects the unseen-pair rule: "unigram" (default) or
	// "legacy".
	BigramBackoff string
}

// DefaultOptions returns the options used by the run command.
func DefaultOptions() Options {
	return Options{
		MinWordLen:       1,
		MaxWordLen:       0,
		MaxTokenLen:      64,
		MaxCandidates:    8,
		MaxNodes:         20000,
		MaxUnknownLen:    12,
		MaxWords:         6,
		UnknownPerRune:   math.Log(0.01),
		WordPenalty:      1.5,
		ShortWordPenalty: 2.0,
		ShortWordLen:     3,
		BigramBackoff:    "unigram",
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
	if o.MaxWords < 1 {
		o.MaxWords = def.MaxWords
	}
	if o.ShortWordLen < 1 {
		o.ShortWordLen = def.ShortWordLen
	}
	if o.UnknownPerRune == 0 {
		o.UnknownPerRune = def.UnknownPerRune
	}
	if o.BigramBackoff == "" {
		o.BigramBackoff = def.BigramBackoff
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

// Compare orders candidates from best to worst: higher score first, then
// fewer words, then a longer shortest word, then lexicographic order. The
// tie-breakers make the output deterministic and prefer the more readable
// spelling when the probabilistic model cannot separate two candidates.
func Compare(a, b Candidate) int {
	if a.Score > b.Score {
		return -1
	}
	if a.Score < b.Score {
		return 1
	}
	if len(a.Words) != len(b.Words) {
		return len(a.Words) - len(b.Words)
	}
	as, bs := shortestWord(a.Words), shortestWord(b.Words)
	if as != bs {
		return bs - as
	}
	for i := 0; i < len(a.Words) && i < len(b.Words); i++ {
		if a.Words[i] < b.Words[i] {
			return -1
		}
		if a.Words[i] > b.Words[i] {
			return 1
		}
	}
	return 0
}

func shortestWord(words []string) int {
	best := 0
	for _, w := range words {
		if best == 0 || len(w) < best {
			best = len(w)
		}
	}
	return best
}

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

// score is the quality score of a whole sequence of words:
//
//	log P(w0) + sum_i log P(wi | w(i-1))
//	- WordPenalty * (len(words)-1)
//	- short-word penalties
//
// Missing bigrams fall back to the unigram probability of wi, and missing
// words to UnknownPerRune per rune, so every candidate is compared on the
// same scale. The penalties make a split pay for every extra boundary and
// for tiny fragments such as "b" or "y".
func (s *Segmenter) score(words []string) float64 {
	if len(words) == 0 {
		return 0
	}
	total := s.wordLogProb(words[0])
	for i := 1; i < len(words); i++ {
		if s.Opt.BigramBackoff == "legacy" {
			total += s.Bi.CondLegacy(words[i-1], words[i], s.uniProb)
		} else {
			total += s.Bi.Cond(words[i-1], words[i], s.uniProb)
		}
	}
	if len(words) > 1 {
		total -= s.Opt.WordPenalty * float64(len(words)-1)
		for _, w := range words {
			total -= s.shortPenalty(w)
		}
	}
	return total
}

// WholeScore scores tok as one unsplit word. It is the baseline against
// which split candidates are filtered.
func (s *Segmenter) WholeScore(tok string) float64 {
	low := text.Fold(tok)
	if low == "" {
		return 0
	}
	return s.score([]string{low})
}

// shortPenalty charges non-numeric words shorter than ShortWordLen.
func (s *Segmenter) shortPenalty(w string) float64 {
	if s.Opt.ShortWordPenalty == 0 || len(w) >= s.Opt.ShortWordLen || text.IsDigits(w) {
		return 0
	}
	return float64(s.Opt.ShortWordLen-len(w)) * s.Opt.ShortWordPenalty
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
	// Over-generate dictionary paths: the unigram bound used by enumDict is
	// not admissible once bigram scores replace unigram scores, so a path
	// pruned at k can win after finalize re-scores it.
	enumK := s.Opt.MaxCandidates
	if enumK <= 64 {
		enumK *= 4
	}
	splits := s.enumDict(tab, &arena, 0, len(low), enumK, s.Opt.MaxNodes)
	for _, p := range splits {
		cands = append(cands, Candidate{Words: p.words, Split: len(p.words) > 1})
	}
	// Partial splits always rank below the dictionary ones, so only look
	// for them when dictionary splits cannot fill the candidate list.
	if len(splits) < enumK {
		cands = append(cands, s.partialPaths(tab, low, enumK-len(splits))...)
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
		if len(c.Words) > s.Opt.MaxWords {
			continue
		}
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
	sort.SliceStable(uniq, func(i, j int) bool { return Compare(uniq[i], uniq[j]) < 0 })
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
	// taking both dictionary words and one unknown word into account. The
	// unigram sum is only an optimistic heuristic once bigram scores replace
	// unigram scores: a seen bigram can be higher than its unigram. Callers
	// over-generate (see Segment) so a path pruned here can still be found.
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

// bestPrefixes computes up to k best dictionary-only covers of every prefix
// of the token, by unigram score, in one forward pass. The old implementation
// called enumDict once per prefix and spent O(n*MaxNodes) work; this is
// O(edges*k log k).
func (s *Segmenter) bestPrefixes(tab edgeTable, n, k int) [][]pathItem {
	pre := make([][]pathItem, n+1)
	pre[0] = []pathItem{{score: 0}}
	for i := 0; i < n; i++ {
		if len(pre[i]) == 0 {
			continue
		}
		for ei := tab.starts[i]; ei < tab.starts[i+1]; ei++ {
			e := tab.edges[ei]
			for _, p := range pre[i] {
				words := make([]string, len(p.words)+1)
				copy(words, p.words)
				words[len(p.words)] = e.word
				pre[e.j] = addPath(pre[e.j], pathItem{words: words, score: p.score + e.logp}, k)
			}
		}
	}
	return pre
}

// bestSuffixes is the backward counterpart of bestPrefixes.
func (s *Segmenter) bestSuffixes(tab edgeTable, n, k int) [][]pathItem {
	suf := make([][]pathItem, n+1)
	suf[n] = []pathItem{{score: 0}}
	for i := n - 1; i >= 0; i-- {
		for ei := tab.starts[i]; ei < tab.starts[i+1]; ei++ {
			e := tab.edges[ei]
			for _, q := range suf[e.j] {
				words := make([]string, len(q.words)+1)
				words[0] = e.word
				copy(words[1:], q.words)
				suf[i] = addPath(suf[i], pathItem{words: words, score: e.logp + q.score}, k)
			}
		}
	}
	return suf
}

// addPath inserts p into the best-k list, keeping it sorted best first.
func addPath(list []pathItem, p pathItem, k int) []pathItem {
	list = append(list, p)
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	if len(list) > k {
		list = list[:k]
	}
	return list
}

// partialPaths enumerates up to k segmentations carrying exactly one unknown
// run; the text around the run is covered by dictionary words.
func (s *Segmenter) partialPaths(tab edgeTable, t string, k int) []Candidate {
	n := len(t)
	if n < 2 || k <= 0 {
		return nil
	}
	pre := s.bestPrefixes(tab, n, 2)
	suf := s.bestSuffixes(tab, n, 2)

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
					if len(words) > s.Opt.MaxWords {
						continue
					}
					out = append(out, Candidate{Words: words, Split: len(words) > 1})
				}
			}
		}
	}

	for i := range out {
		out[i].Score = s.score(out[i].Words)
	}
	sort.SliceStable(out, func(i, j int) bool { return Compare(out[i], out[j]) < 0 })
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
	for _, set := range sets {
		score += set[0].Score
	}

	pool := 4096
	if k < 256 {
		pool = k * len(sets) * 16
	}
	if pool < 512 {
		pool = 512
	}
	if size := productSize(sets); size < pool {
		pool = size
	}

	h := &comboHeap{}
	seen := map[string]bool{keyOf(idx): true}
	h.push(comboItem{idx: idx, score: score})

	running := make([]Candidate, 0, min(pool, 256))
	for len(h.items) > 0 && len(running) < pool {
		it := h.pop()
		words := make([]string, 0, len(sets)+2)
		for i, ci := range it.idx {
			words = append(words, sets[i][ci].Words...)
		}
		if len(words) <= s.Opt.MaxWords {
			// Recompute Split from the selected candidates. Inheriting it
			// from the parent combination marks a whole glued token as
			// split whenever any earlier combination was split.
			split := false
			for i, ci := range it.idx {
				if sets[i][ci].Split {
					split = true
					break
				}
			}
			running = append(running, Candidate{Words: words, Split: split})
		}
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
			})
		}
	}

	for i := range running {
		running[i].Score = s.score(running[i].Words)
	}
	sort.SliceStable(running, func(i, j int) bool { return Compare(running[i], running[j]) < 0 })

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
