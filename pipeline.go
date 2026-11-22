package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"

	"spell_splitter/internal/dict"
	"spell_splitter/internal/segment"
)

type counters struct {
	lines   int64
	rows    int64
	skipped int64
}

func runPipeline(in io.Reader, out io.Writer, seg *segment.Segmenter, cfg runConfig, workers int) (counters, error) {
	if workers < 1 {
		workers = 1
	}

	type job struct {
		n    int64
		line string
	}
	type result struct {
		n       int64
		rows    []row
		skipped bool
	}

	jobs := make(chan job, 1024)
	results := make(chan result, 1024)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				rows, skipped := processLine(j.line, seg, cfg)
				results <- result{n: j.n, rows: rows, skipped: skipped}
			}
		}()
	}

	w := bufio.NewWriterSize(out, 1<<16)
	done := make(chan counters, 1)
	writeErr := make(chan error, 1)
	go func() {
		var c counters
		var err error
		pending := make(map[int64]result)
		next := int64(0)
		var line []byte
		flush := func() {
			if err != nil {
				return
			}
			if ferr := w.Flush(); ferr != nil {
				err = ferr
			}
		}
		for res := range results {
			pending[res.n] = res
			for {
				r, ok := pending[next]
				if !ok {
					break
				}
				delete(pending, next)
				next++
				c.lines++
				if r.skipped {
					c.skipped++
				}
				if err != nil {
					// Keep draining results after a write error: the workers
					// must never block on a full results channel just because
					// the writer stopped consuming.
					continue
				}
				for _, row := range r.rows {
					line = appendRow(line[:0], row, cfg.score)
					if _, werr := w.Write(line); werr != nil {
						err = werr
						break
					}
					c.rows++
				}
				if err != nil {
					continue
				}
				if c.lines%100000 == 0 {
					fmt.Fprintf(os.Stderr, "spell_splitter: %d lines\n", c.lines)
					flush()
				}
			}
		}
		flush()
		done <- c
		writeErr <- err
	}()

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), dict.ScanBuffer)
	var n int64
	for sc.Scan() {
		n++
		jobs <- job{n: n - 1, line: sc.Text()}
	}
	scanErr := sc.Err()
	close(jobs)
	wg.Wait()
	close(results)
	c := <-done
	if err := <-writeErr; err != nil {
		return c, fmt.Errorf("writing output: %w", err)
	}
	if scanErr != nil {
		return c, fmt.Errorf("reading input: %w", scanErr)
	}
	return c, nil
}

// appendRow renders one TSV row into dst. The optional quality and confidence
// columns are appended when -score is set.
func appendRow(dst []byte, r row, withScore bool) []byte {
	dst = append(dst, r.in...)
	dst = append(dst, '\t')
	dst = append(dst, r.spell...)
	dst = append(dst, '\t')
	dst = append(dst, r.src...)
	if withScore {
		dst = append(dst, '\t')
		dst = strconv.AppendFloat(dst, r.quality, 'f', 3, 64)
		dst = append(dst, '\t')
		dst = strconv.AppendFloat(dst, r.confidence, 'f', 4, 64)
	}
	return append(dst, '\n')
}
