// Package mtg holds Magic-specific, deterministic normalisation used before the
// LLM preprocessing pass. Its job is reliability: French keyword names are a
// finite, stable set, so we map them to their canonical English keyword with a
// hand-verified table rather than trusting the model to translate terminology.
//
// Coverage is deliberately the evergreen keywords (the ones that actually come
// up); anything not here still gets a best-effort translation from the Haiku
// pass. The French spellings below should be reviewed by a French player.
package mtg

import (
	"sort"
	"unicode"
)

// FRtoEN maps a French keyword name to its canonical English keyword. Identical
// FR/EN spellings (vigilance, menace, indestructible, ...) are omitted — they
// need no normalisation. Keys are written in normal French; matching is
// case- and accent-insensitive. Terms confirmed against Scryfall FR printings.
//
// Caveat: "portée" (reach) is also a common French noun ("la portée d'un effet"
// = scope). A false rewrite there is low-impact: normalisation only feeds the
// embedding query, while the generation step always sees the user's original
// wording (see rag.buildContext), so at worst it nudges retrieval, never the
// model's understanding of the question.
var FRtoEN = map[string]string{
	"vol":                  "flying",
	"initiative":           "first strike",
	"double initiative":    "double strike",
	"contact mortel":       "deathtouch",
	"défenseur":            "defender",
	"célérité":             "haste",
	"portée":               "reach",
	"piétinement":          "trample",
	"lien de vie":          "lifelink",
	"défense talismanique": "hexproof",
	"linceul":              "shroud",
	"prouesse":             "prowess",
	"parade":               "ward",
}

// foldedEntry is a precomputed lookup key (French, folded to lowercase-ASCII)
// with its English replacement.
type foldedEntry struct {
	key []rune // folded French phrase
	en  string // canonical English keyword
}

var entries []foldedEntry // sorted longest-first for greedy matching

func init() {
	for fr, en := range FRtoEN {
		entries = append(entries, foldedEntry{key: foldRunes([]rune(fr)), en: en})
	}
	// Longest phrase first so "double initiative" wins over "initiative".
	sort.Slice(entries, func(i, j int) bool { return len(entries[i].key) > len(entries[j].key) })
}

// NormalizeKeywords replaces French keyword names with their canonical English
// keyword, matching case- and accent-insensitively on whole words. Everything
// else is left untouched, so the surrounding sentence stays intact for the LLM.
func NormalizeKeywords(q string) string {
	orig := []rune(q)
	folded := foldRunes(orig)

	var out []rune
	i := 0
	for i < len(orig) {
		if !isWordRune(prev(folded, i)) { // only start matching at a word boundary
			if e := longestMatch(folded, i); e != nil {
				out = append(out, []rune(e.en)...)
				i += len(e.key)
				continue
			}
		}
		out = append(out, orig[i])
		i++
	}
	return string(out)
}

// longestMatch returns the entry matching folded[i:] at a trailing word boundary,
// or nil. entries is longest-first, so the first hit is the longest.
func longestMatch(folded []rune, i int) *foldedEntry {
	for k := range entries {
		e := &entries[k]
		end := i + len(e.key)
		if end > len(folded) || !equal(folded[i:end], e.key) {
			continue
		}
		if end == len(folded) || !isWordRune(folded[end]) {
			return e
		}
	}
	return nil
}

func foldRunes(rs []rune) []rune {
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[i] = fold(r)
	}
	return out
}

// fold maps a rune to lowercase with common French diacritics stripped. Each
// mapping is single-rune -> single-rune, so folded slices stay index-aligned
// with the original.
func fold(r rune) rune {
	r = unicode.ToLower(r) // lowercase first, so uppercase accents (É) fold too
	switch r {
	case 'à', 'â', 'ä', 'á':
		return 'a'
	case 'ç':
		return 'c'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'î', 'ï', 'í':
		return 'i'
	case 'ô', 'ö', 'ó':
		return 'o'
	case 'û', 'ù', 'ü', 'ú':
		return 'u'
	case 'ÿ':
		return 'y'
	}
	return r
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// prev returns the rune before index i, or a space if i is at the start (so the
// start of the string counts as a word boundary).
func prev(rs []rune, i int) rune {
	if i == 0 {
		return ' '
	}
	return rs[i-1]
}

func equal(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
