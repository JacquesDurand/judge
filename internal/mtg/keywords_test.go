package mtg

import "testing"

func TestNormalizeKeywords(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Accents and case are ignored.
		{"la défense talismanique", "la hexproof"},
		{"DÉFENSE TALISMANIQUE", "hexproof"},
		{"defense talismanique", "hexproof"},
		// Longest match wins.
		{"double initiative", "double strike"},
		{"initiative simple", "first strike simple"},
		// Multiple keywords in one sentence.
		{"une créature avec le piétinement et le contact mortel",
			"une créature avec le trample et le deathtouch"},
		// Corrected/added entries (confirmed against Scryfall FR printings).
		{"une créature avec la portée", "une créature avec la reach"},
		{"Parade {2}", "ward {2}"},
		// Word-boundary safety: no match inside a larger word.
		{"survol", "survol"},
		// Unknown terms are left alone.
		{"que fait la ménace ?", "que fait la ménace ?"},
		// Non-keyword text untouched, card names untouched.
		{"si je lance Foudre sur une créature", "si je lance Foudre sur une créature"},
	}
	for _, c := range cases {
		if got := NormalizeKeywords(c.in); got != c.want {
			t.Errorf("NormalizeKeywords(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
