package dict

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWordsCacheWarmEqualsCold(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "alpha\t2\nbeta\t5\n")
	write(t, filepath.Join(dir, "b.txt"), "gamma\t9\n")

	cold, err := LoadWords(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, wordsCacheFile)); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	warm, err := LoadWords(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, warm) {
		t.Error("warm cache load differs from the cold text parse")
	}
}

func TestWordsCacheHitOnUnchangedSources(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	write(t, a, "alpha\t2\n")

	cold, err := LoadWords(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Swap in a different word of the same byte length and restore mtime, so
	// only a real cache hit can still return the original model.
	st, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	write(t, a, "gamma\t2\n")
	if err := os.Chtimes(a, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}

	warm, err := LoadWords(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := warm.Lookup("gamma"); ok {
		t.Error("sources changed on disk but the cache answered: fingerprint missed the edit")
	}
	if !reflect.DeepEqual(cold, warm) {
		t.Error("cache hit returned a different model than the original parse")
	}
}

func TestWordsCacheInvalidatedBySourceAndParams(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	write(t, a, "alpha\t2\nbeta\t5\n")
	if _, err := LoadWords(dir, 1, 1); err != nil {
		t.Fatal(err)
	}

	// Different loader parameters must not be served by the old cache: with
	// minCount=6 nothing survives and the load must fail, not return the
	// minCount=1 model.
	if _, err := LoadWords(dir, 1, 6); err == nil {
		t.Error("expected an error for minCount=6, got the cached minCount=1 model")
	}

	// A changed source must invalidate the fingerprint.
	write(t, a, "alpha\t2\nbeta\t5\ndelta\t7\n")
	w, err := LoadWords(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.Lookup("delta"); !ok {
		t.Error("source grew but the stale cache was served")
	}
}

func TestWordsCacheCorruptFallsBack(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "alpha\t2\nbeta\t5\n")
	cold, err := LoadWords(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	cpath := filepath.Join(dir, wordsCacheFile)
	data, err := os.ReadFile(cpath)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xFF
	if err := os.WriteFile(cpath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadWords(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, got) {
		t.Error("corrupted cache was not ignored in favour of the text parse")
	}
}

func TestBigramsCacheWarmEqualsCold(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bigrams")
	write(t, a, "aa\tbb\t5\naa\tcc\t2\n")
	write(t, filepath.Join(dir, "b.bigrams"), "dd\tee\t7\n")

	cold, err := LoadBigrams(dir, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	if cold == nil {
		t.Fatal("expected bigrams")
	}
	if _, err := os.Stat(filepath.Join(dir, bigramsCacheFile)); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	warm, err := LoadBigrams(dir, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, warm) {
		t.Error("warm cache load differs from the cold text parse")
	}

	// Corrupted payload must fall back to the text parse (which also
	// rewrites a clean cache).
	cpath := filepath.Join(dir, bigramsCacheFile)
	data, err := os.ReadFile(cpath)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xFF
	if err := os.WriteFile(cpath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fb, err := LoadBigrams(dir, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, fb) {
		t.Error("corrupted cache was not ignored in favour of the text parse")
	}

	// Same-size swap + restored mtime: only a cache hit can still know aa.
	st, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	write(t, a, "zz\tbb\t5\nzz\tcc\t2\n")
	if err := os.Chtimes(a, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	hit, err := LoadBigrams(dir, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hit.index["zz"]; ok {
		t.Error("sources changed on disk but the cache answered: fingerprint missed the edit")
	}
	if !reflect.DeepEqual(cold, hit) {
		t.Error("cache hit returned a different model than the original parse")
	}
}

// TestShippedDictsCacheRoundtrip proves on the real models that a warm cache
// load is identical to a cold parse of dicts/ (it runs both paths back to
// back and deep-compares). It skips when the shipped directory is missing.
func TestShippedDictsCacheRoundtrip(t *testing.T) {
	dir := filepath.Join("..", "..", "dicts")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("dicts: %v", err)
	}
	// Drop any cache written by other tests so the first load here is cold.
	os.Remove(filepath.Join(dir, wordsCacheFile))
	os.Remove(filepath.Join(dir, bigramsCacheFile))

	coldW, err := LoadWords(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	warmW, err := LoadWords(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(coldW, warmW) {
		t.Error("shipped words: warm cache load differs from the cold parse")
	}

	coldB, err := LoadBigrams(dir, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	warmB, err := LoadBigrams(dir, 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(coldB, warmB) {
		t.Error("shipped bigrams: warm cache load differs from the cold parse")
	}
}
