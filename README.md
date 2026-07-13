# MTG Rules Assistant

A RAG-powered chatbot that answers **Magic: The Gathering** rules questions,
grounded in the official Comprehensive Rules and Scryfall card data, with rule
numbers cited so every answer can be double-checked. Built as a personal project
to learn RAG and mobile development.

Ask in plain language (English, French, or a mix) — the assistant retrieves the
relevant rules and card text, then answers **only** from that context, citing the
rule numbers it used and saying "I don't know" when the rules don't cover it.

> Card data from [Scryfall](https://scryfall.com). Rules text © Wizards of the Coast.

## How it works

Two separate flows (see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for detail):

1. **Ingestion** (offline, one-shot): fetch the Comprehensive Rules `.txt` and
   Scryfall bulk data → chunk the rules by rule number → embed rules + glossary
   → store in Postgres (`pgvector` for semantic search, `pg_trgm` for fuzzy card
   lookup).
2. **Query** (per question): a cheap Haiku pass normalises the question
   (French keyword terms → canonical English, card names → English) → embed it →
   `pgvector` similarity search over rules + glossary, `pg_trgm` lookup for named
   cards → assemble context → Claude Sonnet answers with citations, in the user's
   language.

**Tech:** Go (stdlib `net/http` + `pgx`), Postgres 16 + pgvector + pg_trgm,
OpenAI `text-embedding-3-small` for embeddings, Claude (Haiku + Sonnet) for
generation, React Native / Expo for the mobile client.

## Prerequisites

- Go (version in `go.mod`)
- Docker + Docker Compose (for Postgres)
- An OpenAI API key (embeddings) and an Anthropic API key (generation)
- For the mobile app: Node.js 18+, and [Expo Go](https://expo.dev/go) on your phone

## Run it locally

### 1. Database

```sh
docker compose up -d          # Postgres + pgvector + pg_trgm; init.sql runs on first start
```

The host port is **5433** (mapped to the container's 5432) to avoid clashing with
a local Postgres on 5432 — see `compose.yaml`.

### 2. Configuration

```sh
cp .env.example .env          # then fill in EMBEDDING_API_KEY and LLM_API_KEY
```

`.env` is gitignored — never commit your keys.

### 3. Ingest the data

```sh
go run ./cmd/ingest           # rules + glossary + Scryfall cards + rulings + embeddings
```

Re-runnable at any time (upserts). Flags let you run one stage: `-rules`,
`-cards`, `-embed`. Only `-embed` needs the OpenAI key; the rest need none.

### 4. Try it from the CLI

```sh
# Inspect what retrieval surfaces (no LLM call):
go run ./cmd/ask "how does trample interact with deathtouch?"

# Full grounded answer with citations:
go run ./cmd/answer -v "Si mon adversaire lance Foudre sur ma créature, puis-je la sauver ?"
```

### 5. HTTP server

```sh
go run ./cmd/server           # listens on :8090 (configurable via PORT)
curl localhost:8090/healthz
curl -X POST localhost:8090/chat -H 'content-type: application/json' \
  -d '{"question":"how does first strike work?"}'
```

### 6. Mobile app (Expo)

```sh
cd mobile
npx expo start                # scan the QR code with Expo Go (same Wi-Fi as this machine)
```

Point the app at your server by editing the one line in `mobile/config.ts`
(default: your machine's LAN IP on port 8090). The app is pinned to **Expo SDK 54**
to match a specific Expo Go build — see `mobile/` if you upgrade.

## Repository layout

```
cmd/
  ingest        one-shot ingestion CLI (rules, cards, embeddings)
  ask           retrieval probe (prints the chunks a question retrieves)
  answer        full RAG pipeline, prints a grounded answer
  server        HTTP API (POST /chat, GET /healthz)
internal/
  config        .env / env loading
  rules         Comprehensive Rules parser (rules + glossary)
  cards         Scryfall bulk client (streaming, double-faced cards)
  embed         OpenAI embeddings client + pgvector formatting
  retrieval     pgvector search + trigram card resolution
  llm           Anthropic client: Haiku preprocessing + Sonnet generation
  mtg           deterministic FR→EN keyword normalisation
  rag           orchestration (shared by CLI and server)
mobile/         Expo React Native chat app
docs/           architecture, data sources, roadmap
init.sql        database schema
compose.yaml    Postgres service
```

## Tests

```sh
go test ./...                 # Go unit tests (parser, keyword normalisation)
cd mobile && npx tsc --noEmit # type-check the app
```

CI runs the build, `go vet`, tests, and the mobile type-check on every pull
request (see `.github/workflows/ci.yml`).

## Status

Backend + mobile MVP working end-to-end. See [`docs/ROADMAP.md`](docs/ROADMAP.md)
for what's done and what's next (hosting the server, streaming responses,
tappable citations, packaged mobile builds).
