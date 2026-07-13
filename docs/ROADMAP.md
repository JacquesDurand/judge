# Roadmap

Work through these roughly in order. Each stage should work standalone before moving on.

## 0. Environment

- [x] `docker compose up -d` — Postgres with pgvector + pg_trgm comes up, `init.sql`
      runs automatically on first start (host port 5433 to avoid a clash)
- [x] Copy `.env.example` to `.env`, fill in embedding + LLM API keys

## 1. Ingestion (no server yet)

- [x] Fetch current Comprehensive Rules `.txt`, parse into `(rule_number,
      section_title, body)` chunks (`internal/rules`, `cmd/ingest -rules`)
- [x] Fetch Scryfall `oracle_cards` bulk file, insert into `cards` (`internal/cards`)
- [x] Fetch/derive rulings, insert into `rulings`
- [x] Embed all rule chunks + glossary entries, store in `rules.embedding` /
      `glossary.embedding` (`internal/embed`, `cmd/ingest -embed`)
- [x] Sanity check: manually query Postgres for a known rule number and confirm the
      text + embedding look right (nearest-neighbour check on 601.2a)

## 2. Prove retrieval works (CLI only)

- [x] Small script: take a question, embed it, run the pgvector similarity query,
      print the top matches (`cmd/ask`)
- [x] Try real playgroup questions (trample+deathtouch, stack/priority) and read the
      retrieved chunks — found the precise interaction rule can rank ~10th, so the
      default k was raised to 10 (docs' "prefer more context" principle)
- [x] Wire in the LLM call, print the final answer with citations (`cmd/answer`,
      `internal/rag` + `internal/llm`: Haiku preprocessing → Sonnet 5 generation)
- [x] Verified: correct grounded answer in French to a franglais question naming a
      French card ("Foudre"→Lightning Bolt); "I don't know" holds on out-of-scope
      (tournament/IPG) questions
- [ ] Keep iterating on chunking/prompt as more real questions surface

## 3. HTTP API

- [x] `POST /chat` — accepts a question, runs the full retrieval + generation flow,
      returns the answer + citations (`cmd/server`, reuses `internal/rag`; pgxpool
      for concurrency; `GET /healthz`)
- [ ] Store conversation history if you want multi-turn follow-ups (deferred)
- [x] Basic error handling (400 bad body / empty question, 405 wrong method, 502
      on upstream failure, per-request timeout, graceful shutdown)

## 4. Mobile app (Expo)

- [x] Minimal chat screen: message list + input box, calls `POST /chat` against the
      local machine via LAN IP (`mobile/`, Expo SDK 57 + TypeScript; server URL is
      a one-line config in `mobile/config.ts` for easy swap to a hosted server)
- [x] Render citations distinctly from the main answer text (rule-number + card
      chips under each answer; Scryfall attribution footer)

## 5. Polish (optional, roughly in priority order)

- [ ] Streaming responses instead of waiting for the full answer
- [ ] Card name autocomplete in the input
- [ ] Re-run ingestion on a schedule (new Comprehensive Rules version, fresh card data)
- [ ] Tappable rule citations that show the full rule text

## 6. If you ever publish (cost & abuse protection)

The API keys live only on the Go server, never in the mobile app — so the risk
isn't a stolen key, it's an open `/chat` endpoint that anyone (or too many real
users) can hammer, spending real money on every call. Defenses, in priority order:

- [ ] **Provider-side spend cap** — set a hard monthly budget limit in the OpenAI
      and Anthropic consoles. This is the ultimate backstop: even if everything
      else fails, the bill stops at a known ceiling. Worth setting up from day one,
      even for personal use.
- [ ] **Rate limiting on the server** — N requests/minute per device or IP.
- [ ] **Lightweight auth** — an anonymous per-device token so a single abuser can
      be throttled or banned without affecting everyone.
- [ ] **Cache identical questions** — many users ask the same classic rules
      questions; serving those from cache means zero LLM calls.

For personal use, only the spend cap really matters. The rest become relevant
only when/if the app is public, and are easy to add at that point.
