package dict

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
)

// A warm start reads a binary cache from the dictionary directory instead of
// re-parsing every *.txt / *.bigrams source. The cache records a fingerprint
// of the source files (name, size, mtime) plus the loader parameters, so any
// change to a source, a parameter or the cache format falls back to the text
// parse and rewrites the cache. A CRC32 over the whole payload detects
// truncated or corrupted files. Cache writes are best-effort: a read-only
// directory simply never gets a cache.
const (
	wordsCacheFile   = ".words.cache"
	bigramsCacheFile = ".bigrams.cache"
	// cacheVersion must be bumped whenever parsing, folding, filtering or
	// the model layout changes: the cache stores post-filter structures and
	// would otherwise silently keep serving results from the old semantics.
	cacheVersion = uint32(1)
)

var cacheMagic = [4]byte{'S', 'S', 'D', 'C'}

// fileFP is one source file's identity in the cache fingerprint.
type fileFP struct {
	name  string
	size  int64
	mtime int64
}

// fingerprint describes every source file, in the order the loader saw them.
func fingerprint(files []string) []fileFP {
	fp := make([]fileFP, 0, len(files))
	for _, f := range files {
		one := fileFP{name: filepath.Base(f), size: -1, mtime: -1}
		if st, err := os.Stat(f); err == nil {
			one.size, one.mtime = st.Size(), st.ModTime().UnixNano()
		}
		fp = append(fp, one)
	}
	return fp
}

// cacheWriter accumulates a little-endian cache payload.
type cacheWriter struct{ buf bytes.Buffer }

func (w *cacheWriter) u32(v uint32) {
	w.buf.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}

func (w *cacheWriter) i64(v int64) { w.u32(uint32(v)); w.u32(uint32(v >> 32)) }

func (w *cacheWriter) str(s string) {
	w.u32(uint32(len(s)))
	w.buf.WriteString(s)
}

func (w *cacheWriter) header(p1, p2 int, fp []fileFP) {
	w.buf.Write(cacheMagic[:])
	w.u32(cacheVersion)
	w.u32(uint32(int32(p1)))
	w.u32(uint32(int32(p2)))
	w.u32(uint32(len(fp)))
	for _, f := range fp {
		w.str(f.name)
		w.i64(f.size)
		w.i64(f.mtime)
	}
}

// cacheReader walks a cache payload; any out-of-bounds read clears ok.
type cacheReader struct {
	b   []byte
	off int
	ok  bool
}

func (r *cacheReader) need(n int) bool {
	if !r.ok || n < 0 || len(r.b)-r.off < n {
		r.ok = false
		return false
	}
	return true
}

func (r *cacheReader) u32() uint32 {
	if !r.need(4) {
		return 0
	}
	v := binary.LittleEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v
}

func (r *cacheReader) i64() int64 {
	lo, hi := uint64(r.u32()), uint64(r.u32())
	return int64(lo | hi<<32)
}

func (r *cacheReader) str() string {
	n := int(r.u32())
	if !r.need(n) {
		return ""
	}
	s := string(r.b[r.off : r.off+n])
	r.off += n
	return s
}

// header validates magic, format version, loader parameters and fingerprint.
func (r *cacheReader) header(p1, p2 int, fp []fileFP) bool {
	if !r.need(len(cacheMagic)) || !bytes.Equal(r.b[r.off:r.off+len(cacheMagic)], cacheMagic[:]) {
		return false
	}
	r.off += len(cacheMagic)
	if v := r.u32(); v != cacheVersion {
		return false
	}
	if int(int32(r.u32())) != p1 || int(int32(r.u32())) != p2 {
		return false
	}
	if int(r.u32()) != len(fp) {
		return false
	}
	for i := range fp {
		if r.str() != fp[i].name || r.i64() != fp[i].size || r.i64() != fp[i].mtime {
			return false
		}
	}
	return r.ok
}

// payload returns the cache with its trailing CRC32 verified.
func payload(b []byte) ([]byte, bool) {
	if len(b) < 4 {
		return nil, false
	}
	body, want := b[:len(b)-4], binary.LittleEndian.Uint32(b[len(b)-4:])
	return body, crc32.ChecksumIEEE(body) == want
}

// saveCache atomically installs buf as dir/name via a temp file + rename and
// ignores every error (a read-only directory just never gets a cache).
func saveCache(dir, name string, buf *bytes.Buffer) {
	tmp, err := os.CreateTemp(dir, name)
	if err != nil {
		return
	}
	_, werr := tmp.Write(buf.Bytes())
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return
	}
	if os.Rename(tmp.Name(), filepath.Join(dir, name)) != nil {
		os.Remove(tmp.Name())
	}
}

// readCache returns the verified payload of dir/name, if it exists and its
// CRC matches.
func readCache(dir, name string) ([]byte, bool) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, false
	}
	return payload(b)
}

// finishCache appends the CRC of everything written so far.
func finishCache(w *cacheWriter) *bytes.Buffer {
	sum := crc32.ChecksumIEEE(w.buf.Bytes())
	w.u32(sum)
	return &w.buf
}

// wordsCache serialises a Words model (counts only; log probabilities are
// recomputed on load with the same float arithmetic as the text path).
func wordsCache(w *Words, minWord, minCount int, fp []fileFP) *bytes.Buffer {
	cw := &cacheWriter{}
	cw.header(minWord, minCount, fp)
	keys := make([]string, 0, len(w.count))
	for word := range w.count {
		keys = append(keys, word)
	}
	sort.Strings(keys)
	cw.u32(uint32(len(keys)))
	for _, word := range keys {
		cw.str(word)
		cw.i64(w.count[word].count)
	}
	return finishCache(cw)
}

// readWordsCache rebuilds a Words model from a verified cache payload.
func readWordsCache(body []byte, minWord, minCount int, fp []fileFP) (*Words, bool) {
	r := &cacheReader{b: body, ok: true}
	if !r.header(minWord, minCount, fp) {
		return nil, false
	}
	n := int(r.u32())
	if n < 0 || n > len(r.b)-r.off { // every entry costs at least 12 bytes
		return nil, false
	}
	w := &Words{count: make(map[string]wordStat, n)}
	for i := 0; i < n; i++ {
		word := r.str()
		if !r.ok {
			return nil, false
		}
		st := wordStat{count: r.i64()}
		w.count[word] = st
		w.total += st.count
		if len(word) > w.maxLen {
			w.maxLen = len(word)
		}
	}
	if !r.ok || r.off != len(r.b) || w.total == 0 {
		return nil, false
	}
	total := float64(w.total + 1)
	for word, st := range w.count {
		st.logp = logProb(st.count, total)
		w.count[word] = st
	}
	return w, true
}

// bigramsCache serialises a Bigrams model in group order (group ids follow
// the sorted left word, so the file is deterministic).
func bigramsCache(b *Bigrams, minCount, maxLen int, fp []fileFP) *bytes.Buffer {
	cw := &cacheWriter{}
	cw.header(minCount, maxLen, fp)
	if b == nil {
		cw.u32(0)
		return finishCache(cw)
	}
	rev := make([]string, len(b.start))
	for w1, g := range b.index {
		rev[g] = w1
	}
	cw.u32(uint32(len(b.start)))
	for g, w1 := range rev {
		cw.str(w1)
		cw.u32(uint32(b.end[g] - b.start[g]))
		for _, p := range b.pairs[b.start[g]:b.end[g]] {
			cw.str(p.w2)
			cw.i64(p.c)
		}
	}
	return finishCache(cw)
}

// readBigramsCache rebuilds a Bigrams model from a verified cache payload.
// A payload with zero groups is the "sources present but nothing survived
// the filters" case and yields (nil, true).
func readBigramsCache(body []byte, minCount, maxLen int, fp []fileFP) (*Bigrams, bool) {
	r := &cacheReader{b: body, ok: true}
	if !r.header(minCount, maxLen, fp) {
		return nil, false
	}
	groups := int(r.u32())
	if !r.ok || groups < 0 || groups > len(r.b)-r.off {
		return nil, false
	}
	if groups == 0 {
		if r.off != len(r.b) {
			return nil, false
		}
		return nil, true
	}
	b := &Bigrams{index: make(map[string]int32, groups)}
	for g := 0; g < groups; g++ {
		w1 := r.str()
		n := int(r.u32())
		if !r.ok || n < 0 || n > len(r.b)-r.off {
			return nil, false
		}
		first := int32(len(b.pairs))
		var sum int64
		for i := 0; i < n; i++ {
			w2 := r.str()
			if !r.ok {
				return nil, false
			}
			c := r.i64()
			b.pairs = append(b.pairs, bpair{w2: w2, c: c})
			sum += c
		}
		b.index[w1] = int32(g)
		b.start = append(b.start, first)
		b.end = append(b.end, int32(len(b.pairs)))
		b.total = append(b.total, sum)
	}
	if !r.ok || r.off != len(r.b) || len(b.pairs) == 0 {
		return nil, false
	}
	return b, true
}
