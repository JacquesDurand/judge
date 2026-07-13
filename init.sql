-- Extensions
CREATE EXTENSION IF NOT EXISTS vector;    -- semantic search over rules text
CREATE EXTENSION IF NOT EXISTS pg_trgm;   -- fuzzy card name matching

-- Comprehensive Rules, chunked by rule number
CREATE TABLE IF NOT EXISTS rules (
  id            SERIAL PRIMARY KEY,
  rule_number   TEXT NOT NULL,        -- e.g. '601.2a'
  section_title TEXT,                 -- e.g. 'Casting Spells'
  body          TEXT NOT NULL,
  embedding     vector(1536),         -- adjust dimension to match your embedding model
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS rules_rule_number_idx ON rules (rule_number);

-- Glossary terms from the Comprehensive Rules (keyword definitions)
CREATE TABLE IF NOT EXISTS glossary (
  id         SERIAL PRIMARY KEY,
  term       TEXT NOT NULL,
  definition TEXT NOT NULL,
  embedding  vector(1536)
);
CREATE UNIQUE INDEX IF NOT EXISTS glossary_term_idx ON glossary (term);

-- Cards, from Scryfall bulk data. Looked up by name (trigram), not by vector search.
CREATE TABLE IF NOT EXISTS cards (
  oracle_id   UUID PRIMARY KEY,
  name        TEXT NOT NULL,
  mana_cost   TEXT,
  type_line   TEXT,
  oracle_text TEXT,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS cards_name_trgm_idx ON cards USING gin (name gin_trgm_ops);

-- Official rulings per card, from Scryfall's rulings endpoint
CREATE TABLE IF NOT EXISTS rulings (
  id          SERIAL PRIMARY KEY,
  oracle_id   UUID NOT NULL REFERENCES cards (oracle_id) ON DELETE CASCADE,
  source      TEXT,                 -- Scryfall rulings feed: 'wotc' or 'scryfall'
  published_at DATE,
  comment     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS rulings_oracle_id_idx ON rulings (oracle_id);

-- Conversation history, if you want multi-turn context per user/session
CREATE TABLE IF NOT EXISTS conversations (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS messages (
  id              SERIAL PRIMARY KEY,
  conversation_id UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
  role            TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
  content         TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS messages_conversation_id_idx ON messages (conversation_id);
