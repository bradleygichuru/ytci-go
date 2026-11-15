package alerts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"
	"time"
)

type FCDOAdvice struct {
	PublicUpdatedAt   time.Time
	ChangeDescription string
	SafetyTitle       string
	SafetyText        string
	ContentHash       string
}

type fcdoDocument struct {
	PublicUpdatedAt string `json:"public_updated_at"`
	Details         struct {
		ChangeDescription string `json:"change_description"`
		Parts             []struct {
			Slug  string `json:"slug"`
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"parts"`
	} `json:"details"`
}

var htmlTagRE = regexp.MustCompile(`(?s)<[^>]*>`)

func ParseFCDO(r io.Reader) (*FCDOAdvice, error) {
	var doc fcdoDocument
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse FCDO JSON: %w", err)
	}
	updated, err := time.Parse(time.RFC3339, doc.PublicUpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("FCDO public_updated_at: %w", err)
	}
	advice := &FCDOAdvice{
		PublicUpdatedAt:   updated.UTC(),
		ChangeDescription: strings.TrimSpace(doc.Details.ChangeDescription),
		SafetyTitle:       "Safety and security",
	}
	for _, part := range doc.Details.Parts {
		if part.Slug != "safety-and-security" {
			continue
		}
		advice.SafetyTitle = strings.TrimSpace(part.Title)
		advice.SafetyText = htmlToText(part.Body)
		break
	}
	if advice.SafetyText == "" {
		return nil, fmt.Errorf("FCDO safety-and-security section missing")
	}
	sum := sha256.Sum256([]byte(advice.SafetyText))
	advice.ContentHash = hex.EncodeToString(sum[:])
	return advice, nil
}

func htmlToText(in string) string {
	in = html.UnescapeString(in)
	// Preserve paragraph boundaries before stripping tags.
	in = regexp.MustCompile(`(?i)</p\s*>`).ReplaceAllString(in, "\n\n")
	in = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(in, "\n")
	in = htmlTagRE.ReplaceAllString(in, " ")
	lines := strings.Split(in, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n\n")
}
