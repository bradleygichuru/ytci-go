package ocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrDisabled = errors.New("ocr disabled")

// Provider extracts text from a receipt image.
type Provider interface {
	Extract(ctx context.Context, image []byte, contentType string) (Result, error)
}

type Result struct {
	Merchant   string  `json:"merchant,omitempty"`
	Date       string  `json:"date,omitempty"`
	Total      float64 `json:"total,omitempty"`
	Currency   string  `json:"currency,omitempty"`
	Confidence float64 `json:"confidence"`
	RawText    string  `json:"rawText,omitempty"`
}

type Client struct {
	apiKey   string
	endpoint string
	http     *http.Client
}

func NewClient(apiKey, endpoint string) *Client {
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "https://eu-vision.googleapis.com"
	}
	return &Client{
		apiKey:   apiKey,
		endpoint: strings.TrimRight(endpoint, "/"),
		http:     &http.Client{Timeout: 20 * time.Second},
	}
}

type visionRequest struct {
	Requests []visionRequestItem `json:"requests"`
}

type visionRequestItem struct {
	Image        visionImage         `json:"image"`
	Features     []visionFeature     `json:"features"`
	ImageContext *visionImageContext `json:"imageContext,omitempty"`
}

type visionImageContext struct {
	LanguageHints []string `json:"languageHints,omitempty"`
}

type visionImage struct {
	Content string `json:"content"`
}

type visionFeature struct {
	Type string `json:"type"`
}

type visionResponse struct {
	Responses []struct {
		FullTextAnnotation *struct {
			Text string `json:"text"`
		} `json:"fullTextAnnotation"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"responses"`
}

func (c *Client) Extract(ctx context.Context, image []byte, contentType string) (Result, error) {
	if c == nil || c.apiKey == "" {
		return Result{}, ErrDisabled
	}
	if len(image) == 0 {
		return Result{}, errors.New("empty image")
	}

	body, err := json.Marshal(visionRequest{
		Requests: []visionRequestItem{{
			Image:        visionImage{Content: base64.StdEncoding.EncodeToString(image)},
			Features:     []visionFeature{{Type: "DOCUMENT_TEXT_DETECTION"}},
			ImageContext: &visionImageContext{LanguageHints: []string{"en", "sw"}},
		}},
	})
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/v1/images:annotate?key="+url.QueryEscape(c.apiKey), bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("vision status %d: %s", resp.StatusCode, string(raw))
	}

	var vr visionResponse
	if err := json.Unmarshal(raw, &vr); err != nil {
		return Result{}, err
	}
	if len(vr.Responses) == 0 {
		return Result{}, errors.New("vision returned no responses")
	}
	if vr.Responses[0].Error != nil && vr.Responses[0].Error.Message != "" {
		return Result{}, errors.New(vr.Responses[0].Error.Message)
	}
	text := ""
	if vr.Responses[0].FullTextAnnotation != nil {
		text = vr.Responses[0].FullTextAnnotation.Text
	}
	if strings.TrimSpace(text) == "" {
		return Result{}, errors.New("no text found in receipt")
	}
	return ParseReceipt(text), nil
}

var (
	amountRe   = regexp.MustCompile(`(?i)(?:kes|ksh|kshs|usd|eur|gbp|tzs|ugx|zar)?\s*([0-9][0-9,]*\.[0-9]{2}|[0-9][0-9,]{1,}|[0-9]+)`)
	currencyRe = regexp.MustCompile(`(?i)\b(KES|KSH|KSHS|USD|EUR|GBP|TZS|UGX|ZAR)\b`)
	dateRe     = regexp.MustCompile(`(?i)(\d{1,2})[/-](\d{1,2})[/-](\d{2,4})|(\d{4})-(\d{2})-(\d{2})|(\d{1,2})\s+(JAN|FEB|MAR|APR|MAY|JUN|JUL|AUG|SEP|OCT|NOV|DEC)[A-Z]*\s+(\d{4})`)
)

var merchantSkip = []string{
	"receipt", "tax invoice", "invoice", "date", "time", "total", "amount",
	"vat", "pin", "tel", "phone", "thank you", "welcome", "cashier", "till",
	"terminal", "transaction", "customer copy", "duplicate", "original",
	"served by", "table", "order", "ksh", "kes",
}

// ParseReceipt is a deterministic Kenyan/Swahili receipt parser. It is
// intentionally conservative: it returns what it can prove from the text and
// leaves confidence low when the total, date, or merchant is missing.
func ParseReceipt(text string) Result {
	result := Result{RawText: text}
	lines := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return result
	}

	result.Merchant = findMerchant(lines)
	result.Date = findDate(text)
	result.Currency = findCurrency(text)
	result.Total = findTotal(lines)

	switch {
	case result.Merchant != "" && result.Date != "" && result.Total > 0:
		result.Confidence = 1.0
	case result.Total > 0 && result.Currency != "":
		result.Confidence = 0.8
	case result.Total > 0:
		result.Confidence = 0.6
	default:
		result.Confidence = 0.3
	}
	return result
}

func findMerchant(lines []string) string {
	for _, line := range lines {
		lower := strings.ToLower(line)
		skip := false
		for _, word := range merchantSkip {
			if lower == word || strings.HasPrefix(lower, word+" ") || strings.HasPrefix(lower, word+":") {
				skip = true
				break
			}
		}
		if skip || len(line) < 2 || len(line) > 60 {
			continue
		}
		letters := 0
		for _, r := range line {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				letters++
			}
		}
		if letters >= 3 {
			return line
		}
	}
	return ""
}

func findDate(text string) string {
	m := dateRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		day, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		year, _ := strconv.Atoi(m[3])
		if year < 100 {
			year += 2000
		}
		return validDate(year, month, day)
	}
	if m[4] != "" {
		year, _ := strconv.Atoi(m[4])
		month, _ := strconv.Atoi(m[5])
		day, _ := strconv.Atoi(m[6])
		return validDate(year, month, day)
	}
	if m[7] != "" {
		day, _ := strconv.Atoi(m[7])
		year, _ := strconv.Atoi(m[9])
		month := monthNumber(strings.ToUpper(m[8][:3]))
		return validDate(year, month, day)
	}
	return ""
}

func validDate(year, month, day int) string {
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return ""
	}
	return t.Format("2006-01-02")
}

func monthNumber(m string) int {
	switch m {
	case "JAN":
		return 1
	case "FEB":
		return 2
	case "MAR":
		return 3
	case "APR":
		return 4
	case "MAY":
		return 5
	case "JUN":
		return 6
	case "JUL":
		return 7
	case "AUG":
		return 8
	case "SEP":
		return 9
	case "OCT":
		return 10
	case "NOV":
		return 11
	case "DEC":
		return 12
	}
	return 0
}

func findCurrency(text string) string {
	if m := currencyRe.FindStringSubmatch(text); m != nil {
		cur := strings.ToUpper(m[1])
		if cur == "KSH" || cur == "KSHS" {
			return "KES"
		}
		return cur
	}
	switch {
	case strings.Contains(text, "$"):
		return "USD"
	case strings.Contains(text, "€"):
		return "EUR"
	case strings.Contains(text, "£"):
		return "GBP"
	}
	return "KES"
}

func findTotal(lines []string) float64 {
	var best float64
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "total") || strings.Contains(lower, "amount due") ||
			strings.Contains(lower, "to pay") || strings.Contains(lower, "grand") ||
			strings.Contains(lower, "subtotal") {
			for _, m := range amountRe.FindAllStringSubmatch(line, -1) {
				if len(m) < 2 {
					continue
				}
				if v := parseAmount(m[1]); v > best {
					best = v
				}
			}
		}
	}
	if best > 0 {
		return best
	}
	for _, line := range lines {
		for _, m := range amountRe.FindAllStringSubmatch(line, -1) {
			if len(m) < 2 {
				continue
			}
			if v := parseAmount(m[1]); v > best {
				best = v
			}
		}
	}
	return best
}

func parseAmount(raw string) float64 {
	raw = strings.ReplaceAll(raw, ",", "")
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	return v
}
