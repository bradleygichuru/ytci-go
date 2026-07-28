package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ExpoPushMessage struct {
	To       string `json:"to"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Data     any    `json:"data,omitempty"`
	Sound    string `json:"sound,omitempty"`
	Badge    int    `json:"badge,omitempty"`
	Priority string `json:"priority,omitempty"`
	TTL      int    `json:"ttl,omitempty"`
}

type expoPushResponse struct {
	Data   []expoPushResult `json:"data"`
	Errors []any            `json:"errors"`
}

type expoPushResult struct {
	Status  string `json:"status"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Details any    `json:"details,omitempty"`
}

type expoReceiptResponse struct {
	Data   map[string]expoReceipt `json:"data"`
	Errors []any                  `json:"errors"`
}

type expoReceipt struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Details any    `json:"details,omitempty"`
}

type Client struct {
	httpClient  *http.Client
	pushToken   string
	pushAPIURL  string
	receiptURL  string
}

func New(expoPushToken string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		pushToken:  expoPushToken,
		pushAPIURL: "https://exp.host/--/api/v2/push/send",
		receiptURL: "https://exp.host/--/api/v2/push/getReceipts",
	}
}

type SendResult struct {
	Sent        int
	Failed      int
	Errors      []string
	TokenErrors map[string]string
}

func (c *Client) SendMessages(ctx context.Context, messages []ExpoPushMessage) (*SendResult, error) {
	result := &SendResult{TokenErrors: make(map[string]string)}

	chunks := chunkMessages(messages, 100)
	for _, chunk := range chunks {
		body, _ := json.Marshal(chunk)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.pushAPIURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.pushToken != "" {
			req.Header.Set("Authorization", "Bearer "+c.pushToken)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send push: %w", err)
		}

		var pushResp expoPushResponse
		if err := json.NewDecoder(resp.Body).Decode(&pushResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode push response: %w", err)
		}
		resp.Body.Close()

		for i, r := range pushResp.Data {
			if r.Status == "ok" {
				result.Sent++
			} else {
				result.Failed++
				result.Errors = append(result.Errors, r.Message)
				if r.Message == "DeviceNotRegistered" && i < len(chunk) {
					result.TokenErrors[chunk[i].To] = r.Message
				}
			}
		}
	}

	return result, nil
}

func (c *Client) GetReceipts(ctx context.Context, ticketIDs []string) (*expoReceiptResponse, error) {
	body, _ := json.Marshal(map[string][]string{"ids": ticketIDs})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.receiptURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create receipt request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.pushToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.pushToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get receipts: %w", err)
	}
	defer resp.Body.Close()

	var receiptResp expoReceiptResponse
	if err := json.NewDecoder(resp.Body).Decode(&receiptResp); err != nil {
		return nil, fmt.Errorf("decode receipts: %w", err)
	}

	return &receiptResp, nil
}

func ResolveActiveTokens(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT token FROM push_tokens WHERE is_active = true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		tokens = append(tokens, t)
	}
	return tokens, nil
}

func chunkMessages(messages []ExpoPushMessage, chunkSize int) [][]ExpoPushMessage {
	var chunks [][]ExpoPushMessage
	for i := 0; i < len(messages); i += chunkSize {
		end := i + chunkSize
		if end > len(messages) {
			end = len(messages)
		}
		chunks = append(chunks, messages[i:end])
	}
	return chunks
}
