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
	To                string `json:"to"`
	Title             string `json:"title"`
	Body              string `json:"body"`
	Data              any    `json:"data,omitempty"`
	Sound             string `json:"sound,omitempty"`
	Badge             int    `json:"badge,omitempty"`
	Priority          string `json:"priority,omitempty"`
	TTL               int    `json:"ttl,omitempty"`
	ChannelID         string `json:"channelId,omitempty"`
	InterruptionLevel string `json:"interruptionLevel,omitempty"`
	CollapseID        string `json:"collapseId,omitempty"`
	ThreadID          string `json:"threadId,omitempty"`
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
	httpClient *http.Client
	pushToken  string
	pushAPIURL string
	receiptURL string
}

func New(expoPushToken string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		pushToken:  expoPushToken,
		pushAPIURL: "https://exp.host/--/api/v2/push/send",
		receiptURL: "https://exp.host/--/api/v2/push/getReceipts",
	}
}

// MessageSpec is the one place campaign and alert pushes assemble an Expo
// message, so data and image handling cannot drift between synchronous and
// scheduled sends.
type MessageSpec struct {
	To                string
	Title             string
	Body              string
	Data              map[string]any
	ImageURL          string
	Sound             string
	Badge             int
	Priority          string
	TTL               int
	ChannelID         string
	InterruptionLevel string
	CollapseID        string
	ThreadID          string
}

func BuildMessage(spec MessageSpec) ExpoPushMessage {
	data := spec.Data
	if spec.ImageURL != "" {
		copied := make(map[string]any, len(data)+1)
		for k, v := range data {
			copied[k] = v
		}
		copied["imageUrl"] = spec.ImageURL
		data = copied
	}
	sound := spec.Sound
	if sound == "" {
		sound = "default"
	}
	return ExpoPushMessage{
		To: spec.To, Title: spec.Title, Body: spec.Body, Data: data, Sound: sound,
		Badge: spec.Badge, Priority: spec.Priority, TTL: spec.TTL,
		ChannelID: spec.ChannelID, InterruptionLevel: spec.InterruptionLevel,
		CollapseID: spec.CollapseID, ThreadID: spec.ThreadID,
	}
}

type SendResult struct {
	Sent           int
	Failed         int
	Errors         []string
	TokenErrors    map[string]string
	TicketForToken map[string]string
}

// Receipt is the exported receipt shape used by the alerts worker.
type Receipt struct {
	Status  string
	Message string
	Code    string
}

// GetReceiptMap returns receipts keyed by Expo ticket ID.
func (c *Client) GetReceiptMap(ctx context.Context, ticketIDs []string) (map[string]Receipt, error) {
	resp, err := c.GetReceipts(ctx, ticketIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Receipt, len(resp.Data))
	for id, receipt := range resp.Data {
		code := ""
		if details, ok := receipt.Details.(map[string]any); ok {
			if v, ok := details["error"].(string); ok {
				code = v
			}
		}
		out[id] = Receipt{Status: receipt.Status, Message: receipt.Message, Code: code}
	}
	return out, nil
}

func (c *Client) SendMessages(ctx context.Context, messages []ExpoPushMessage) (*SendResult, error) {
	result := &SendResult{TokenErrors: make(map[string]string), TicketForToken: make(map[string]string)}

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
				if i < len(chunk) && r.ID != "" {
					result.TicketForToken[chunk[i].To] = r.ID
				}
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
