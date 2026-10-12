// Package text provides Latin-script folding helpers shared by the
// dictionary and segmentation packages.
package text

import (
	"strings"
	"unicode"
)

// foldMap maps lowercased Latin letters carrying diacritics to their ASCII
// equivalents.
var foldMap = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'æ': "ae",
	'ć': "c", 'ĉ': "c", 'ċ': "c", 'č': "c", 'ç': "c",
	'ď': "d", 'đ': "d",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ĕ': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'ĝ': "g", 'ğ': "g", 'ġ': "g", 'ģ': "g",
	'ĥ': "h", 'ħ': "h",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ĩ': "i", 'ī': "i", 'ĭ': "i", 'į': "i", 'ı': "i",
	'ĵ': "j",
	'ķ': "k",
	'ĺ': "l", 'ļ': "l", 'ľ': "l", 'ł': "l", 'ŀ': "l",
	'ñ': "n", 'ń': "n", 'ņ': "n", 'ň': "n", 'ŉ': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ō': "o", 'ŏ': "o", 'ő': "o", 'ø': "o",
	'ŕ': "r", 'ŗ': "r", 'ř': "r",
	'ś': "s", 'ŝ': "s", 'ş': "s", 'š': "s", 'ß': "ss",
	'ţ': "t", 'ť': "t", 'ŧ': "t", 'þ': "th",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ũ': "u", 'ū': "u", 'ŭ': "u", 'ů': "u", 'ű': "u", 'ų': "u",
	'ŵ': "w",
	'ý': "y", 'ÿ': "y", 'ŷ': "y",
	'ź': "z", 'ż': "z", 'ž': "z", 'ð': "d",
}

var folder *strings.Replacer

func init() {
	pairs := make([]string, 0, len(foldMap)*2)
	for from, to := range foldMap {
		pairs = append(pairs, string(from), to)
	}
	folder = strings.NewReplacer(pairs...)
}

// Fold lowercases s and replaces Latin letters carrying diacritics with
// their ASCII equivalents. Runes outside the Latin scripts are left as
// they are. Strings that are already folded are returned unchanged.
func Fold(s string) string {
	ascii := true
	lower := true
	for _, r := range s {
		if r >= unicode.MaxASCII {
			ascii = false
			break
		}
		if 'A' <= r && r <= 'Z' {
			lower = false
			break
		}
	}
	if ascii && lower {
		return s
	}
	return folder.Replace(strings.ToLower(s))
}

// IsWordRune reports whether r can be part of a word: any letter, digit or
// combining mark, in any script. Those runes never separate spells.
func IsWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.In(r, unicode.Mn, unicode.Mc)
}

// IsSplitRune reports whether r may take part of a splittable spell token.
func IsSplitRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// IsSplittable reports whether s consists solely of splittable runes.
func IsSplittable(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !IsSplitRune(r) {
			return false
		}
	}
	return true
}
