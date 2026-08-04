package places

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const baseURL = "https://places.googleapis.com/v1/"

type Client struct {
	apiKey   string
	http     *http.Client
	baseURL  string
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:   apiKey,
		baseURL:  baseURL,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) post(ctx context.Context, path string, body interface{}, fieldMask string) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("places: marshal request: %w", err)
	}

	return c.doWithRetry(ctx, "POST", path, bytes.NewReader(payload), fieldMask)
}

func (c *Client) get(ctx context.Context, path string, fieldMask string) ([]byte, error) {
	return c.doWithRetry(ctx, "GET", path, nil, fieldMask)
}

func (c *Client) doWithRetry(ctx context.Context, method, path string, body io.Reader, fieldMask string) ([]byte, error) {
	url := c.baseURL + path

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt*attempt) * time.Second
			slog.Warn("places: retrying request", "attempt", attempt+1, "path", path, "delay", delay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			return nil, fmt.Errorf("places: create request: %w", err)
		}
		req.Header.Set("X-Goog-Api-Key", c.apiKey)
		req.Header.Set("X-Goog-FieldMask", fieldMask)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("places: execute request: %w", err)
			continue
		}

		respBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("places: read response: %w", err)
			continue
		}

		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			lastErr = fmt.Errorf("places: %s returned %d: %s", path, resp.StatusCode, string(respBytes))
			continue
		}

		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("places: %s returned %d: %s", path, resp.StatusCode, string(respBytes))
		}

		return respBytes, nil
	}

	return nil, fmt.Errorf("places: max retries exceeded: %w", lastErr)
}
