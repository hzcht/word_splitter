package main

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"spell_splitter/internal/dict"
	"spell_splitter/internal/segment"
)

// loadBenchSeg loads the shipped dictionaries and the first 2000 lines of the
// canonical input list (old/list.full). Both benchmarks skip when the data is
// missing.
func loadBenchSeg(b *testing.B) (*segment.Segmenter, []string) {
	b.Helper()
	uni, err := dict.LoadWords("dicts", 1, 1)
	if err != nil {
		b.Skipf("dicts: %v", err)
	}
	bi, err := dict.LoadBigrams("dicts", 1, 40)
	if err != nil {
		b.Skipf("bigrams: %v", err)
	}
	f, err := os.Open("old/list.full")
	if err != nil {
		b.Skipf("list: %v", err)
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), dict.ScanBuffer)
	for sc.Scan() && len(lines) < 2000 {
		lines = append(lines, sc.Text())
	}
	seg := segment.New(uni, bi, segment.DefaultOptions())
	return seg, lines
}

func BenchmarkProcessLine(b *testing.B) {
	seg, lines := loadBenchSeg(b)
	cfg := defaultRunConfig()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, l := range lines {
			processLine(l, seg, cfg)
		}
	}
}

func BenchmarkSegmentOnly(b *testing.B) {
	seg, lines := loadBenchSeg(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, l := range lines {
			for _, f := range segment.Forms(l, 8) {
				for _, t := range strings.Fields(f) {
					seg.Segment(t)
				}
			}
		}
	}
}
