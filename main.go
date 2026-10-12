// Command spell_splitter turns glued domain-like spell strings into the
// human-spellable word forms they stand for.
//
//	spell_splitter run -in spells.txt -out spells.out
//	spell_splitter makedict -out dicts/en.txt -src corpus.txt
//	spell_splitter makebigrams -out dicts/en.bigrams -src bigrams.txt
//
// Dictionaries live in the dicts directory: "*.txt" files hold
// "word [count]" lists (any language, merged at load time) and
// "*.bigrams" files hold "w1<TAB>w2<TAB>count" lists.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	err := dispatch(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "spell_splitter: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: spell_splitter <command> [flags]

commands:
  run           split spells into word forms (default command)
  makedict      build a "word [count]" dictionary
  makebigrams   build a "w1 w2 count" bigram dictionary

run flags:
  -in           input file with spells (required, "-" for stdin)
  -out          output file (default stdout)
  -dicts        dictionary directory (default "dicts")
  -col          1-based input column holding the spell (default 1)
  -max_variants max spells per input line (default 20)
  -max_forms    max separator forms per input line (default 8)
  -max_token    max candidate splits per token (default 8)
  -max_nodes    max search steps per token (default 20000)
  -max_unknown  max runes of an unknown run inside a token (default 12)
  -max_token_len tokens longer than this are not split (default 64)
  -min_word     shortest dictionary word considered (default 1)
  -max_len      longest dictionary word considered (default 40)
  -min_count    drop dictionary words rarer than this (default 1)
  -workers      parallel workers (default NumCPU)

output columns: input, spell, source (asis, sep, split, partial)
`)
}

func dispatch(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("no command given")
	}
	switch cmd := args[0]; cmd {
	case "run":
		return cmdRun(args[1:])
	case "makedict":
		return cmdMakeDict(args[1:])
	case "makebigrams":
		return cmdMakeBigrams(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		// A leading flag means the implicit "run" command.
		if strings.HasPrefix(cmd, "-") {
			return cmdRun(args)
		}
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}
