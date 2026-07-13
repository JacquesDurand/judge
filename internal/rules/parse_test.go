package rules

import "testing"

// A synthetic snippet mirroring the real file's shape: a table of contents that
// repeats the headers, then the real rules, then the glossary, then credits.
// Written with CRLF and a BOM to exercise the normalisation path.
const sample = "\xEF\xBB\xBF" +
	"Magic: The Gathering Comprehensive Rules\r\n\r\n" +
	"Contents\r\n\r\n" +
	"1. Game Concepts\r\n100. General\r\n" + // table of contents (no bodies)
	"Glossary\r\nCredits\r\n\r\n" +
	"1. Game Concepts\r\n\r\n" + // real rules start here
	"100. General\r\n\r\n" +
	"100.1. These rules apply.\r\n\r\n" +
	"100.1a A two-player game.\r\n\r\n" +
	"Example: this line continues 100.1a.\r\n\r\n" +
	"601. Casting Spells\r\n\r\n" +
	"601.2 To cast a spell.\r\n\r\n" +
	"Glossary\r\n\r\n" +
	"Ability\r\n1. Text on an object.\r\n2. An ability on the stack.\r\n\r\n" +
	"Absorb\r\nA keyword ability.\r\n\r\n" +
	"Credits\r\nThanks everyone.\r\n"

func TestParse(t *testing.T) {
	rules, glossary, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}

	wantRules := []Rule{
		{Number: "100.1", SectionTitle: "100. General", Body: "These rules apply."},
		{Number: "100.1a", SectionTitle: "100. General", Body: "A two-player game.\nExample: this line continues 100.1a."},
		{Number: "601.2", SectionTitle: "601. Casting Spells", Body: "To cast a spell."},
	}
	if len(rules) != len(wantRules) {
		t.Fatalf("got %d rules, want %d: %+v", len(rules), len(wantRules), rules)
	}
	for i, w := range wantRules {
		if rules[i] != w {
			t.Errorf("rule %d = %+v, want %+v", i, rules[i], w)
		}
	}

	wantGloss := []GlossaryEntry{
		{Term: "Ability", Definition: "1. Text on an object.\n2. An ability on the stack."},
		{Term: "Absorb", Definition: "A keyword ability."},
	}
	if len(glossary) != len(wantGloss) {
		t.Fatalf("got %d glossary entries, want %d: %+v", len(glossary), len(wantGloss), glossary)
	}
	for i, w := range wantGloss {
		if glossary[i] != w {
			t.Errorf("glossary %d = %+v, want %+v", i, glossary[i], w)
		}
	}
}
