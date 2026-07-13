// Package cards fetches Magic card data and rulings from Scryfall's bulk-data
// feeds and normalises them into the shapes stored in Postgres.
//
// Bulk files are large (oracle_cards ~180MB), so the JSON array is decoded
// element-by-element off the response stream rather than read into memory whole.
package cards

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const userAgent = "judge-mtg-rag/0.1 (personal learning project)"

// Card is the trimmed subset of a Scryfall card object we keep. Cards are
// looked up by fuzzy name (pg_trgm), so we store the printable text fields only.
type Card struct {
	OracleID   string
	Name       string
	ManaCost   string
	TypeLine   string
	OracleText string
}

// Ruling is one official ruling, tied to a card by OracleID.
type Ruling struct {
	OracleID    string
	Source      string
	PublishedAt *time.Time
	Comment     string
}

// BulkURI queries the Scryfall bulk-data catalogue and returns the current
// download URL for the given bulk type (e.g. "oracle_cards", "rulings"). These
// URLs are date-stamped and change with each daily rebuild, so they must be
// looked up rather than hardcoded.
func BulkURI(ctx context.Context, catalogueURL, bulkType string) (string, error) {
	resp, err := get(ctx, catalogueURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var cat struct {
		Data []struct {
			Type        string `json:"type"`
			DownloadURI string `json:"download_uri"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cat); err != nil {
		return "", fmt.Errorf("decode bulk catalogue: %w", err)
	}
	for _, b := range cat.Data {
		if b.Type == bulkType {
			return b.DownloadURI, nil
		}
	}
	return "", fmt.Errorf("bulk type %q not found in catalogue", bulkType)
}

// rawCard mirrors the Scryfall card JSON. Double-faced layouts carry their text
// in card_faces rather than at the top level.
type rawCard struct {
	OracleID   string `json:"oracle_id"`
	Name       string `json:"name"`
	ManaCost   string `json:"mana_cost"`
	TypeLine   string `json:"type_line"`
	OracleText string `json:"oracle_text"`
	CardFaces  []struct {
		ManaCost   string `json:"mana_cost"`
		TypeLine   string `json:"type_line"`
		OracleText string `json:"oracle_text"`
	} `json:"card_faces"`
}

func (r rawCard) normalise() Card {
	c := Card{
		OracleID:   r.OracleID,
		Name:       r.Name,
		ManaCost:   r.ManaCost,
		TypeLine:   r.TypeLine,
		OracleText: r.OracleText,
	}
	// Double-faced / split cards: assemble the missing top-level fields from the
	// individual faces, joined with " // " (mana, type) or a face separator.
	if len(r.CardFaces) > 0 {
		var texts, manas, types []string
		for _, f := range r.CardFaces {
			if f.OracleText != "" {
				texts = append(texts, f.OracleText)
			}
			if f.ManaCost != "" {
				manas = append(manas, f.ManaCost)
			}
			if f.TypeLine != "" {
				types = append(types, f.TypeLine)
			}
		}
		if c.OracleText == "" {
			c.OracleText = strings.Join(texts, "\n//\n")
		}
		if c.ManaCost == "" {
			c.ManaCost = strings.Join(manas, " // ")
		}
		if c.TypeLine == "" {
			c.TypeLine = strings.Join(types, " // ")
		}
	}
	return c
}

// FetchCards streams the oracle_cards bulk file and returns one Card per unique
// oracle_id. Entries without an oracle_id (art series, some tokens) are skipped,
// and duplicate oracle_ids are collapsed (the file is already deduplicated, but
// we guard against it so a later COPY doesn't hit a primary-key conflict).
func FetchCards(ctx context.Context, url string) ([]Card, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	if _, err := dec.Token(); err != nil { // consume opening '['
		return nil, fmt.Errorf("read cards array: %w", err)
	}

	seen := make(map[string]bool)
	var out []Card
	for dec.More() {
		var r rawCard
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("decode card: %w", err)
		}
		if r.OracleID == "" || seen[r.OracleID] {
			continue
		}
		seen[r.OracleID] = true
		out = append(out, r.normalise())
	}
	return out, nil
}

type rawRuling struct {
	OracleID    string `json:"oracle_id"`
	Source      string `json:"source"`
	PublishedAt string `json:"published_at"`
	Comment     string `json:"comment"`
}

// FetchRulings streams the rulings bulk file. published_at is parsed to a date;
// an empty or malformed value becomes NULL.
func FetchRulings(ctx context.Context, url string) ([]Ruling, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("read rulings array: %w", err)
	}

	var out []Ruling
	for dec.More() {
		var r rawRuling
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("decode ruling: %w", err)
		}
		if r.OracleID == "" {
			continue
		}
		out = append(out, Ruling{
			OracleID:    r.OracleID,
			Source:      r.Source,
			PublishedAt: parseDate(r.PublishedAt),
			Comment:     r.Comment,
		})
	}
	return out, nil
}

func parseDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return resp, nil
}
