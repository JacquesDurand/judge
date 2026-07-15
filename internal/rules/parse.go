// Package rules parses the Magic: The Gathering Comprehensive Rules .txt file
// into self-contained chunks suitable for embedding and retrieval.
//
// The .txt has a very regular three-level structure:
//
//  1. Game Concepts                     <- section header      (^[1-9]\. Title)
//  100. General                         <- category header     (^\d{3}\. Title, no body)
//     100.1. These Magic rules apply ...   <- rule                (^\d{3}\.\d+\. body)
//     100.1a A two-player game ...         <- subrule             (^\d{3}\.\d+[a-z] body)
//     Example: ...                     <- continuation of the current (sub)rule
//
// The file starts with a table of contents that repeats the section/category
// headers with no bodies; the real rules begin at the *second* occurrence of
// "1. Game Concepts" and run until the Glossary, which runs until Credits.
package rules

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// Rule is one (sub)rule chunk. SectionTitle is the containing category header
// (e.g. "601. Casting Spells"), kept on the row so a retrieved chunk is
// understandable without its surrounding context.
type Rule struct {
	Number       string // e.g. "100.1", "601.2a"
	SectionTitle string // e.g. "601. Casting Spells"
	Body         string
}

// GlossaryEntry is one term/definition pair from the Glossary section.
type GlossaryEntry struct {
	Term       string
	Definition string
}

var (
	// A (sub)rule line: three-digit category, a dot, the rule number, an
	// optional subrule letter, then the body. Rule lines like "100.1." carry a
	// trailing dot after the number; subrule lines like "100.1a" do not — the
	// optional \.? absorbs it either way.
	ruleLine = regexp.MustCompile(`^(\d{3}\.\d+[a-z]?)\.?\s+(.*)$`)
	// A category header: three digits, a dot, a space, then a non-digit title.
	// (Distinguished from a rule line by the absence of a second number.)
	categoryLine = regexp.MustCompile(`^\d{3}\.\s+\D`)
	// A top-level section header, e.g. "1. Game Concepts".
	sectionLine = regexp.MustCompile(`^[1-9]\.\s+\D`)
)

// Parse turns the raw Comprehensive Rules .txt bytes into rules and glossary
// entries. It tolerates a UTF-8 BOM and CRLF line endings.
func Parse(raw []byte) ([]Rule, []GlossaryEntry, error) {
	// Strip a leading UTF-8 BOM and normalise CRLF -> LF.
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))

	lines := strings.Split(string(raw), "\n")

	rulesStart := lastIndex(lines, "1. Game Concepts")
	glossaryStart := lastIndex(lines, "Glossary")
	creditsStart := lastIndex(lines, "Credits")
	if rulesStart < 0 || glossaryStart <= rulesStart || creditsStart <= glossaryStart {
		return nil, nil, fmt.Errorf("rules: could not locate document sections "+
			"(rulesStart=%d glossaryStart=%d creditsStart=%d)", rulesStart, glossaryStart, creditsStart)
	}

	rules := parseRules(lines[rulesStart:glossaryStart])
	glossary := parseGlossary(lines[glossaryStart+1 : creditsStart])
	return rules, glossary, nil
}

func parseRules(lines []string) []Rule {
	var out []Rule
	var section string // current category header, used as SectionTitle

	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			continue
		}
		switch {
		case ruleLine.MatchString(line):
			m := ruleLine.FindStringSubmatch(line)
			out = append(out, Rule{Number: m[1], SectionTitle: section, Body: m[2]})
		case categoryLine.MatchString(line):
			section = line // e.g. "601. Casting Spells"
		case sectionLine.MatchString(line):
			// Top-level section header — not stored, and we key SectionTitle on
			// the more specific category instead. Nothing to do.
		default:
			// A continuation line (e.g. "Example: ...") belongs to the current
			// (sub)rule. Append it so it is embedded together with its rule.
			if n := len(out); n > 0 {
				out[n-1].Body += "\n" + strings.TrimSpace(line)
			}
		}
	}
	return out
}

func parseGlossary(lines []string) []GlossaryEntry {
	var out []GlossaryEntry
	var block []string

	flush := func() {
		if len(block) == 0 {
			return
		}
		term := strings.TrimSpace(block[0])
		def := strings.TrimSpace(strings.Join(block[1:], "\n"))
		if term != "" && def != "" {
			out = append(out, GlossaryEntry{Term: term, Definition: def})
		}
		block = nil
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush() // blank line separates entries
			continue
		}
		block = append(block, line)
	}
	flush()
	return out
}

// lastIndex returns the index of the last line equal (after trimming) to want,
// or -1. The document repeats section headers in its table of contents, so the
// real content is always the last occurrence.
func lastIndex(lines []string, want string) int {
	idx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == want {
			idx = i
		}
	}
	return idx
}
