// Command ingest is the one-shot ingestion CLI: it fetches source data
// (Comprehensive Rules, later Scryfall) and loads it into Postgres. Re-runnable
// at any time — inserts are upserts keyed on the natural identifier.
//
//	go run ./cmd/ingest
//
// Configuration comes from the environment (see .env.example). A local .env is
// loaded automatically if present.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"judge/internal/cards"
	"judge/internal/config"
	"judge/internal/embed"
	"judge/internal/rules"

	"github.com/jackc/pgx/v5"
)

// embedBatchSize is how many texts we send to the embeddings API per request.
// Rule/glossary chunks are small (a few hundred tokens), so this stays well
// under the provider's per-request limits.
const embedBatchSize = 128

const userAgent = "judge-mtg-rag/0.1 (personal learning project)"

func main() {
	// Select which sources to ingest. With no flag, ingest everything.
	doRules := flag.Bool("rules", false, "ingest the Comprehensive Rules + glossary")
	doCards := flag.Bool("cards", false, "ingest Scryfall cards + rulings")
	doEmbed := flag.Bool("embed", false, "embed rules + glossary rows that have no vector yet")
	flag.Parse()
	if !*doRules && !*doCards && !*doEmbed {
		*doRules, *doCards, *doEmbed = true, true, true
	}

	config.LoadDotEnv(".env")
	dbURL := config.MustEnv("DATABASE_URL")

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect to Postgres: %v", err)
	}
	defer conn.Close(ctx)

	if *doRules {
		if err := ingestRules(ctx, conn); err != nil {
			log.Fatalf("ingest rules: %v", err)
		}
	}
	if *doCards {
		if err := ingestCards(ctx, conn); err != nil {
			log.Fatalf("ingest cards: %v", err)
		}
	}
	if *doEmbed {
		if err := ingestEmbeddings(ctx, conn); err != nil {
			log.Fatalf("embed: %v", err)
		}
	}
	log.Printf("done")
}

func ingestRules(ctx context.Context, conn *pgx.Conn) error {
	rulesURL := config.MustEnv("COMPREHENSIVE_RULES_URL")
	log.Printf("fetching Comprehensive Rules from %s", rulesURL)
	raw, err := fetch(ctx, rulesURL)
	if err != nil {
		return err
	}
	parsedRules, glossary, err := rules.Parse(raw)
	if err != nil {
		return err
	}
	log.Printf("parsed %d rules and %d glossary entries", len(parsedRules), len(glossary))

	if err := upsertRules(ctx, conn, parsedRules); err != nil {
		return err
	}
	return upsertGlossary(ctx, conn, glossary)
}

func ingestCards(ctx context.Context, conn *pgx.Conn) error {
	catalogueURL := config.MustEnv("SCRYFALL_BULK_DATA_URL")

	cardsURI, err := cards.BulkURI(ctx, catalogueURL, "oracle_cards")
	if err != nil {
		return err
	}
	log.Printf("fetching Scryfall oracle_cards from %s", cardsURI)
	cs, err := cards.FetchCards(ctx, cardsURI)
	if err != nil {
		return err
	}
	log.Printf("parsed %d cards", len(cs))
	if err := storeCards(ctx, conn, cs); err != nil {
		return err
	}

	rulingsURI, err := cards.BulkURI(ctx, catalogueURL, "rulings")
	if err != nil {
		return err
	}
	log.Printf("fetching Scryfall rulings from %s", rulingsURI)
	rs, err := cards.FetchRulings(ctx, rulingsURI)
	if err != nil {
		return err
	}
	log.Printf("parsed %d rulings", len(rs))
	return storeRulings(ctx, conn, cs, rs)
}

// storeCards reloads the cards table wholesale (TRUNCATE + COPY). Cards carry no
// derived state (no embeddings), so a full replace is simplest and fastest, and
// COPY is far quicker than row-by-row upserts for ~30k rows. CASCADE clears the
// dependent rulings, which are reloaded immediately after.
func storeCards(ctx context.Context, conn *pgx.Conn, cs []cards.Card) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "TRUNCATE cards CASCADE"); err != nil {
		return err
	}
	rows := make([][]any, len(cs))
	for i, c := range cs {
		rows[i] = []any{c.OracleID, c.Name, c.ManaCost, c.TypeLine, c.OracleText}
	}
	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"cards"},
		[]string{"oracle_id", "name", "mana_cost", "type_line", "oracle_text"},
		pgx.CopyFromRows(rows))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// storeRulings reloads the rulings table (TRUNCATE + COPY). Rulings have no
// natural unique key, so a wholesale reload is the clean way to stay idempotent.
// Rulings whose oracle_id isn't among the loaded cards are dropped, otherwise
// the foreign key would reject the whole COPY.
func storeRulings(ctx context.Context, conn *pgx.Conn, cs []cards.Card, rs []cards.Ruling) error {
	known := make(map[string]bool, len(cs))
	for _, c := range cs {
		known[c.OracleID] = true
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "TRUNCATE rulings"); err != nil {
		return err
	}
	var rows [][]any
	var skipped int
	for _, r := range rs {
		if !known[r.OracleID] {
			skipped++
			continue
		}
		rows = append(rows, []any{r.OracleID, r.Source, r.PublishedAt, r.Comment})
	}
	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"rulings"},
		[]string{"oracle_id", "source", "published_at", "comment"},
		pgx.CopyFromRows(rows))
	if err != nil {
		return err
	}
	if skipped > 0 {
		log.Printf("skipped %d rulings with no matching card", skipped)
	}
	return tx.Commit(ctx)
}

// upsertRules inserts/updates every rule in a single transaction. On conflict it
// refreshes the text and clears the embedding only when the body actually
// changed, so a re-run re-embeds just the rules that moved.
func upsertRules(ctx context.Context, conn *pgx.Conn, rs []rules.Rule) error {
	const q = `
		INSERT INTO rules (rule_number, section_title, body)
		VALUES ($1, $2, $3)
		ON CONFLICT (rule_number) DO UPDATE SET
			section_title = EXCLUDED.section_title,
			body          = EXCLUDED.body,
			embedding     = CASE WHEN rules.body IS DISTINCT FROM EXCLUDED.body
			                     THEN NULL ELSE rules.embedding END,
			updated_at    = now()`

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	batch := &pgx.Batch{}
	for _, r := range rs {
		batch.Queue(q, r.Number, r.SectionTitle, r.Body)
	}
	if err := sendBatch(ctx, tx, batch); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func upsertGlossary(ctx context.Context, conn *pgx.Conn, es []rules.GlossaryEntry) error {
	const q = `
		INSERT INTO glossary (term, definition)
		VALUES ($1, $2)
		ON CONFLICT (term) DO UPDATE SET
			definition = EXCLUDED.definition,
			embedding  = CASE WHEN glossary.definition IS DISTINCT FROM EXCLUDED.definition
			                  THEN NULL ELSE glossary.embedding END`

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	batch := &pgx.Batch{}
	for _, e := range es {
		batch.Queue(q, e.Term, e.Definition)
	}
	if err := sendBatch(ctx, tx, batch); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// sendBatch executes every queued statement and surfaces the first error, so a
// bad row fails the whole transaction rather than being silently skipped.
func sendBatch(ctx context.Context, tx pgx.Tx, batch *pgx.Batch) error {
	br := tx.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("batch statement %d: %w", i, err)
		}
	}
	return nil
}

// ingestEmbeddings fills the embedding column for any rules/glossary rows that
// don't have a vector yet. It only touches NULL rows, so it is cheap to re-run
// and resumes cleanly if interrupted (each batch commits on its own).
func ingestEmbeddings(ctx context.Context, conn *pgx.Conn) error {
	client := embed.New(config.MustEnv("EMBEDDING_API_KEY"), config.MustEnv("EMBEDDING_MODEL"))

	// For rules we embed the section title together with the body, so the
	// vector carries the rule's topical context (e.g. "601. Casting Spells").
	n, err := embedTable(ctx, conn, client,
		`SELECT rule_number, concat_ws(E'\n', section_title, body) FROM rules WHERE embedding IS NULL`,
		`UPDATE rules SET embedding = $1::vector WHERE rule_number = $2`)
	if err != nil {
		return err
	}
	log.Printf("embedded %d rules", n)

	n, err = embedTable(ctx, conn, client,
		`SELECT term, term || ': ' || definition FROM glossary WHERE embedding IS NULL`,
		`UPDATE glossary SET embedding = $1::vector WHERE term = $2`)
	if err != nil {
		return err
	}
	log.Printf("embedded %d glossary entries", n)
	return nil
}

// embedTable reads (key, text) pairs from selectSQL, embeds the texts in
// batches, and writes each vector back via updateSQL ($1 = vector, $2 = key).
func embedTable(ctx context.Context, conn *pgx.Conn, client *embed.Client, selectSQL, updateSQL string) (int, error) {
	rows, err := conn.Query(ctx, selectSQL)
	if err != nil {
		return 0, err
	}
	var keys, texts []string
	for rows.Next() {
		var key, text string
		if err := rows.Scan(&key, &text); err != nil {
			rows.Close()
			return 0, err
		}
		keys = append(keys, key)
		texts = append(texts, text)
	}
	rows.Close() // must finish reading before reusing the connection for a batch
	if err := rows.Err(); err != nil {
		return 0, err
	}

	total := 0
	for start := 0; start < len(texts); start += embedBatchSize {
		end := min(start+embedBatchSize, len(texts))
		vecs, err := client.Embed(ctx, texts[start:end])
		if err != nil {
			return total, err
		}

		batch := &pgx.Batch{}
		for i, v := range vecs {
			batch.Queue(updateSQL, embed.VectorLiteral(v), keys[start+i])
		}
		br := conn.SendBatch(ctx, batch)
		for range vecs {
			if _, err := br.Exec(); err != nil {
				br.Close()
				return total, err
			}
		}
		br.Close()

		total += len(vecs)
		log.Printf("  ... %d/%d embedded", total, len(texts))
	}
	return total, nil
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
