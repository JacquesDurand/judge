// Package retrieval runs the semantic (pgvector) searches over the embedded
// corpus: given a query vector, it returns the closest rule chunks and glossary
// entries by cosine distance.
package retrieval

import (
	"context"
	"errors"
	"time"

	"judge/internal/embed"

	"github.com/jackc/pgx/v5"
)

// Querier is the subset of pgx used here. Both *pgx.Conn (CLI) and
// *pgxpool.Pool (HTTP server, concurrency-safe) satisfy it.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RuleHit is a retrieved rule chunk with its cosine distance to the query
// (0 = identical direction, 2 = opposite; lower is more relevant).
type RuleHit struct {
	Number       string
	SectionTitle string
	Body         string
	Distance     float64
}

// GlossaryHit is a retrieved glossary entry with its cosine distance.
type GlossaryHit struct {
	Term       string
	Definition string
	Distance   float64
}

// SearchRules returns the k rule chunks closest to the query vector.
func SearchRules(ctx context.Context, db Querier, query []float32, k int) ([]RuleHit, error) {
	const q = `
		SELECT rule_number, section_title, body, embedding <=> $1::vector AS dist
		FROM rules
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2`
	rows, err := db.Query(ctx, q, embed.VectorLiteral(query), k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []RuleHit
	for rows.Next() {
		var h RuleHit
		if err := rows.Scan(&h.Number, &h.SectionTitle, &h.Body, &h.Distance); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// SearchGlossary returns the k glossary entries closest to the query vector.
func SearchGlossary(ctx context.Context, db Querier, query []float32, k int) ([]GlossaryHit, error) {
	const q = `
		SELECT term, definition, embedding <=> $1::vector AS dist
		FROM glossary
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2`
	rows, err := db.Query(ctx, q, embed.VectorLiteral(query), k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []GlossaryHit
	for rows.Next() {
		var h GlossaryHit
		if err := rows.Scan(&h.Term, &h.Definition, &h.Distance); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// Card is a resolved card with its printable fields.
type Card struct {
	OracleID   string
	Name       string
	ManaCost   string
	TypeLine   string
	OracleText string
	Similarity float64
}

// CardRuling is one official ruling for a card.
type CardRuling struct {
	Source      string
	PublishedAt *time.Time
	Comment     string
}

// ResolveCard fuzzy-matches a (possibly misspelled) card name against cards.name
// via pg_trgm and returns the single best match, or nil if nothing clears the
// trigram similarity threshold. This is the "exact card, not similar card"
// lookup — deliberately NOT a vector search.
func ResolveCard(ctx context.Context, db Querier, name string) (*Card, error) {
	const q = `
		SELECT oracle_id, name, mana_cost, type_line, oracle_text, similarity(name, $1) AS sim
		FROM cards
		WHERE name % $1
		ORDER BY sim DESC, length(name)
		LIMIT 1`
	var c Card
	err := db.QueryRow(ctx, q, name).
		Scan(&c.OracleID, &c.Name, &c.ManaCost, &c.TypeLine, &c.OracleText, &c.Similarity)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Rulings returns the official rulings for a card, oldest first.
func Rulings(ctx context.Context, db Querier, oracleID string) ([]CardRuling, error) {
	const q = `
		SELECT COALESCE(source, ''), published_at, comment
		FROM rulings
		WHERE oracle_id = $1
		ORDER BY published_at NULLS LAST`
	rows, err := db.Query(ctx, q, oracleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CardRuling
	for rows.Next() {
		var r CardRuling
		if err := rows.Scan(&r.Source, &r.PublishedAt, &r.Comment); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
