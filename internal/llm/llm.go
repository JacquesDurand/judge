// Package llm wraps the Anthropic API for the two model calls in the query flow:
// a cheap Haiku preprocessing pass (extract card names, normalise language) and
// the Sonnet generation call that writes the grounded answer.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"judge/internal/mtg"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// preprocessModel is fixed (cheap, fast); the generation model is configurable.
const preprocessModel = "claude-haiku-4-5"

// Client holds the Anthropic client plus the configured generation model.
type Client struct {
	api      anthropic.Client
	genModel string
}

func New(apiKey, genModel string) *Client {
	return &Client{
		api:      anthropic.NewClient(option.WithAPIKey(apiKey)),
		genModel: genModel,
	}
}

// Analysis is the structured result of the preprocessing pass.
type Analysis struct {
	QuestionEN     string   `json:"question_en"`     // English rewrite, for embedding
	Cards          []string `json:"cards"`           // canonical English card names cited
	AnswerLanguage string   `json:"answer_language"` // ISO code the user wrote in
}

const preprocessSystem = `You extract structured information from a Magic: The Gathering rules question.
The user may write in English, French, or a mix. Respond with ONLY a JSON object (no prose, no code fences) with exactly these keys:
- "question_en": the question rewritten in clear English, suitable for semantic search over the Comprehensive Rules. If it is already English, lightly clean it up.
- "cards": array of Magic card names explicitly named in the question, each given as its canonical ENGLISH name (translate French names, e.g. "Foudre" -> "Lightning Bolt"). Use [] if no specific card is named.
- "answer_language": the ISO 639-1 code of the language the user wrote in ("en", "fr", ...). For a mix, pick the dominant one.

When rewriting the question, use the exact canonical ENGLISH keyword names for Magic mechanics rather than paraphrasing them (e.g. "défense talismanique" -> "hexproof", "piétinement" -> "trample", "lien de vie" -> "lifelink"). Common keywords are already substituted for you, but map any that remain.`

// Preprocess runs the Haiku pass. On any parse failure it falls back to treating
// the raw question as English with no cards, so the pipeline still works.
func (c *Client) Preprocess(ctx context.Context, question string) (Analysis, error) {
	msg, err := c.api.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     preprocessModel,
		MaxTokens: 1024,
		System:    []anthropic.TextBlockParam{{Text: preprocessSystem}},
		Messages: []anthropic.MessageParam{
			// Deterministic keyword normalisation first; Haiku handles the rest.
			anthropic.NewUserMessage(anthropic.NewTextBlock(mtg.NormalizeKeywords(question))),
		},
	})
	if err != nil {
		return Analysis{}, fmt.Errorf("preprocess: %w", err)
	}

	var a Analysis
	if err := json.Unmarshal([]byte(extractJSON(text(msg))), &a); err != nil {
		// Degrade gracefully rather than fail the whole query.
		return Analysis{QuestionEN: question, AnswerLanguage: "en"}, nil
	}
	if a.QuestionEN == "" {
		a.QuestionEN = question
	}
	if a.AnswerLanguage == "" {
		a.AnswerLanguage = "en"
	}
	return a, nil
}

const generateSystem = `You are a Magic: The Gathering rules assistant for a casual playgroup.

Answer the question using ONLY the provided context (Comprehensive Rules excerpts, glossary entries, and card data). Follow these rules strictly:
- Ground every claim in the context. Do NOT use outside knowledge of the rules, even if you are confident — the context is the single source of truth.
- Cite the specific rule numbers you rely on, in parentheses, e.g. (601.2a). Every rules claim needs a citation.
- If the provided context does not contain enough to answer correctly, say so plainly ("Je ne suis pas sûr d'après les règles récupérées ...") rather than guessing.
- Be concise and concrete. Walk through the interaction step by step when it is subtle.
- Card data is from Scryfall.
- Write in plain text — no Markdown (no #, *, backticks or tables). Use short paragraphs, and simple "- " bullets only if a list genuinely helps. The answer is shown in a mobile chat bubble.
- Write your entire answer in the language identified by this ISO code: %s.`

// generateParams builds the request shared by the buffered and streaming paths.
func (c *Client) generateParams(answerLanguage, contextBlock string) anthropic.MessageNewParams {
	return anthropic.MessageNewParams{
		Model:     anthropic.Model(c.genModel),
		MaxTokens: 8192,
		// Adaptive thinking helps on the chained-rule interactions that are the
		// whole point of this tool; it stays off for trivial questions.
		Thinking: anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
		System:   []anthropic.TextBlockParam{{Text: fmt.Sprintf(generateSystem, answerLanguage)}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(contextBlock)),
		},
	}
}

// Generate produces the grounded answer from an assembled context block.
func (c *Client) Generate(ctx context.Context, answerLanguage, contextBlock string) (string, error) {
	msg, err := c.api.Messages.New(ctx, c.generateParams(answerLanguage, contextBlock))
	if err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}
	return text(msg), nil
}

// GenerateStream is like Generate but calls onDelta with each chunk of answer
// text as it is produced. Thinking blocks are not forwarded — only the visible
// answer. onDelta runs on the calling goroutine, in order.
func (c *Client) GenerateStream(ctx context.Context, answerLanguage, contextBlock string, onDelta func(string)) error {
	stream := c.api.Messages.NewStreaming(ctx, c.generateParams(answerLanguage, contextBlock))
	for stream.Next() {
		event := stream.Current()
		if delta, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if text, ok := delta.Delta.AsAny().(anthropic.TextDelta); ok {
				onDelta(text.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("generate stream: %w", err)
	}
	return nil
}

// text concatenates all text blocks in a response (skipping thinking blocks).
func text(msg *anthropic.Message) string {
	var b strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// extractJSON returns the substring from the first '{' to the last '}', so a
// stray code fence or preamble doesn't break json.Unmarshal.
func extractJSON(s string) string {
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i < 0 || j < i {
		return s
	}
	return s[i : j+1]
}
