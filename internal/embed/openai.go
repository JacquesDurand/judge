// Package embed wraps the embedding provider (OpenAI text-embedding-3-small).
// It is shared by two callers that must use the SAME model so their vectors are
// comparable: the ingestion CLI (embeds the rules/glossary corpus) and, later,
// the query server (embeds the user's question).
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const endpoint = "https://api.openai.com/v1/embeddings"

// Client calls the OpenAI embeddings API.
type Client struct {
	apiKey string
	model  string
	http   *http.Client
}

func New(apiKey, model string) *Client {
	return &Client{
		apiKey: apiKey,
		model:  model,
		http:   &http.Client{Timeout: 60 * time.Second},
	}
}

type request struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type response struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Embed returns one vector per input, in the same order as the inputs. It
// retries transient failures (429 / 5xx) with a short backoff.
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(request{Model: c.model, Input: inputs})
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}

		vecs, retryable, err := c.embedOnce(ctx, body, len(inputs))
		if err == nil {
			return vecs, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, fmt.Errorf("embed: giving up after retries: %w", lastErr)
}

// embedOnce performs a single request. The bool reports whether the error is
// worth retrying (rate limit / server error / network).
func (c *Client) embedOnce(ctx context.Context, body []byte, want int) ([][]float32, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, err // network error — retry
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, retryable, fmt.Errorf("embeddings API: status %d: %s", resp.StatusCode, raw)
	}

	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, false, fmt.Errorf("decode embeddings response: %w", err)
	}
	if r.Error != nil {
		return nil, false, fmt.Errorf("embeddings API error: %s", r.Error.Message)
	}
	if len(r.Data) != want {
		return nil, false, fmt.Errorf("embeddings API returned %d vectors, expected %d", len(r.Data), want)
	}

	// Place each vector at its reported index so order matches the inputs.
	out := make([][]float32, want)
	for _, d := range r.Data {
		if d.Index < 0 || d.Index >= want {
			return nil, false, fmt.Errorf("embeddings API returned out-of-range index %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, false, nil
}
