// Command ask is a retrieval probe: it embeds a question, runs the pgvector
// similarity search over rules + glossary, and prints the chunks that would be
// fed to the LLM. No generation yet — this is the tool for eyeballing whether
// retrieval surfaces the right rules before we build anything on top of it.
//
//	go run ./cmd/ask "if I cast Lightning Bolt targeting a creature, can it be redirected?"
//	go run ./cmd/ask -k 8 "how does trample interact with deathtouch?"
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"judge/internal/config"
	"judge/internal/embed"
	"judge/internal/retrieval"

	"github.com/jackc/pgx/v5"
)

func main() {
	// Default k errs on the generous side: vector search clusters by topic, so
	// the one rule that answers a specific interaction can rank below general or
	// definitional chunks. Retrieving a few extra is cheaper than missing it
	// (see docs/ARCHITECTURE.md, "Prompting notes").
	k := flag.Int("k", 10, "number of rule chunks (and glossary entries) to retrieve")
	flag.Parse()
	question := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if question == "" {
		log.Fatal("usage: ask [-k N] \"<your rules question>\"")
	}

	config.LoadDotEnv(".env")
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, config.MustEnv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("connect to Postgres: %v", err)
	}
	defer conn.Close(ctx)

	// Embed the question with the SAME model used at ingestion, or the vectors
	// wouldn't be comparable.
	client := embed.New(config.MustEnv("EMBEDDING_API_KEY"), config.MustEnv("EMBEDDING_MODEL"))
	vecs, err := client.Embed(ctx, []string{question})
	if err != nil {
		log.Fatalf("embed question: %v", err)
	}
	query := vecs[0]

	rules, err := retrieval.SearchRules(ctx, conn, query, *k)
	if err != nil {
		log.Fatalf("search rules: %v", err)
	}
	glossary, err := retrieval.SearchGlossary(ctx, conn, query, *k)
	if err != nil {
		log.Fatalf("search glossary: %v", err)
	}

	fmt.Printf("Q: %s\n", question)
	fmt.Printf("\n=== Rules (top %d by cosine distance) ===\n", *k)
	for _, h := range rules {
		fmt.Printf("\n[%.4f] %s  (%s)\n%s\n", h.Distance, h.Number, h.SectionTitle, indent(h.Body))
	}
	fmt.Printf("\n=== Glossary (top %d) ===\n", *k)
	for _, h := range glossary {
		fmt.Printf("\n[%.4f] %s\n%s\n", h.Distance, h.Term, indent(h.Definition))
	}
}

// indent prefixes each line so multi-line bodies read clearly under their header.
func indent(s string) string {
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}
