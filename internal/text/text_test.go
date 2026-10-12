package text

import "testing"

func TestFold(t *testing.T) {
	cases := map[string]string{
		"Ärger":   "arger",
		"naïve":   "naive",
		"ABC":     "abc",
		"abc123":  "abc123",
		"straße":  "strasse",
		"Zürich":  "zurich",
		"coöper":  "cooper",
		"привет":  "привет",
		"":        "",
		"already": "already",
	}
	for in, want := range cases {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFoldFastPathIdentity(t *testing.T) {
	in := "alreadyfolded"
	if got := Fold(in); got != in {
		t.Fatalf("Fold(%q) = %q", in, got)
	}
}

func TestIsSplittable(t *testing.T) {
	yes := []string{"abc", "a1b2", "007"}
	no := []string{"", "a-b", "a b", "café", "naïve"}
	for _, s := range yes {
		if !IsSplittable(s) {
			t.Errorf("IsSplittable(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsSplittable(s) {
			t.Errorf("IsSplittable(%q) = true, want false", s)
		}
	}
}

func TestIsWordRune(t *testing.T) {
	for _, r := range []rune{'a', 'Z', '7', 'é', 'я'} {
		if !IsWordRune(r) {
			t.Errorf("IsWordRune(%q) = false, want true", r)
		}
	}
	for _, r := range []rune{'-', ' ', '.', '/'} {
		if IsWordRune(r) {
			t.Errorf("IsWordRune(%q) = true, want false", r)
		}
	}
}
