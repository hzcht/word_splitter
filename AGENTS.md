# AGENTS.md — spell_splitter

Pure-Go, stdlib-only splitter of glued domain/keyword "spells" into word forms.
`go.mod`: `module spell_splitter`, `go 1.27.0`. `README.md` is the authoritative
flag/format reference — read it before changing behavior.

## Verify

- `go build -o spell_splitter .` · `go test ./...` · `go vet ./...` · `gofmt -l .`
  (must print nothing outside `old/`).
- Output regression gate: `go test -run TestGoldenListFull .` (root package,
  hash constant in `run_test.go`). Skips when `old/list.full` or `dicts/` is absent.
  Rebaseline deliberately: compare `go run . run -in old/list.full -max_variants 20 -out out_new.tsv`
  against the old dump first, then change the SHA-256 and say why in the test comment.
  The gate uses `defaultRunConfig()` (max_variants 20), not the CLI default of 8.
- Benchmarks: `go test -bench Benchmark -benchmem ./...` (skip without `dicts/` +
  `old/list.full`).
- `go run . run -in -` reads stdin; `-in` is required. A leading `-` flag also
  dispatches to `run`.

## Where things live

- `main.go` CLI dispatch; `run.go` worker pool + per-line pipeline + `source`
  labels; `dictcmd.go` `makedict`/`makebigrams` with atomic temp+rename writes.
- `internal/text` (folding, rune classes) · `internal/dict` (loaders, parsers,
  scoring) · `internal/segment` (separator forms, token search, combination).
- Output-quality changes: `internal/segment/forms.go` and `segment.go`.
  Vocabulary/scoring: `dicts/` and `internal/dict/`.

## Traps

- `old/` is a separate nested module (go 1.21), excluded from root `./...`, and
  does not build here (cgo/aspell, Linux-only constraints). `cd old` before any Go
  command there. Its scripts have destructive side effects (live DB inserts,
  `dig`, append-only outputs, prebuilt Linux ELF) — reference only, never run
  casually; if you do, use a fresh output filename.
- Changing dictionary parsing/folding/filtering or model layout requires bumping
  `cacheVersion` in `internal/dict/cache.go`; warm `.words.cache` / `.bigrams.cache`
  otherwise keep serving old semantics. Caches are gitignored and machine-local
  (fingerprints record mtimes); delete them to force a cold parse.
- Rebuilding shipped dicts: use `old/en.bigrams.v1` (24 hand-cleaned pairs), not
  the current `dicts/en.bigrams` — merging sums counts and would double every pair.
  Never pass `old/1.txt` and `old/w2_.txt` together (same data). `ParseWordCount`
  is word-first on `word<TAB>count` so numeric tokens keep their real counts.
- Only `[a-z0-9]` words are splittable; other scripts are dropped by the loader and
  must be romanized first (`tools/fetchdicts -romanize`). Diacritics fold to ASCII.
- `run` uses workers but writes in input order — output must stay deterministic.
  Every input line contributes exactly one `asis` row; fewer means skipped lines
  (missing/blank `-col`), reported on stderr as `N lines skipped`.
- `tools/fetchdicts` writes into `dicts/`; use `-scale_total` so a new source does
  not swamp the English mass (all `*.txt` counts are summed into one total).
  Adding a source creates attribution duties — keep a NOTICE next to `dicts/`
  (see `SOURCES.md`).
