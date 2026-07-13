// Command server exposes the RAG engine over HTTP.
//
//	POST /chat     {"question": "..."}  -> grounded answer + citations
//	GET  /healthz                       -> 200 if the DB is reachable
//
// The engine core (internal/rag) is shared with the CLI; this binary only adds
// the HTTP layer and a connection pool (safe for concurrent requests).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"judge/internal/config"
	"judge/internal/embed"
	"judge/internal/llm"
	"judge/internal/rag"

	"github.com/jackc/pgx/v5/pgxpool"
)

// requestTimeout caps a single question: preprocessing + embedding + generation
// (with adaptive thinking) can take a while, but not forever.
const requestTimeout = 90 * time.Second

func main() {
	config.LoadDotEnv(".env")
	port := config.MustEnv("PORT")

	// Base context cancelled on SIGINT/SIGTERM for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, config.MustEnv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	embedClient := embed.New(config.MustEnv("EMBEDDING_API_KEY"), config.MustEnv("EMBEDDING_MODEL"))
	llmClient := llm.New(config.MustEnv("LLM_API_KEY"), config.MustEnv("LLM_MODEL"))
	engine := rag.New(pool, embedClient, llmClient, 10)

	srv := &server{engine: engine, pool: pool}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /chat", srv.handleChat)
	mux.HandleFunc("GET /healthz", srv.handleHealth)

	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      requestTimeout + 10*time.Second,
	}

	// Run the server; shut it down cleanly when the signal context is cancelled.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("listening on :%s", port)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
	log.Printf("stopped")
}

type server struct {
	engine *rag.Engine
	pool   *pgxpool.Pool
}

type chatRequest struct {
	Question string `json:"question"`
}

type ruleCitation struct {
	Number  string `json:"number"`
	Section string `json:"section"`
}

type chatResponse struct {
	Answer   string         `json:"answer"`
	Language string         `json:"language"`
	Cards    []string       `json:"cards"`
	Rules    []ruleCitation `json:"rules"`
}

func (s *server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Question == "" {
		writeError(w, http.StatusBadRequest, "field \"question\" is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()

	res, err := s.engine.Answer(ctx, req.Question)
	if err != nil {
		// Upstream dependency (embedding/LLM/DB) failed; don't leak internals.
		log.Printf("answer error: %v", err)
		writeError(w, http.StatusBadGateway, "could not generate an answer right now")
		return
	}

	resp := chatResponse{
		Answer:   res.Answer,
		Language: res.Analysis.AnswerLanguage,
		Cards:    make([]string, 0, len(res.Cards)),
		Rules:    make([]ruleCitation, 0, len(res.Rules)),
	}
	for _, c := range res.Cards {
		resp.Cards = append(resp.Cards, c.Name)
	}
	for _, rl := range res.Rules {
		resp.Rules = append(resp.Rules, ruleCitation{Number: rl.Number, Section: rl.SectionTitle})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
