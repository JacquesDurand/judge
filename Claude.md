# MTG Rules Assistant

A RAG-powered chatbot that answers Magic: The Gathering rules questions, built to learn
RAG and mobile development. Backend in Go, storage in Postgres (pgvector + pg_trgm),
mobile client in React Native (Expo).

## What this project is

Friends in a new-ish playgroup keep needing to look up rules interactions (stack,
priority, layers, replacement effects, etc). Instead of manually searching, this app
lets them ask in plain language and get an answer grounded in the actual Comprehensive
Rules text and card data, with rule numbers cited so the answer can be double-checked.

Not trying to reinvent Scryfall or the Comprehensive Rules — this is a thin,
well-grounded layer on top of them.

## Tech stack

- **Backend**: Go (stdlib net/http or chi router — keep it simple, no heavy framework needed)
- **Database**: Postgres 16 + `pgvector` extension (semantic search over rules) +
  `pg_trgm` extension (fuzzy card name lookup)
- **Embeddings**: external API (OpenAI `text-embedding-3-small` or Voyage) — do not
  train or self-host an embedding model, it's unnecessary for this corpus size
- **Generation**: Claude or GPT API call, prompted to answer only from retrieved context
- **Mobile**: React Native + Expo

## Architecture (see docs/ARCHITECTURE.md for full detail)

Two separate flows:

1. **Offline ingestion** (run on a schedule, not per-request): fetch the Comprehensive
   Rules text + Scryfall bulk card data → chunk by rule number → embed → store in Postgres.
2. **Online query** (per user question): embed the question → pgvector similarity
   search over rule chunks → trigram lookup for any card names mentioned → assemble
   context → call the LLM → stream answer with citations back to the app.

## Key design decisions — don't relitigate these without discussion

- **Cards are looked up by name (pg_trgm fuzzy match), never by vector similarity.**
  Vector search on cards would return semantically *similar* cards, not the exact card
  the user named. Vector search is reserved for conceptual rules questions.
- **Chunk the Comprehensive Rules by rule number**, not by fixed token windows. Each
  chunk should carry its section title so it's self-contained out of context.
- **The LLM must be explicitly instructed to say "I don't know" when retrieved context
  doesn't cover the question**, rather than fall back on parametric knowledge. This is
  the main defense against confidently wrong answers on rules edge cases.
- **Cite rule numbers in every answer.** If an answer can't be traced to a specific
  rule or card ruling, treat that as a bug in retrieval, not something to paper over
  with a more confident-sounding prompt.
- At this corpus size (~3,000 rules, ~30k cards) an exact pgvector search (no
  ivfflat/HNSW index) is fast enough. Don't add ANN indexing prematurely.

## Repo structure (proposed)

```
/cmd
  /ingest        — one-shot ingestion CLI (rules + cards → Postgres)
  /server        — HTTP API server
/internal
  /rules         — Comprehensive Rules fetching + chunking
  /cards         — Scryfall client + card storage
  /retrieval     — embedding calls, pgvector queries, context assembly
  /llm           — prompt construction, LLM API client
/migrations      — SQL schema migrations
/mobile          — Expo app
docker-compose.yml
init.sql
.env.example
```

## Conventions

- Standard Go project layout, `gofmt`/`goimports` clean.
- Explicit error handling, no panics in request-handling paths.
- Config via environment variables (see `.env.example`), no hardcoded secrets.
- Keep the ingestion CLI runnable standalone (`go run ./cmd/ingest`) so it can be
  re-run manually whenever Wizards updates the Comprehensive Rules.
- Prefer boring, explicit SQL (`pgx`) over an ORM — the queries here are simple enough
  that an ORM adds indirection without real benefit.

## Current status / build order

See `ROADMAP.md`. Work through it roughly in order — each stage should be runnable and
testable on its own before moving to the next (e.g. don't build the mobile app before
the CLI RAG loop is proven to give good answers).