# Changelog

All notable changes to `spell_splitter` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/). The repository has
no version-control history, so milestones are grouped by the date they were
completed. The legacy `old/` pipeline (Linux/aspell) is a separate module and
is not tracked here.

## [Unreleased]

### Added

- `TestGoldenListFull`: a golden-hash regression gate that runs the whole
  pipeline over the canonical input list `old/list.full` (80 047 lines →
  764 501 rows) with default flags and asserts the SHA-256 of the output
  (`952e99fe…`). It skips when `old/list.full` or `dicts/` are missing.
- Shipped dictionaries extended from the unused legacy sources:
  `dicts/en.txt` now also merges `old/corpus.txt` (117 280 → 559 936 words)
  and `dicts/en.bigrams` merges `old/1.txt` + `old/w3.txt`
  (249 794 → 1 105 638 pairs, `-min_count 3`; the pre-extension snapshot is
  kept as `old/en.bigrams.v1` so the rebuild recipe stays repeatable and the
  24 hand-cleaned `bk/bigrams.go` pairs survive — `bg1` alone regresses
  them). On `old/list.40k` this lifts the output from 380 074 to 382 501
  rows with the top-ranked alternative changed on 12.6 % of inputs (clear
  wins like `dishtvnetwork → dishtv network`, `cre8tivemedia →
  cre8tive media`; a few rank shifts in the other direction from the second
  corpus).
- Binary dictionary cache (`internal/dict/cache.go`): the loaders write
  `.words.cache` / `.bigrams.cache` next to the sources — source-file
  fingerprint (name/size/mtime), loader parameters and a CRC32 payload,
  installed atomically via temp + rename — and a warm start reads it instead
  of re-parsing. On the shipped models: startup ~2.0 s cold (first run also
  builds the ~30 MB cache) vs ~0.6 s warm; `old/list.40k` whole-process
  ~3.0 s → ~1.8 s. Any source, parameter, corruption or format-version
  change falls back to the text parse and rewrites the cache; cache writes
  are best-effort, so a read-only directory just never caches.
  `TestShippedDictsCacheRoundtrip` deep-compares warm vs cold on the real
  dictionaries and `TestGoldenListFull` passes both cold and warm.
- `tools/fetchdicts`: a standard-library tool that downloads public
  word-frequency lists (`-url`/`-in`, `.gz` supported), transliterates
  Cyrillic and Greek to ASCII (`-romanize`), calibrates counts
  (`-scale_total`) and writes `word<TAB>count` unigrams plus, from a sentence
  corpus, `w1<TAB>w2<TAB>count` bigrams directly into the dictionary
  directory.

### Fixed

- `ParseWordCount` swapped fields on `word<TAB>count` lines whose word is
  numeric: the old "first integer field is the count" rule read
  `1<TAB>70533` as `count=1 word=70533`, and summed 6509 numeric-token
  `old/corpus.txt` lines into wrapped-int64 garbage (digit `1` ended up with
  a count of 1.5e18). It is now word-first unless the second field is not
  numeric; the v1 dictionary sources contain no numeric words, so the
  pre-extension dictionaries rebuilt byte-identically either way.

### Changed

- Benchmarks (`BenchmarkProcessLine`, `BenchmarkSegmentOnly`) now sample
  `old/list.full` instead of `old/list.40k`, following the project decision
  to treat `list.full` as the canonical input list.

### Documentation

- `README.md` gains a shipped-models table, a supported-scripts table and an
  "Adding another language" walkthrough (fetch, romanize, `-scale_total`).
- `SOURCES.md` catalogues downloadable word-frequency lists and corpora
  (FrequencyWords, wordfreq, Leipzig, SUBTLEX, Google Books Ngram, OPUS,
  Tatoeba, Wikipedia, OSCAR, …), transliteration tools and the licenses that
  apply to derived `dicts/` files.

## [0.2.0] - 2026-10-12

Performance pass. Every change was verified byte-identical: `run -in
old/list.40k` still produces 380 074 rows with SHA-256
`DC1AE0C03F2C8CF6F89BEE4CAE08105565DE63F6C5B8C41F48AC53FD522CE52F`
(default flags).

### Changed

- `internal/dict`: `Bigrams` now stores pairs in one flat slice grouped by
  left word and sorted by right word, with an `index map[string]int32` mapping
  a left word to its range. `Cond` is a hash lookup plus a short binary search
  instead of nested maps.
- `internal/dict`: `LoadWords` streams counts directly into the model and
  `LoadBigrams` sorts records once instead of building an intermediate dedup
  map, cutting load-time allocations by roughly two thirds.
- `internal/dict`: `ParseWordCount`/`ParseBigram` use a new hand-rolled
  `atoiField` decimal parser instead of `strconv.Atoi`/`ParseInt`, so parsing
  no longer allocates an error value per non-numeric field. The `strconv`
  import is gone from the package.
- `internal/segment`: `pathHeap` and `comboHeap` are hand-rolled binary heaps
  instead of `container/heap`, which boxed every push and pop through `any`.
  The sift order matches `container/heap` exactly (root element excluded from
  the sift in `pop`).
- `internal/segment`: `Combine` no longer keeps a dead string-keyed `dup` map
  (two different index paths can never produce equal word sequences) and calls
  `keyOf` once per neighbor instead of twice.

### Performance

Measured on an Intel Xeon E5-2630 @ 2.30 GHz (Windows, go 1.27) with the
shipped `dicts/`; machine timings are noisy, so figures are minimums of
repeated runs:

| Metric | Before | After |
| --- | --- | --- |
| startup, warm dictionary cache | ~773 ms | ~283 ms |
| 40 000 lines, whole process | — | ~2.1 s |
| `BenchmarkProcessLine` (2000 lines) | ~143 ms | ~118 ms |
| `BenchmarkSegmentOnly` (2000 lines) | ~9.9 ms | ~8.4 ms |

### Fixed

- `comboHeap.pop` sifted the heap before removing the popped element during
  the heap conversion; caught by the golden-hash check and corrected to
  truncate first, matching `container/heap` semantics.

### Documentation

- `README.md` performance table updated for the new numbers.

## [0.1.0] - 2026-10-12

Initial pure-Go, cross-platform rewrite of the legacy `old/` spell-splitting
pipeline. Standard library only; builds and tests on Windows and Unix.

### Added

- `run` command (also the default when a leading `-` flag is given): reads
  domain-like glued tokens, emits every plausible human-spellable word form as
  TSV `input<TAB>spell<TAB>source`, where `source` is one of
  `asis|sep|split|partial`. Streams input through a worker pool while writing
  output in input order.
- `makedict` and `makebigrams` commands: merge `word [count]` / `w1 w2 count`
  sources with counts summed **before** `min_count`/`max_len` filtering, write
  through `path + ".tmp"` + rename so a failed run never clobbers an existing
  dictionary, and refuse to write an empty result.
- Package layout: `main` (CLI) plus `internal/text` (folding, rune classes),
  `internal/dict` (unigram/bigram loaders and parsers), `internal/segment`
  (separator forms, token search, candidate combination).
- Algorithm: separator-form cross-product (`Forms`, exhaustive up to 4
  separator runs), dictionary segmentation with admissible-heuristic pruning
  (`enumDict`), single-unknown-run partial paths (`partialPaths`), whole-line
  candidate re-scoring with bigrams (`Combine`). Scoring is
  `log P(w0) + Σ log P(wi|wi-1)` with unigram interpolation for missing
  bigrams and `UnknownPerRune * runes` for missing words.
- Guardrails: `-max_token_len` (tokens longer than 64 are never split),
  `-max_token`, `-max_nodes`, `-max_variants`, `-max_forms`.
- Shipped dictionaries in `dicts/`: `en.txt` (117 280 words, built from
  `old/un1` + `old/w`) and `en.bigrams` (249 794 pairs, built from
  `old/bk/bigrams.go` with `-min_count 3`).
- Tests next to each package (`*_test.go`) including `BenchmarkProcessLine`
  and `BenchmarkSegmentOnly`, plus `README.md`.

### Fixed

- Dictionary filtering happens after merging, so a word split across sources
  survives when its summed count qualifies.
- `makedict`/`makebigrams` use atomic temp-file writes; empty results exit 1
  without creating a file.
- `flag.ErrHelp` (`-h`/`help`) exits 0 instead of 1; output-file close errors
  propagate from `run`.
- Glued tokens with no separator forms fall back to the line itself as their
  only form, so every input line always yields at least one `asis` row.

## [0.0.1] - 2026-10-12

Legacy baseline preserved for reference: the `old/` module (Linux-only,
cgo/aspell) and its input lists (`old/list.40k`, `old/list.full`). Not part of
the buildable root module.
