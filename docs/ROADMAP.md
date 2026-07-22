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

- [x] Streaming responses instead of waiting for the full answer (`POST /chat/stream`
      NDJSON: one `meta` line with citations, then `delta` lines, then `done`;
      `internal/llm` GenerateStream + `internal/rag` AnswerStream; the app consumes
      it via XHR. Generation prompt switched to plain text for clean mobile rendering.)
- [x] Card name autocomplete in the input (`GET /cards/search`; a suggestion bar
      above the input reacts to the word being typed, tap inserts the full name)
- [ ] Re-run ingestion on a schedule (new Comprehensive Rules version, fresh card
      data) — best done as a k8s CronJob once the server is hosted
- [x] Tappable rule citations that show the full rule text (`GET /rules/{number}` +
      a modal in the app; tap a rule-number chip to read the full rule)
- [x] Standalone APK build pipeline via EAS (`mobile/eas.json`: `preview` → APK,
      `production` → AAB, `development` → dev client). App identity set; the actual
      cloud build is a user step (`npx eas-cli build -p android --profile preview`).
      See README §7.
- [x] In-app server configuration: the user enters the server address on first
      launch (and via the header gear), persisted with AsyncStorage
      (`mobile/serverUrl.ts`), with a "Test" button that pings `/healthz`. One
      build works against any server; Android cleartext enabled so a LAN
      `http://` server is reachable.
- [x] CI: PRs also verify the mobile bundle (`expo export`); a separate tag/manual
      `release.yml` builds the APK on EAS and attaches it to a GitHub Release
      (needs the `EXPO_TOKEN` secret). Never builds the APK on PRs.

## 6. If you ever publish (cost & abuse protection)

The API keys live only on the Go server, never in the mobile app — so the risk
isn't a stolen key, it's an open `/chat` endpoint that anyone (or too many real
users) can hammer, spending real money on every call. Defenses, in priority order:

- [ ] **Provider-side spend cap** — set a hard monthly budget limit in the OpenAI
      and Anthropic consoles. This is the ultimate backstop: even if everything
      else fails, the bill stops at a known ceiling. Worth setting up from day one,
      even for personal use.
- [x] **Rate limiting on the server** — per-client token bucket (`internal/ratelimit`):
      `RATE_LIMIT_RPM` sustained req/min + `RATE_LIMIT_BURST`, keyed on the
      authenticated subject (JWT `sub`) or client IP; returns `429` +
      `Retry-After`. Wired inside the auth middleware in `cmd/server`.
- [x] **Auth (server side)** — OIDC bearer-token validation middleware
      (`internal/auth`): when `OIDC_ISSUER`/`OIDC_AUDIENCE` are set, every route
      except `/healthz` requires a valid JWT, verified offline against the
      provider's JWKS. Works with any standard provider (Authentik, Keycloak,
      Auth0). Open (with a startup warning) when unset, for local dev.
- [ ] **Auth (app side)** — Authorization Code + PKCE login flow to obtain the
      token. Requires a custom mobile build (a redirect scheme), so it couples
      with the packaged-build step, not Expo Go.
- [ ] **Cache identical questions** — many users ask the same classic rules
      questions; serving those from cache means zero LLM calls.

For personal use, only the spend cap really matters. The rest become relevant
only when/if the app is public, and are easy to add at that point.
