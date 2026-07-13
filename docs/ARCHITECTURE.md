# Architecture

## Two flows

**Offline ingestion** (run manually or on a schedule — not per request):

```
Comprehensive Rules text ─┐
                           ├─► Ingestion job (Go) ─► Postgres + pgvector
Scryfall bulk data ───────┘
```

**Online query** (per user question):

```
Mobile app
   │  question
   ▼
Go API server ── embeds question, extracts any card names mentioned
   │
   ▼
Postgres + pgvector ── top-k rule chunks (semantic search) + card text (trigram lookup)
   │
   ▼
LLM API call ── answers only from retrieved context, cites rule numbers
   │
   ▼
Mobile app ── streamed answer + citations
```

## Schema

See `init.sql` for the runnable version. Summary:

- `rules` — one row per Comprehensive Rules sub-rule, with an embedding column.
  Chunked by rule number (e.g. `601.2a`), not fixed token windows, so each chunk stays
  self-contained.
- `glossary` — keyword definitions from the back of the Comprehensive Rules, also
  embedded, since a lot of rules questions are really "what does X mean."
- `cards` — from Scryfall bulk data. Looked up by trigram similarity on `name`, not
  vector search — a name lookup should return the exact card, not similar ones.
- `rulings` — official per-card rulings from Scryfall, joined in whenever a specific
  card is mentioned.
- `conversations` / `messages` — optional, for multi-turn context within a session.

## Retrieval logic (per query)

1. Try to detect card names in the user's message (either via `pg_trgm` similarity
   search against `cards.name`, or by asking the LLM itself to extract candidate names
   as a cheap preprocessing pass).
2. Embed the user's question with the same embedding model used during ingestion.
3. Run a cosine-similarity search (`embedding <=> query_embedding`) against `rules`
   and `glossary`, top 5–8 results. No ANN index needed at this corpus size — a brute
   force scan over ~3,000 rows is fast.
4. For any detected cards, pull `oracle_text` + associated `rulings`.
5. Assemble a context block: rule chunks (with rule numbers), glossary entries, card
   text, and rulings.
6. Call the LLM with a system prompt instructing it to answer only from this context,
   cite rule numbers, and explicitly say when the context doesn't cover the question.

## Why cards aren't retrieved via vector search

Semantic similarity on card text would surface cards that are *conceptually similar*
to what the user typed, not the specific card they named. "Lightning Bolt" needs to
resolve to the one card named Lightning Bolt (fuzzy-matched for typos via trigram),
not to whatever burn spell embeds closest to it.

## Prompting notes

The single biggest failure mode for a rules bot is confident wrong answers on stack/
layers/replacement-effect edge cases, because those questions require chaining several
specific rule numbers correctly — general knowledge from the LLM's training data is
not reliable here, even though it sounds plausible. Mitigate by:

- Explicitly instructing "if the provided context doesn't answer this, say so" in the
  system prompt.
- Requiring a cited rule number for any specific ruling claim.
- Preferring more retrieved context (a few extra rule chunks) over fewer, since the
  cost of missing the relevant rule is worse than a slightly longer prompt.
