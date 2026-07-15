// Package rag orchestrates one question end-to-end: preprocess (Haiku) ->
// embed -> retrieve rules/glossary/cards -> assemble context -> generate
// (Sonnet). It is the reusable core shared by the CLI and, later, the HTTP API.
package rag

import (
	"context"
	"fmt"
	"strings"

	"judge/internal/embed"
	"judge/internal/llm"
	"judge/internal/retrieval"
)

// maxRulingsPerCard bounds how many rulings we inject per card, to keep the
// prompt from ballooning on heavily-ruled cards.
const maxRulingsPerCard = 15

// Engine holds the collaborators for the query flow.
type Engine struct {
	conn  retrieval.Querier
	embed *embed.Client
	llm   *llm.Client
	k     int
}

func New(conn retrieval.Querier, e *embed.Client, l *llm.Client, k int) *Engine {
	return &Engine{conn: conn, embed: e, llm: l, k: k}
}

// CardContext is a resolved card plus its rulings, as fed to the model.
type CardContext struct {
	retrieval.Card
	Rulings []retrieval.CardRuling
}

// Result carries the answer plus everything retrieved, so callers can show the
// grounding (citations, which cards resolved, etc.).
type Result struct {
	Answer   string
	Analysis llm.Analysis
	Rules    []retrieval.RuleHit
	Glossary []retrieval.GlossaryHit
	Cards    []CardContext
}

// Meta is everything known before generation starts: the detected language and
// the retrieved context. The streaming path emits this first, then the answer.
type Meta struct {
	Language string
	Rules    []retrieval.RuleHit
	Glossary []retrieval.GlossaryHit
	Cards    []CardContext
}

// prepared holds the result of the retrieval half of the pipeline.
type prepared struct {
	analysis llm.Analysis
	rules    []retrieval.RuleHit
	glossary []retrieval.GlossaryHit
	cards    []CardContext
	context  string
}

func (p *prepared) meta() Meta {
	return Meta{Language: p.analysis.AnswerLanguage, Rules: p.rules, Glossary: p.glossary, Cards: p.cards}
}

// prepare runs everything up to (but not including) generation: preprocess,
// embed, retrieve rules/glossary, resolve named cards, assemble the context.
func (e *Engine) prepare(ctx context.Context, question string) (*prepared, error) {
	analysis, err := e.llm.Preprocess(ctx, question)
	if err != nil {
		return nil, err
	}

	vecs, err := e.embed.Embed(ctx, []string{analysis.QuestionEN})
	if err != nil {
		return nil, err
	}
	query := vecs[0]

	rules, err := retrieval.SearchRules(ctx, e.conn, query, e.k)
	if err != nil {
		return nil, err
	}
	glossary, err := retrieval.SearchGlossary(ctx, e.conn, query, e.k)
	if err != nil {
		return nil, err
	}

	var cards []CardContext
	seen := make(map[string]bool)
	for _, name := range analysis.Cards {
		c, err := retrieval.ResolveCard(ctx, e.conn, name)
		if err != nil {
			return nil, err
		}
		if c == nil || seen[c.OracleID] {
			continue // no confident match, or already added
		}
		seen[c.OracleID] = true
		rulings, err := retrieval.Rulings(ctx, e.conn, c.OracleID)
		if err != nil {
			return nil, err
		}
		cards = append(cards, CardContext{Card: *c, Rulings: rulings})
	}

	return &prepared{
		analysis: analysis,
		rules:    rules,
		glossary: glossary,
		cards:    cards,
		context:  buildContext(question, rules, glossary, cards),
	}, nil
}

// Answer runs the full pipeline for one question and returns the complete result.
func (e *Engine) Answer(ctx context.Context, question string) (*Result, error) {
	p, err := e.prepare(ctx, question)
	if err != nil {
		return nil, err
	}
	answer, err := e.llm.Generate(ctx, p.analysis.AnswerLanguage, p.context)
	if err != nil {
		return nil, err
	}
	return &Result{
		Answer:   answer,
		Analysis: p.analysis,
		Rules:    p.rules,
		Glossary: p.glossary,
		Cards:    p.cards,
	}, nil
}

// AnswerStream runs the pipeline but streams the answer: onMeta is called once
// with the citations (before any text), then onDelta is called with each chunk
// of answer text as it is generated.
func (e *Engine) AnswerStream(ctx context.Context, question string, onMeta func(Meta), onDelta func(string)) error {
	p, err := e.prepare(ctx, question)
	if err != nil {
		return err
	}
	onMeta(p.meta())
	return e.llm.GenerateStream(ctx, p.analysis.AnswerLanguage, p.context, onDelta)
}

// buildContext assembles the single context block handed to the generation
// model. The original question (not the English rewrite) is included so the
// model answers the user's actual phrasing.
func buildContext(question string, rules []retrieval.RuleHit, glossary []retrieval.GlossaryHit, cards []CardContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "QUESTION:\n%s\n", question)

	b.WriteString("\nRELEVANT COMPREHENSIVE RULES:\n")
	for _, r := range rules {
		fmt.Fprintf(&b, "\n[%s] (%s)\n%s\n", r.Number, r.SectionTitle, r.Body)
	}

	b.WriteString("\nGLOSSARY:\n")
	for _, g := range glossary {
		fmt.Fprintf(&b, "\n[%s] %s\n", g.Term, g.Definition)
	}

	if len(cards) > 0 {
		b.WriteString("\nCARDS MENTIONED (data from Scryfall):\n")
		for _, c := range cards {
			fmt.Fprintf(&b, "\n[%s] %s  %s\n%s\n", c.Name, c.ManaCost, c.TypeLine, c.OracleText)
			if len(c.Rulings) > 0 {
				b.WriteString("Rulings:\n")
				for i, r := range c.Rulings {
					if i >= maxRulingsPerCard {
						fmt.Fprintf(&b, "  (+%d more rulings omitted)\n", len(c.Rulings)-maxRulingsPerCard)
						break
					}
					date := "?"
					if r.PublishedAt != nil {
						date = r.PublishedAt.Format("2006-01-02")
					}
					fmt.Fprintf(&b, "  - (%s) %s\n", date, r.Comment)
				}
			}
		}
	}
	return b.String()
}
