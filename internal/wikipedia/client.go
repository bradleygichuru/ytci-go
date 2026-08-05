package wikipedia

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HeroImage struct {
	URL         string
	Source      string
	Attribution string
}

type Client struct {
	http    *http.Client
	baseURL string
}

func NewClient() *Client {
	return &Client{
		http:    &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://en.wikipedia.org",
	}
}

type wikiSummary struct {
	Originalimage struct {
		Source string `json:"source"`
	} `json:"originalimage"`
	ContentURLs struct {
		Desktop struct {
			Page string `json:"page"`
		} `json:"desktop"`
	} `json:"content_urls"`
}

type commonsResponse struct {
	Query struct {
		Pages map[string]struct {
			Imageinfo []struct {
				URL         string                 `json:"url"`
				Extmetadata map[string]interface{} `json:"extmetadata"`
			} `json:"imageinfo"`
		} `json:"pages"`
	} `json:"query"`
}

func (c *Client) FetchHeroImage(ctx context.Context, placeName string) (HeroImage, error) {
	slugVariants := buildSlugVariants(placeName)
	for _, slug := range slugVariants {
		hero, ok, err := c.fetchWikipediaSummary(ctx, slug)
		if err != nil {
			slog.Warn("wikipedia: summary fetch failed", "slug", slug, "error", err)
			continue
		}
		if ok {
			return hero, nil
		}
	}
	hero, err := c.fetchCommons(ctx, placeName)
	if err != nil {
		slog.Warn("wikipedia: commons fallback failed", "place", placeName, "error", err)
		return HeroImage{Source: "wikipedia_not_found"}, nil
	}
	return hero, nil
}

func buildSlugVariants(name string) []string {
	slug := strings.ReplaceAll(name, " ", "_")
	variants := []string{slug}
	if idx := strings.Index(slug, "_("); idx > 0 {
		variants = append(variants, slug[:idx])
	}
	variants = append(variants, slug+"_(Kenya)")
	seen := make(map[string]bool, len(variants))
	unique := make([]string, 0, len(variants))
	for _, v := range variants {
		if !seen[v] {
			seen[v] = true
			unique = append(unique, v)
		}
	}
	return unique
}

func (c *Client) fetchWikipediaSummary(ctx context.Context, slug string) (HeroImage, bool, error) {
	summaryURL := fmt.Sprintf("%s/api/rest_v1/page/summary/%s", c.baseURL, url.PathEscape(slug))
	req, err := http.NewRequestWithContext(ctx, "GET", summaryURL, nil)
	if err != nil {
		return HeroImage{}, false, fmt.Errorf("wikipedia: create request: %w", err)
	}
	req.Header.Set("User-Agent", "YTCExplorer/1.0 (https://github.com/bradleygichuru/ytci-mobile)")

	resp, err := c.http.Do(req)
	if err != nil {
		return HeroImage{}, false, fmt.Errorf("wikipedia: execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return HeroImage{}, false, nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return HeroImage{}, false, fmt.Errorf("wikipedia: summary %d: %s", resp.StatusCode, string(body))
	}

	var summary wikiSummary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return HeroImage{}, false, fmt.Errorf("wikipedia: decode: %w", err)
	}

	if summary.Originalimage.Source == "" {
		return HeroImage{}, false, nil
	}

	articleURL := summary.ContentURLs.Desktop.Page
	attribution := fmt.Sprintf("Photo: Wikimedia \u00b7 %s", articleURL)

	return HeroImage{
		URL:         summary.Originalimage.Source,
		Source:      "wikipedia",
		Attribution: attribution,
	}, true, nil
}

func (c *Client) fetchCommons(ctx context.Context, placeName string) (HeroImage, error) {
	searchQuery := fmt.Sprintf("%s Kenya", placeName)
	apiURL := fmt.Sprintf(
		"https://commons.wikimedia.org/w/api.php?action=query&generator=search&gsrsearch=%s&gsrnamespace=6&gsrlimit=5&prop=imageinfo&iiprop=url|extmetadata&iiurlwidth=1200&format=json",
		url.QueryEscape(searchQuery),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return HeroImage{}, fmt.Errorf("commons: create request: %w", err)
	}
	req.Header.Set("User-Agent", "YTCExplorer/1.0 (https://github.com/bradleygichuru/ytci-mobile)")

	resp, err := c.http.Do(req)
	if err != nil {
		return HeroImage{}, fmt.Errorf("commons: execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return HeroImage{}, fmt.Errorf("commons: %d: %s", resp.StatusCode, string(body))
	}

	var apiResp commonsResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return HeroImage{}, fmt.Errorf("commons: decode: %w", err)
	}

	for _, page := range apiResp.Query.Pages {
		if len(page.Imageinfo) == 0 {
			continue
		}
		info := page.Imageinfo[0]
		if info.URL == "" {
			continue
		}
		attribution := "Photo: Wikimedia Commons"
		if lic, ok := info.Extmetadata["LicenseShortName"]; ok {
			if val, ok := lic.(map[string]interface{})["value"].(string); ok && val != "" {
				attribution = fmt.Sprintf("Photo: Wikimedia Commons \u00b7 %s", val)
			}
		}
		return HeroImage{
			URL:         info.URL,
			Source:      "wikimedia_commons",
			Attribution: attribution,
		}, nil
	}

	return HeroImage{}, fmt.Errorf("commons: no usable images found for %q", placeName)
}
