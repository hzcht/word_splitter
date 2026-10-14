# spell_splitter

Turns glued domain/keyword "spells" (`newyork`, `ku-ra`, `yourbis`, `0-apr-credit-card`)
into the human-spellable word forms they stand for, using word-frequency
models:

```text
$ echo -e "newyork\nku-ra" | go run . run -in - -max_variants 4
newyork        newyork       asis
newyork        new york      split
newyork        new yor k     split
newyork        ne w york     split
ku-ra          ku-ra         asis
ku-ra          kura          sep
ku-ra          kur a         split
ku-ra          ku ra         sep
```

The replacement for the legacy Linux/aspell pipeline in `old/` (see
[Legacy pipeline](#legacy-pipeline-old)). Pure Go, standard library only,
runs on Windows, macOS and Linux.

## Build and test

```sh
go build -o spell_splitter .
go test ./...
go vet ./...
gofmt -l .            # must print nothing outside old/
```

## Commands

| Command | Purpose |
| --- | --- |
| `run` | split spells into word forms (the default command; a leading `-` flag also dispatches to `run`) |
| `makedict` | build a `word [count]` dictionary from word lists |
| `makebigrams` | build a `w1<TAB>w2<TAB>count` dictionary from word-pair lists |

Run `go run . help` for the same summary the binary prints.

### run

```sh
spell_splitter run -in spells.txt -out spells.tsv
```

`-in` is required (`-` reads stdin), `-out` writes to stdout by default.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-in` | – | input file, one spell per column (`-` for stdin) |
| `-out` | stdout | output file |
| `-dicts` | `dicts` | dictionary directory |
| `-col` | `1` | 1-based input column holding the spell |
| `-max_variants` | `20` | max output rows per input line, including the `asis` row |
| `-max_forms` | `8` | max separator forms per input line |
| `-max_token` | `8` | max candidate splits per token |
| `-max_nodes` | `20000` | max search steps per token |
| `-max_unknown` | `12` | max runes of an unknown run inside a token |
| `-max_token_len` | `64` | tokens longer than this are never split |
| `-min_word` | `1` | shortest dictionary word considered (raise it to drop single-letter noise) |
| `-max_len` | `40` | longest dictionary word considered |
| `-min_count` | `1` | drop dictionary words rarer than this |
| `-workers` | `NumCPU` | parallel workers |

### Output format

Tab-separated, one row per candidate spelling:

```text
input<TAB>spell<TAB>source
```

| `source` | Meaning |
| --- | --- |
| `asis` | the input as it came in (always the first row) |
| `sep` | separators normalized only — no token was split |
| `split` | tokens were split and every resulting word is in the dictionary |
| `partial` | tokens were split and at least one resulting word is unknown |

Every input line contributes exactly one `asis` row, so an output with fewer
`asis` rows than input lines means some lines were skipped (reported on
stderr as `N lines skipped` — a line is skipped when `-col` does not exist
or the column is blank).

## Dictionaries

Models live in one directory (`dicts/` by default):

| Pattern | Format |
| --- | --- |
| `*.txt` | `word [count]`, one per line; the count field may be absent (`1`) and may come first when the word field is not numeric (`12 word`); with two numeric fields the first one is the word (`1<TAB>70533` = digit token, count 70533) |
| `*.bigrams` | `w1<TAB>w2<TAB>count`; also accepts `w1 w2 count`, `count w1 w2` and `w1 w2` (count `1`) |

Rules applied at load time:

- Words are folded to lowercase with Latin diacritics mapped to ASCII
  (`Zürich` → `zurich`, `naïve` → `naive`); non-Latin scripts are kept as-is
  but are never splittable.
- Only `[a-z0-9]` words take part in splitting; anything else is ignored.
- All `*.txt` files are **merged before** `min_count` filtering, so a word
  spread over several sources survives when the summed count does. That is
  why the vocabulary deliberately mixes languages: a spell may combine words
  from different files.
- All `*.bigrams` files are merged the same way (counts summed), then
  filtered by `-min_count` / `-max_len`.
- `LoadBigrams` returns `nil` when the directory has no `*.bigrams` file;
  scoring then falls back to unigrams only.

### Shipped models

| File | Contents |
| --- | --- |
| `dicts/en.txt` | 559 936 Latin-script words (English plus other Latin-script languages), folded to ASCII |
| `dicts/en.bigrams` | 1 105 638 word pairs |

The vocabulary deliberately mixes languages, so a spell may combine words from
different files.

### Supported scripts

| Script | How it is handled |
| --- | --- |
| Latin, with diacritics | folded to ASCII automatically (`Zürich` → `zurich`, `naïve` → `naive`) |
| Cyrillic, Greek | load after romanization, e.g. `tools/fetchdicts -romanize cyrillic` |
| Arabic, Hebrew, CJK, … | not built in; transliterate to `[a-z0-9]` before adding |

A source may contain non-Latin runes, but the loader drops them: only
`[a-z0-9]` words take part in splitting (`text.IsSplittable`). Dictionaries for
non-Latin languages must therefore be romanized first — the loader never
segments other scripts, and input spells are folded the same way.

### Warm cache

The loaders keep a binary cache next to the sources — `.words.cache` and
`.bigrams.cache` in the model directory (~30 MB for the shipped pair). A warm
start reads it instead of re-parsing the text files. The cache records a
fingerprint of every source file (name, size, mtime), the loader parameters
and a CRC32 of the payload, so editing a source, changing a flag, a corrupted
file or a format bump all fall back to the text parse and rewrite the cache.
Writes are best-effort: a read-only directory simply never gets a cache.
Delete the cache files to force a cold rebuild.

### Building dictionaries

```sh
go run . makedict -out dicts/en.txt -src words.txt -src more_words.txt
go run . makebigrams -out dicts/en.bigrams -src pairs.txt -min_count 3
```

Both builders write through `path + ".tmp"` and rename on success, so a
failed run never clobbers an existing dictionary, and they refuse to write an
empty result.

To rebuild the shipped dictionaries:

```sh
go run . makedict -out dicts/en.txt -src old/un1 -src old/w -src old/corpus.txt
go run . makebigrams -out dicts/en.bigrams -src old/en.bigrams.v1 -src old/1.txt -src old/w3.txt -min_count 3
```

`old/en.bigrams.v1` is the pre-extension bigram snapshot: it holds the 24
hand-cleaned pairs from the lost `old/bk/bigrams.go` extraction, which a
plain `-src old/bg1` rebuild would drop (and gain three typo variants
instead). Do not feed the current `dicts/en.bigrams` back as a source —
merging sums counts, so a rerun would double every pair. Never pass
`old/1.txt` and `old/w2_.txt` together — they are the same data.
`ParseWordCount` reads `word<TAB>count` lines word-first, so digit
tokens like `1<TAB>70533` keep their real counts (an older "first integer is
the count" rule summed 6509 numeric-token corpus lines into wrapped-int64
garbage).

### Adding another language

`tools/fetchdicts` downloads public word-frequency lists, transliterates
non-Latin scripts to ASCII and calibrates counts, then writes a ready
`dicts/<lang>.txt` (and, from a corpus, `dicts/<lang>.bigrams`):

```sh
go run ./tools/fetchdicts -lang ru -romanize cyrillic \
    -url https://raw.githubusercontent.com/hermitdave/FrequencyWords/master/content/2018/ru/ru_50k.txt \
    -min_count 2 -scale_total 12000000 -out dicts/ru.txt
```

`-scale_total` rescales the new file's counts so its mass matches a target.
Because every `*.txt` count is summed into one total, an unscaled source with
large raw counts would swamp the existing English model; a good starting point
is to give each language a mass comparable to (or a fraction of) the English
file's total. The loader prints `N words` on start — watch it move.

For a Latin-script language, drop `-romanize`; diacritics are folded for you:

```sh
go run ./tools/fetchdicts -lang de \
    -url https://raw.githubusercontent.com/hermitdave/FrequencyWords/master/content/2018/de/de_50k.txt \
    -scale_total 12000000 -out dicts/de.txt
```

For bigrams, pass a sentence corpus (one sentence per line):

```sh
go run ./tools/fetchdicts -lang ru -romanize cyrillic \
    -corpus ru.sentences.txt -min_bigram_count 3
```

Where to get the data (FrequencyWords, wordfreq, Leipzig, SUBTLEX, Google
Books Ngram, OPUS, Tatoeba, Wikipedia, OSCAR, …), which transliteration tools
exist, and which licenses apply is catalogued in
[SOURCES.md](SOURCES.md).

### Sources and attribution

The shipped models are derived from external frequency data. See
[SOURCES.md](SOURCES.md) for the dataset list and the CC-BY-SA / attribution
obligations that apply when a derived `dicts/` is redistributed; keep a NOTICE
file next to `dicts/` when you add a source.

## How it works

For each input line:

1. **Column selection.** The line is split on tabs and the `-col`-th field is
   trimmed; other columns are not part of the output.
2. **Separator forms** (`internal/segment/forms.go`). A *separator run* is any
   maximal run of runes that is not a letter, digit or combining mark
   (`-`, `_`, `.`, spaces, …). Every run is either replaced by a space or
   dropped, producing the cross-product of "keep" vs "drop". This is
   exhaustive for up to 4 runs; above that only the fully exploded form, the
   glued form and the single-removal forms are kept. The fully exploded form
   always comes first and the list is capped by `-max_forms`. A spell without
   separators yields no forms, so the line itself is used as its only form.
3. **Token segmentation** (`internal/segment/segment.go`). Each token is
   folded, then split into words:
   - a *whole-token* candidate is always kept (never marked as `split`);
   - tokens longer than `-max_token_len`, or containing non `[a-z0-9]`
     runes, are not split at all;
   - `enumDict` enumerates the best dictionary-only segmentations, pruning
     branches that cannot reach the current k-th best score and spending at
     most `-max_nodes` steps;
   - when that yields fewer than `-max_token` candidates, `partialPaths`
     adds segmentations carrying exactly one unknown run of at most
     `-max_unknown` runes, with dictionary words on both sides (this is what
     produces `partial` rows such as `0 apr credit card`).
4. **Combination and re-scoring.** Per-token candidates are combined best-first
   by their additive scores (a pool of the best combinations is kept), then
   every combination is re-scored as a whole line so that bigrams spanning
   token boundaries count. Results are sorted by score, de-duplicated and
   capped by `-max_variants`.

### Scoring

```text
score(w0..wn) = log P(w0) + Σ log P(wi | wi-1)
```

- `P(w) = (c(w) + 1) / (N + 1)` with `c(w)` the corpus count and `N` the sum
  of all counts.
- `P(wi | wi-1) = (c(wi-1, wi) + K·P(wi)) / (c(wi-1) + K)` with `K = 5`
  (interpolated bigram); a missing pair therefore degrades to the unigram
  probability of `wi` rather than to zero.
- A word missing from the dictionary scores `UnknownPerRune` per rune with
  `UnknownPerRune = ln(0.01)`, and is never charged a unigram on top of that.

This keeps whole-token, all-dictionary and mixed candidates comparable on one
scale, which is what makes `your bis` rank above `you rbis`.

### Guardrails

| Limit | Flag | Purpose |
| --- | --- | --- |
| token length | `-max_token_len` | prevents exponential search on long glued strings |
| candidates per token | `-max_token` | bounds the per-token candidate list |
| search steps per token | `-max_nodes` | hard budget for `enumDict` |
| unknown run length | `-max_unknown` | bounds the single unknown run in partial paths |
| forms per line | `-max_forms` | bounds the separator cross-product |
| rows per line | `-max_variants` | bounds the output |

`run` streams input through a worker pool but writes output in input order, so
the TSV is deterministic for a given input, dictionary and flag set.

## Performance

Measured on an Intel Xeon E5-2630 @ 2.30 GHz (Windows, go 1.27) with the
shipped `dicts/` (559 936 words, 1 105 638 bigrams) and `old/list.40k`
(40 000 spells):

| Step | Time |
| --- | --- |
| startup, warm dictionary cache | ~0.6 s |
| startup, cold text parse (first run, builds the cache) | ~2.0 s |
| 40 000 lines → 382 501 rows, whole process (warm) | ~1.8 s (~22 k lines/s, ~212 k rows/s) |
| single-threaded `processLine` (2000-line `old/list.full` sample) | ~75 µs/line (~20 KB, ~171 allocations) |
| …of which pure token segmentation | ~16 µs/line |

Most of the per-line CPU is the separator cross-product, candidate
combination and de-duplication rather than the token search itself; the
allocation rate drives a visible GC share, so `-max_variants` and
`-max_forms` are the cheapest knobs when throughput matters.

Reproduce with:

```sh
go test -bench Benchmark -benchmem ./...
```

Both benchmarks load `dicts/` and `old/list.full` and skip when those are
missing.

## Development

- Tests live next to each package (`*_test.go`), cover folding, the
  dictionary loaders (including the merge-before-filter rule), segmentation,
  separator forms, combination, the `run` pipeline (ordered output, skip
  counting, write-error propagation) and a golden-hash regression gate over
  the canonical input list `old/list.full` (80 047 lines → 764 501 rows,
  SHA-256 `952e99fe…`). They pass on Windows.
- `internal/segment/forms.go` and `internal/segment/segment.go` are the
  packages to touch for output-quality changes; `dicts/` and `internal/dict/`
  for vocabulary/scoring changes.
- After changing a loader or a parser, run the golden test; if the change is
  output-affecting, update the expected hash deliberately. When changing how
  dictionaries are parsed or filtered, also bump `cacheVersion` in
  `internal/dict/cache.go` — otherwise a warm `.words.cache` would keep
  serving results from the old semantics:

  ```sh
  go test -run TestGoldenListFull .
  # or compare full dumps by hand:
  go run . run -in old/list.full -out out_new.tsv
  # compare against the previous out.tsv
  ```

## Layout

```text
main.go          CLI dispatch, usage, flag handling
run.go           run command: worker pool, per-line pipeline, source labels
dictcmd.go       makedict / makebigrams, atomic dictionary writes
internal/text    folding and word-rune classification
internal/dict    unigram/bigram loaders and parsers
internal/segment token search, separator forms, candidate combination
dicts/           shipped word and bigram models
tools/fetchdicts download/transliterate external lists into dicts/
SOURCES.md       external dataset catalogue and licenses
bench_test.go    end-to-end benchmarks
```

## Legacy pipeline (`old/`)

`old/` is the previous iteration and is kept as a reference only:

- separate nested module (`module spell`, go 1.21) — `cd old` before any Go
  command; excluded from the root module's `./...` patterns;
- does not build here: `github.com/trustmaster/go-aspell` is cgo with
  Linux-only build constraints;
- `old/main.go` writes TSV from four engines (ngram, Norvig/viterbi,
  aspell suggestions, language tags) with `-in_file` / `-out_file`;
- `old/spell` is a prebuilt Linux x86-64 ELF and will not run on Windows;
- its output files are opened with `O_APPEND` and never truncated, and ~400
  goroutines append concurrently — rerunning against an existing output
  duplicates and interleaves rows, so use a fresh filename or delete first.

Some scripts and generated artifacts in `old/` have destructive side effects —
live database inserts, `dig` queries, append-only outputs — and must not be run
casually; `process.sh`, `digg.sh` and the Ruby helpers are reference-only.
