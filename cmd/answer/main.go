// Command answer runs the full RAG pipeline for one question and prints the
// grounded answer: preprocess -> retrieve -> assemble -> generate.
//
//	go run ./cmd/answer "si je lance Foudre sur une créature, mon adversaire peut-il la rediriger ?"
//	go run ./cmd/answer -k 12 "how does trample interact with deathtouch?"
//	go run ./cmd/answer -v "..."   # also print what was retrieved
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"judge/internal/config"
	"judge/internal/embed"
	"judge/internal/llm"
	"judge/internal/rag"

	"github.com/jackc/pgx/v5"
)

func main() {
	k := flag.Int("k", 10, "number of rule chunks / glossary entries to retrieve")
	verbose := flag.Bool("v", false, "also print the retrieved context and analysis")
	flag.Parse()
	question := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if question == "" {
		log.Fatal(`usage: answer [-k N] [-v] "<your rules question>"`)
	}

	config.LoadDotEnv(".env")
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, config.MustEnv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("connect to Postgres: %v", err)
	}
	defer conn.Close(ctx)

	embedClient := embed.New(config.MustEnv("EMBEDDING_API_KEY"), config.MustEnv("EMBEDDING_MODEL"))
	llmClient := llm.New(config.MustEnv("LLM_API_KEY"), config.MustEnv("LLM_MODEL"))
	engine := rag.New(conn, embedClient, llmClient, *k)

	res, err := engine.Answer(ctx, question)
	if err != nil {
		log.Fatalf("answer: %v", err)
	}

	if *verbose {
		fmt.Printf("── analysis ──\nquestion_en: %s\ncards: %v\nlanguage: %s\n",
			res.Analysis.QuestionEN, res.Analysis.Cards, res.Analysis.AnswerLanguage)
		fmt.Printf("\n── retrieved rules ──\n")
		for _, r := range res.Rules {
			fmt.Printf("  [%.3f] %s (%s)\n", r.Distance, r.Number, r.SectionTitle)
		}
		for _, c := range res.Cards {
			fmt.Printf("  card: %s (sim %.2f, %d rulings)\n", c.Name, c.Similarity, len(c.Rulings))
		}
		fmt.Printf("\n── answer ──\n")
	}

	fmt.Println(res.Answer)
}
