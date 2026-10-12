package segment

import (
	"strings"

	"spell_splitter/internal/text"
)

// SepRuns returns the byte ranges of the maximal runs of separator runes in
// s. Anything that is neither a letter nor a digit separates words.
func SepRuns(s string) [][2]int {
	var runs [][2]int
	start := -1
	for byteIdx, r := range s {
		if text.IsWordRune(r) {
			if start >= 0 {
				runs = append(runs, [2]int{start, byteIdx})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = byteIdx
		}
	}
	if start >= 0 {
		runs = append(runs, [2]int{start, len(s)})
	}
	return runs
}

// Forms returns the separator-normalized forms of line: every separator run
// is either replaced by a space or dropped. At most maxForms forms are
// returned, the most exploded one (all separators kept as spaces) first.
func Forms(line string, maxForms int) []string {
	line = strings.TrimSpace(line)
	runs := SepRuns(line)
	if len(runs) == 0 || maxForms <= 0 {
		return nil
	}

	// With many separators the full cross product is useless, so only the
	// exploded form, the glued form and the single-removal forms are kept.
	exhaustive := len(runs) <= 4

	var out []string
	seen := map[string]bool{}
	add := func(mask []bool) {
		if len(out) >= maxForms {
			return
		}
		form := applyMask(line, runs, mask)
		if seen[form] {
			return
		}
		seen[form] = true
		out = append(out, form)
	}

	if exhaustive {
		for bits := 1<<len(runs) - 1; bits >= 0; bits-- {
			mask := make([]bool, len(runs))
			for i := range runs {
				mask[i] = bits&(1<<i) != 0
			}
			add(mask)
			if len(out) >= maxForms {
				break
			}
		}
		return out
	}

	all := make([]bool, len(runs))
	for i := range all {
		all[i] = true
	}
	add(all)
	for i := range runs {
		mask := append([]bool(nil), all...)
		mask[i] = false
		add(mask)
	}
	return out
}

// applyMask rebuilds s keeping (true) or dropping (false) every separator run.
func applyMask(s string, runs [][2]int, mask []bool) string {
	var b strings.Builder
	pos := 0
	for i, r := range runs {
		b.WriteString(s[pos:r[0]])
		if mask[i] {
			b.WriteByte(' ')
		}
		pos = r[1]
	}
	b.WriteString(s[pos:])
	return strings.TrimSpace(b.String())
}
