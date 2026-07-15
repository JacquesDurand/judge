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
	"strings"
	"syscall"
	"time"

	"judge/internal/config"
	"judge/internal/embed"
	"judge/internal/llm"
	"judge/internal/rag"
	"judge/internal/retrieval"

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
	mux.HandleFunc("POST /chat/stream", srv.handleChatStream)
	mux.HandleFunc("GET /rules/{number}", srv.handleRule)
	mux.HandleFunc("GET /card", srv.handleCard)
	mux.HandleFunc("GET /cards/search", srv.handleCardSearch)
	mux.HandleFunc("GET /healthz", srv.handleHealth)

	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// No WriteTimeout: /chat/stream holds the response open while streaming.
		// Each request is bounded by its own context timeout (requestTimeout).
		WriteTimeout: 0,
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

// Stream line types (NDJSON: one JSON object per line, newline-terminated).
type streamMeta struct {
	Type     string         `json:"type"` // "meta"
	Language string         `json:"language"`
	Cards    []string       `json:"cards"`
	Rules    []ruleCitation `json:"rules"`
}
type streamDelta struct {
	Type string `json:"type"` // "delta"
	Text string `json:"text"`
}
type streamEvent struct {
	Type  string `json:"type"`            // "done" | "error"
	Error string `json:"error,omitempty"` // set when Type == "error"
}

// handleChatStream runs the pipeline and streams the answer as newline-delimited
// JSON: one "meta" line (citations), then many "delta" lines (answer text), then
// a terminal "done" (or "error"). Errors after streaming has begun are reported
// in-band, since the 200 status is already committed.
func (s *server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Question == "" {
		writeError(w, http.StatusBadRequest, "field \"question\" is required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()

	enc := json.NewEncoder(w) // Encode appends a newline → NDJSON framing
	writeLine := func(v any) {
		_ = enc.Encode(v)
		flusher.Flush()
	}

	err := s.engine.AnswerStream(ctx, req.Question,
		func(m rag.Meta) {
			meta := streamMeta{Type: "meta", Language: m.Language, Cards: make([]string, 0, len(m.Cards)), Rules: make([]ruleCitation, 0, len(m.Rules))}
			for _, c := range m.Cards {
				meta.Cards = append(meta.Cards, c.Name)
			}
			for _, rl := range m.Rules {
				meta.Rules = append(meta.Rules, ruleCitation{Number: rl.Number, Section: rl.SectionTitle})
			}
			writeLine(meta)
		},
		func(delta string) {
			writeLine(streamDelta{Type: "delta", Text: delta})
		},
	)
	if err != nil {
		log.Printf("answer stream error: %v", err)
		writeLine(streamEvent{Type: "error", Error: "could not generate an answer right now"})
		return
	}
	writeLine(streamEvent{Type: "done"})
}

type ruleResponse struct {
	Number  string `json:"number"`
	Section string `json:"section"`
	Body    string `json:"body"`
}

// handleRule returns the full text of a single rule, so the app can expand a
// tapped citation.
func (s *server) handleRule(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if number == "" {
		writeError(w, http.StatusBadRequest, "missing rule number")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rule, err := retrieval.RuleByNumber(ctx, s.pool, number)
	if err != nil {
		log.Printf("rule lookup error: %v", err)
		writeError(w, http.StatusBadGateway, "could not look up that rule")
		return
	}
	if rule == nil {
		writeError(w, http.StatusNotFound, "no such rule")
		return
	}
	writeJSON(w, http.StatusOK, ruleResponse{Number: rule.Number, Section: rule.SectionTitle, Body: rule.Body})
}

type cardRulingResponse struct {
	PublishedAt string `json:"published_at"`
	Source      string `json:"source"`
	Comment     string `json:"comment"`
}
type cardResponse struct {
	Name       string               `json:"name"`
	ManaCost   string               `json:"mana_cost"`
	TypeLine   string               `json:"type_line"`
	OracleText string               `json:"oracle_text"`
	Rulings    []cardRulingResponse `json:"rulings"`
}

// handleCard returns a card's oracle text and rulings, so the app can expand a
// tapped card citation. Name comes as a query param (card names contain spaces
// and "//", awkward in a path segment).
func (s *server) handleCard(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing card name")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	card, err := retrieval.ResolveCard(ctx, s.pool, name)
	if err != nil {
		log.Printf("card lookup error: %v", err)
		writeError(w, http.StatusBadGateway, "could not look up that card")
		return
	}
	if card == nil {
		writeError(w, http.StatusNotFound, "no such card")
		return
	}
	rulings, err := retrieval.Rulings(ctx, s.pool, card.OracleID)
	if err != nil {
		log.Printf("card rulings error: %v", err)
		writeError(w, http.StatusBadGateway, "could not look up that card")
		return
	}

	resp := cardResponse{
		Name:       card.Name,
		ManaCost:   card.ManaCost,
		TypeLine:   card.TypeLine,
		OracleText: card.OracleText,
		Rulings:    make([]cardRulingResponse, 0, len(rulings)),
	}
	for _, rl := range rulings {
		date := ""
		if rl.PublishedAt != nil {
			date = rl.PublishedAt.Format("2006-01-02")
		}
		resp.Rulings = append(resp.Rulings, cardRulingResponse{PublishedAt: date, Source: rl.Source, Comment: rl.Comment})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleCardSearch returns up to a few card names matching the query, for the
// app's input autocomplete. Empty/short queries return an empty list.
func (s *server) handleCardSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeJSON(w, http.StatusOK, []string{})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	names, err := retrieval.SearchCardNames(ctx, s.pool, q, 8)
	if err != nil {
		log.Printf("card search error: %v", err)
		writeError(w, http.StatusBadGateway, "search failed")
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, names)
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
