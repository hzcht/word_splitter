# Dictionary sources

`spell_splitter` reads two kinds of model file from the dictionary directory
(`dicts/` by default):

- `*.txt` — `word [count]`, merged and summed at load time (`LoadWords`);
- `*.bigrams` — `w1<TAB>w2<TAB>count`, merged and summed (`LoadBigrams`).

Words are folded to lowercase with Latin diacritics mapped to ASCII, and only
`[a-z0-9]` words take part in splitting. Sources in a non-Latin script must be
**transliterated to ASCII first**; `tools/fetchdicts` does that for Cyrillic and
Greek and normalizes everything else.

Counts from all `*.txt` files are summed into one total, so a source with far
larger raw counts dominates the model. Use `fetchdicts -scale_total` to rescale
a language to a chosen mass before adding it (see the README).

The list below is a starting catalogue, not an endorsement. **Check each
source's license before shipping a derived `dicts/` file** — see
[Licensing and attribution](#licensing-and-attribution).

## Ready-to-load frequency lists (word + count)

| Source | Format | License | Notes |
| --- | --- | --- | --- |
| [FrequencyWords](https://github.com/hermitdave/FrequencyWords) | `word count` | content CC-BY-SA-4.0, code MIT | 60+ languages from OpenSubtitles 2016/2018. Native scripts. Easiest source. |
| [wordfreq](https://github.com/rspeer/wordfreq) | Python API (Zipf) | code Apache-2.0, data CC-BY-SA-4.0 | 40+ languages, consistent Zipf scale; transliterates Serbian automatically. Export via a script. |
| [Leipzig Corpora Collection](https://wortschatz.uni-leipzig.de/en/download) | `word freq` | varies (CC-BY / CC-BY-NC) | 250+ languages, plus sentence corpora for bigrams. Check the license per corpus. |
| [SUBTLEX](http://crr.ugent.be/programs-data/subtitle-frequencies) | per-language | free with attribution | Subtitle frequencies: US, UK, DE, NL, CH, ES, FR, … |
| [Wiktionary frequency lists](https://en.wiktionary.org/wiki/Wiktionary:Frequency_lists) | varies | CC-BY-SA-4.0 / GFDL | Russian, Ukrainian, Greek and others; also romanization tables. |
| [Google Books Ngram](https://storage.googleapis.com/books/ngrams/books/datasetsv3.html) | n-gram counts | free use with attribution | Per language unigrams **and bigrams**; very large, sample it. |

### FrequencyWords

```
https://raw.githubusercontent.com/hermitdave/FrequencyWords/master/content/2018/<code>/<code>_50k.txt
https://raw.githubusercontent.com/hermitdave/FrequencyWords/master/content/2018/<code>/<code>_full.txt
```

`<code>` is the ISO 639-1/2 code (`ru`, `uk`, `de`, `fr`, `el`, `ar`, …).
`_50k` is ~1 MB, `_full` can be tens of MB. Example (`ru`):

```
go run ./tools/fetchdicts -lang ru -romanize cyrillic \
    -url https://raw.githubusercontent.com/hermitdave/FrequencyWords/master/content/2018/ru/ru_50k.txt \
    -min_count 2 -scale_total 12000000 -out dicts/ru.txt
```

For a Latin-script language drop `-romanize` (diacritics are folded for you):

```
go run ./tools/fetchdicts -lang de \
    -url https://raw.githubusercontent.com/hermitdave/FrequencyWords/master/content/2018/de/de_50k.txt \
    -scale_total 12000000 -out dicts/de.txt
```

### wordfreq

```
pip install wordfreq
```

`wordfreq` refuses a CSV dump (attribution/ShareAlike), but you may query it
and emit a `word count` file, e.g. convert the Zipf value back to a count:

```python
from wordfreq import top_n_list, zipf_frequency
lang, n = "ru", 50000
with open(f"{lang}.txt", "w", encoding="utf-8") as f:
    for w in top_n_list(lang, n):
        f.write(f"{w}\t{zipf_frequency(w, lang, wordlist='large')}\n")
```

Then feed the file to `fetchdicts -romanize cyrillic` (for Cyrillic) or to
`makedict`. Cite wordfreq and its upstream sources.

### wordfreq via Go (no Python)

If you already have a `word count` export (from any of the sources above),
`tools/fetchdicts` reads it directly with `-in`.

## Corpora (for building bigrams)

A bigram model is what makes splits rank well; feed a sentence corpus (one
sentence/doc per line) to `fetchdicts -corpus`, which counts adjacent words:

```
go run ./tools/fetchdicts -lang ru -romanize cyrillic \
    -corpus ru.sentences.txt -min_bigram_count 3
```

| Source | URL | License |
| --- | --- | --- |
| OPUS (OpenSubtitles, and more) | https://opus.nlpl.eu/ | varies per corpus, mostly permissive |
| Tatoeba sentences | https://tatoeba.org/en/downloads | CC-BY-2.0-FR |
| Wikipedia dumps | https://dumps.wikimedia.org/ | CC-BY-SA-4.0 |
| Leipzig sentence corpora | https://wortschatz.uni-leipzig.de/en/download | varies |
| OSCAR | https://oscar-corpus.com/ | CC-BY-4.0 (per-crawl terms) |
| mC4 / multilingual C4 | https://huggingface.co/datasets/mc4 | ODC-BY / Common Crawl terms |
| Google Books Ngram (2-grams) | https://storage.googleapis.com/books/ngrams/books/datasetsv3.html | free with attribution |

Bigram counts are used relative to the same left word, so their absolute scale
does not have to match the unigrams; but a much larger corpus makes bigrams
dominate the interpolation more strongly (the smoothing constant `K` is in the
same count units).

## Transliteration

| Tool | Output | Notes |
| --- | --- | --- |
| `fetchdicts -romanize cyrillic` | ASCII | Simplified BGN/PCGN for Russian/Ukrainian/Belarusian/Serbian/Bulgarian. |
| `fetchdicts -romanize greek` | ASCII | Single letters + common digraphs; approximate. |
| [ICU / PyICU](https://unicode-org.github.io/icu/) `Transliterator` | any | `Russian-Latin/BGN`, `Greek-Latin`, `Any-Latin`; highest quality. |
| [Unidecode](https://pypi.org/project/Unidecode/) | ASCII | Broad script coverage, reversible-ish, no dependencies. |
| [Apertium](https://wiki.apertium.org/) `apertium-*-latn` | Latin | Rule-based romanization modules. |
| [Wikipedia romanization tables](https://en.wikipedia.org/wiki/Romanization) | tables | ISO 9 / GOST 7.79 / BGN/PCGN / ELOT 743. |

Transliteration is ambiguous (`х` → `kh`/`h`/`x`, `я` → `ya`/`ia`): different
domain owners spell the same word differently. If you need variants, generate
several romanizations and add them all, then watch that the extra entries do
not outvote the real ones.

## Licensing and attribution

Shipped `dicts/` files derived from these sources are **derivative works** and
inherit their obligations:

- **CC-BY-SA-4.0** (FrequencyWords, wordfreq, Wikipedia/Wiktionary): credit the
  source, link the license, and keep derived data under the same license.
- **SUBTLEX**: credit the SUBTLEX authors and state that SUBTLEX is free data.
- **Google Books Ngram**: free for any use; acknowledge the Google Books Ngram
  Viewer and link http://books.google.com/ngrams.
- **Leipzig / OSCAR / OPUS**: check each corpus; some are non-commercial.

Keep a `NOTICE`/attribution file next to the dictionaries listing, per file,
the source URL, the license and the transformation applied
(`fetchdicts -romanize … -scale_total …`). `wordfreq`'s README is a good model
for how detailed this should be.
