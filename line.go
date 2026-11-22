package main

import (
	"math"
	"slices"
	"strings"

	"spell_splitter/internal/segment"
	"spell_splitter/internal/text"
)

// processLine turns one input line into its spell rows. The boolean reports
// whether the line was skipped because it had no usable column.
func processLine(raw string, seg *segment.Segmenter, cfg runConfig) ([]row, bool) {
	key, ok := column(raw, cfg.col)
	if !ok {
		return nil, true
	}

	// The baseline is the best unsplit reading: the line as it came in or
	// the same line with its separators glued together. A split must beat it
	// to survive -min_gain.
	base := seg.WholeScore(key)
	if glued := text.Glue(key); glued != "" {
		if q := seg.WholeScore(glued); q > base {
			base = q
		}
	}

	rows := make([]row, 0, cfg.maxVariants)
	if !cfg.noAsis {
		rows = append(rows, row{in: key, spell: key, src: srcAsIs, quality: base})
	}

	forms := segment.Forms(key, cfg.maxForms)
	if len(forms) == 0 {
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
	slices.SortFunc(pool, segment.Compare)

	best := math.Inf(-1)
	for _, cand := range pool {
		if cand.Score > best {
			best = cand.Score
		}
	}

	seen := make(map[string]bool, len(pool)+1)
	if !cfg.noAsis {
		seen[key] = true
	}
	for _, cand := range pool {
		if len(rows) >= cfg.maxVariants {
			break
		}
		if cand.Score < base+cfg.minGain {
			continue
		}
		if best > math.Inf(-1) && cand.Score < best-cfg.window {
			continue
		}
		spell := cand.String()
		if seen[spell] {
			continue
		}
		seen[spell] = true
		rows = append(rows, row{
			in:      key,
			spell:   spell,
			src:     sourceOf(cand, seg),
			quality: cand.Score,
		})
	}

	if cfg.rank == "quality" {
		slices.SortStableFunc(rows, compareRows)
	}
	assignConfidence(rows)
	return rows, false
}

// column returns the trimmed col-th tab-separated field of raw.
func column(raw string, col int) (string, bool) {
	start := 0
	for i := 1; i < col; i++ {
		j := strings.IndexByte(raw[start:], '\t')
		if j < 0 {
			return "", false
		}
		start += j + 1
	}
	end := strings.IndexByte(raw[start:], '\t')
	if end >= 0 {
		raw = raw[start : start+end]
	} else {
		raw = raw[start:]
	}
	key := strings.TrimSpace(raw)
	return key, key != ""
}

// sourceOf labels how a candidate was produced.
func sourceOf(cand segment.Candidate, seg *segment.Segmenter) string {
	if !cand.Split {
		return srcSep
	}
	for _, w := range cand.Words {
		if _, ok := seg.Uni.Lookup(w); !ok && !text.IsDigits(w) {
			return srcPartial
		}
	}
	return srcSplit
}

// compareRows orders rows for -rank quality.
func compareRows(a, b row) int {
	if a.quality > b.quality {
		return -1
	}
	if a.quality < b.quality {
		return 1
	}
	aw, bw := len(strings.Fields(a.spell)), len(strings.Fields(b.spell))
	if aw != bw {
		return aw - bw
	}
	return strings.Compare(a.spell, b.spell)
}

// assignConfidence turns the quality scores of one input line into a softmax
// confidence. The numbers are relative to the alternatives on that line, not
// calibrated probabilities.
func assignConfidence(rows []row) {
	if len(rows) == 0 {
		return
	}
	maxQ := rows[0].quality
	for _, r := range rows[1:] {
		if r.quality > maxQ {
			maxQ = r.quality
		}
	}
	var sum float64
	for i := range rows {
		rows[i].confidence = math.Exp(rows[i].quality - maxQ)
		sum += rows[i].confidence
	}
	if sum == 0 {
		return
	}
	for i := range rows {
		rows[i].confidence /= sum
	}
}
