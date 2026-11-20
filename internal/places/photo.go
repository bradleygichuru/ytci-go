package places

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

var photoRefPattern = regexp.MustCompile(`^places/[A-Za-z0-9_-]+/photos/[A-Za-z0-9_-]+$`)

// ValidPhotoRef reports whether ref is a Google Places photo resource name.
// Keeping this strict prevents the photo proxy from becoming an SSRF surface.
func ValidPhotoRef(ref string) bool {
	return photoRefPattern.MatchString(ref)
}

// PhotoMedia fetches a Google Places photo through the server-side API key.
// Callers should stream the bytes rather than persisting them.
func (c *Client) PhotoMedia(ctx context.Context, ref string, maxWidthPx int) ([]byte, string, error) {
	if c == nil || c.apiKey == "" {
		return nil, "", fmt.Errorf("places: photo media: missing API key")
	}
	if !ValidPhotoRef(ref) {
		return nil, "", fmt.Errorf("places: photo media: invalid photo ref")
	}
	if maxWidthPx <= 0 || maxWidthPx > 4800 {
		maxWidthPx = 1200
	}

	photoURL := c.baseURL + ref + "/media?maxWidthPx=" +
		strconv.Itoa(maxWidthPx) + "&key=" + url.QueryEscape(c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, photoURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("places: photo media request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("places: photo media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("places: photo media returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, "", fmt.Errorf("places: photo media read: %w", err)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}
	return body, contentType, nil
}
