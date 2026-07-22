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

> The app uses only pgvector's standard `vector` type and `<=>` operator (no ANN
> index), so it also runs unchanged on any pgvector-compatible engine such as
> [VectorChord](https://docs.vectorchord.ai/). See `docs/DEPLOYMENT.md`.

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

The app asks for the **server address on first launch** and remembers it; you can
change it anytime via the gear (⚙) in the header. On a physical phone use the
server's LAN IP, e.g. `http://192.168.1.20:8090` (not `localhost` — that resolves
to the phone itself). For the simulator/web the `http://localhost:8090` default is
fine; you can override the default via `EXPO_PUBLIC_API_BASE_URL` (e.g. in a
gitignored `mobile/.env.local`) if you don't want to type it each run. The app is
pinned to **Expo SDK 54** to match a specific Expo Go build — see
`mobile/CLAUDE.md` if you upgrade.

### 7. Build a standalone APK (optional)

Expo Go is enough for development; to get an installable `.apk` (e.g. to sideload
on Android), build it in the cloud with [EAS](https://docs.expo.dev/build/introduction/) —
no local Android SDK or JDK required:

```sh
cd mobile
npx eas-cli login                                  # your Expo account
npx eas-cli init                                   # links/creates the Expo project (writes the projectId)
npx eas-cli build -p android --profile preview     # cloud build → download URL for the APK
```

The `preview` profile (see `eas.json`) produces an APK for internal distribution;
the first build offers to generate and store a signing keystore for you. Because
the server address is entered in-app, the same APK works against any server — no
per-server rebuild.

> The build enables Android cleartext traffic (`expo-build-properties` in
> `app.json`) so the app can reach a plain-HTTP server on your LAN. If you only
> ever point it at an HTTPS server, you can drop that plugin option for a stricter
> network policy.
>
> To update an already-installed APK in place, bump `android.versionCode` in
> `app.json` before rebuilding (Android refuses to install an equal/older code).

**Automated releases.** Instead of building by hand, push a tag to let CI do it:
`.github/workflows/release.yml` builds the APK on EAS and attaches it to a GitHub
Release. It needs one repository secret, `EXPO_TOKEN` (expo.dev → Account settings
→ Access tokens). Then:

```sh
# bump android.versionCode in mobile/app.json, commit, then:
git tag v1.0.1 && git push origin v1.0.1     # → Release with the APK attached
```

Running it manually from the Actions tab (workflow_dispatch) instead uploads the
APK as a run artifact, handy for testing the pipeline without cutting a release.
It never runs on pull requests, so it doesn't spend EAS credits on every push.

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

CI runs the build, `go vet`, tests, the mobile type-check, and a mobile bundle
check (`expo export`) on every pull request (see `.github/workflows/ci.yml`). A
separate, manually/tag-triggered workflow builds and publishes the APK (see
"Build a standalone APK" above).

## Status

Backend + mobile MVP working end-to-end. See [`docs/ROADMAP.md`](docs/ROADMAP.md)
for what's done and what's next (hosting the server, streaming responses,
tappable citations, packaged mobile builds).
