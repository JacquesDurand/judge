# Data sources

## Comprehensive Rules

- Landing page (always current): https://magic.wizards.com/en/rules
- Direct download links are date-stamped in the filename and change with every rules
  update (e.g. a version effective June 19, 2026 lived at a URL containing
  `MagicCompRules%2020260619.txt`). **Don't hardcode a filename long-term** — either
  scrape the current link off the rules landing page before downloading, or store the
  last-known-good URL in `.env` and update it manually when Wizards ships a new set.
- Available in `.txt`, `.pdf`, and `.docx`. Use the `.txt` version — it's the
  cleanest to parse into rule-number chunks.
- Structure: numbered rules (e.g. `100.`, `100.1`, `100.1a`), organized into sections
  (1. Game Concepts, 2. Parts of a Card, ... 9. Casual Variants), followed by a
  Glossary section at the end. Subrule letters skip `l` and `o` (avoid confusion with
  `1`/`0`) — don't assume a contiguous alphabet when parsing.
- Updates roughly every few months, tied to new set releases (each version names the
  set it launched alongside, e.g. "Teenage Mutant Ninja Turtles" or "Marvel Super
  Heroes").

## Scryfall API

- Base docs: https://scryfall.com/docs/api
- Bulk data endpoint: `https://api.scryfall.com/bulk-data` — returns metadata
  including a download URL for the `oracle_cards` bulk file (one JSON object per
  unique card, deduplicated across printings — this is what you want, not the much
  larger `all_cards` file which includes every printing).
- Each card object includes `oracle_text`, `type_line`, `mana_cost`, and an `oracle_id`
  — use `oracle_id` as your primary key so reprints don't create duplicate rows.
- Rulings are a **separate** endpoint per card (`/cards/{id}/rulings` or the bulk
  `rulings` file, also listed at the bulk-data endpoint) — not included in the card
  object itself.
- Rate limit: Scryfall asks for ~50–100ms between individual card requests, but the
  bulk data files sidestep this entirely — always prefer bulk download over per-card
  API calls for ingestion.
- Attribution requirement: Scryfall asks that apps using their data credit them
  somewhere in the UI (a simple "card data from Scryfall" footer is sufficient).

## Judge documents (optional, if you later want tournament-rules coverage)

- Infraction Procedure Guide (IPG) and Magic Tournament Rules (MTR):
  https://blogs.magicjudges.org/rules/ — useful if your playgroup ever plays anything
  resembling sanctioned events, otherwise skippable for a casual playgroup tool.
