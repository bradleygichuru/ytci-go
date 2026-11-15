package alerts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultKMDURL  = "https://meteo.go.ke/api/cap/rss.xml"
	DefaultFCDOURL = "https://www.gov.uk/api/content/foreign-travel-advice/kenya"
)

type Ingester struct {
	store    *Store
	mapper   *Mapper
	client   *http.Client
	kmdURL   string
	fcdoURL  string
	maxXML   int64
	maxItems int
}

type IngestResult struct {
	Source  string
	Seen    int
	Created int
	Updated int
	Err     error
}

func NewIngester(ctx context.Context, store *Store, kmdURL, fcdoURL string) (*Ingester, error) {
	mapper, err := NewMapper(ctx, store)
	if err != nil {
		return nil, err
	}
	if kmdURL == "" {
		kmdURL = DefaultKMDURL
	}
	if fcdoURL == "" {
		fcdoURL = DefaultFCDOURL
	}
	return &Ingester{
		store:    store,
		mapper:   mapper,
		client:   &http.Client{Timeout: 20 * time.Second},
		kmdURL:   kmdURL,
		fcdoURL:  fcdoURL,
		maxXML:   5 << 20,
		maxItems: 10,
	}, nil
}

func (i *Ingester) IngestKMD(ctx context.Context) IngestResult {
	result := IngestResult{Source: "kmd"}
	runID, err := i.store.StartRun(ctx, "kmd")
	if err != nil {
		result.Err = err
		return result
	}
	started := time.Now()

	body, status, err := i.fetch(ctx, i.kmdURL, 2<<20)
	if err != nil {
		i.store.FinishRun(ctx, runID, "http_error", status, 0, 0, 0, 0, time.Since(started), err.Error())
		i.store.UpdateSourceHealth(ctx, "kmd", false, status, err.Error())
		result.Err = err
		return result
	}
	feed, err := ParseCAPRSS(strings.NewReader(string(body)))
	if err != nil {
		i.store.FinishRun(ctx, runID, "parse_error", status, 0, 0, 0, int64(len(body)), time.Since(started), err.Error())
		i.store.UpdateSourceHealth(ctx, "kmd", false, status, err.Error())
		result.Err = err
		return result
	}

	result.Seen = len(feed.Items)
	limit := len(feed.Items)
	if limit > i.maxItems {
		limit = i.maxItems
	}
	for _, item := range feed.Items[:limit] {
		if item.Link == "" {
			continue
		}
		capBody, capStatus, err := i.fetch(ctx, item.Link, i.maxXML)
		if err != nil {
			slog.Warn("alerts: KMD CAP fetch failed", "link", item.Link, "status", capStatus, "error", err)
			continue
		}
		capAlert, err := ParseCAP(strings.NewReader(string(capBody)))
		if err != nil {
			slog.Warn("alerts: KMD CAP parse failed", "link", item.Link, "error", err)
			continue
		}
		n, info, ok := normalizeKMD(capAlert)
		if !ok {
			continue
		}
		mapping, err := i.mapper.MapInfo(ctx, info)
		if err != nil {
			slog.Warn("alerts: KMD mapping failed", "identifier", n.SourceIdentifier, "error", err)
			continue
		}
		n.MappingMethod = mapping.Method
		n.MappingReview = mapping.Review
		pushEnabled := mapping.Method != MethodUnmapped && !mapping.Review && !n.IsTest
		upsert, err := i.store.UpsertAlert(ctx, n, mapping.Counties, pushEnabled)
		if err != nil {
			slog.Warn("alerts: KMD upsert failed", "identifier", n.SourceIdentifier, "error", err)
			continue
		}
		if upsert.Created {
			result.Created++
		}
		if upsert.Updated {
			result.Updated++
		}
	}

	i.store.FinishRun(ctx, runID, "ok", status, result.Seen, result.Created, result.Updated, int64(len(body)), time.Since(started), "")
	i.store.UpdateSourceHealth(ctx, "kmd", true, status, "")
	return result
}

func (i *Ingester) IngestFCDO(ctx context.Context) IngestResult {
	result := IngestResult{Source: "fcdo"}
	runID, err := i.store.StartRun(ctx, "fcdo")
	if err != nil {
		result.Err = err
		return result
	}
	started := time.Now()

	body, status, err := i.fetch(ctx, i.fcdoURL, 2<<20)
	if err != nil {
		i.store.FinishRun(ctx, runID, "http_error", status, 0, 0, 0, 0, time.Since(started), err.Error())
		i.store.UpdateSourceHealth(ctx, "fcdo", false, status, err.Error())
		result.Err = err
		return result
	}
	advice, err := ParseFCDO(strings.NewReader(string(body)))
	if err != nil {
		i.store.FinishRun(ctx, runID, "parse_error", status, 0, 0, 0, int64(len(body)), time.Since(started), err.Error())
		i.store.UpdateSourceHealth(ctx, "fcdo", false, status, err.Error())
		result.Err = err
		return result
	}

	hash := sha256.Sum256([]byte(advice.SafetyText))
	n := NormalizedAlert{
		SourceID:         "fcdo",
		SourceIdentifier: "fcdo-kenya",
		SourceURL:        "https://www.gov.uk/foreign-travel-advice/kenya",
		MsgType:          "Alert",
		Category:         "Security",
		Categories:       []string{"Security"},
		Event:            "Kenya travel advice",
		Severity:         "Unknown",
		Urgency:          "Unknown",
		Certainty:        "Unknown",
		Language:         "en",
		Headline:         "Security advisory for Kenya",
		Description:      advice.SafetyText,
		Instruction:      "",
		WebURL:           "https://www.gov.uk/foreign-travel-advice/kenya",
		SenderName:       "UK Foreign, Commonwealth & Development Office",
		SentAt:           advice.PublicUpdatedAt,
		ContentHash:      hex.EncodeToString(hash[:]),
		RawAreaDesc:      "Kenya",
		MappingMethod:    MethodNational,
		MappingReview:    false,
		IsTest:           false,
	}
	result.Seen = 1
	upsert, err := i.store.UpsertAlert(ctx, n, nil, false)
	if err != nil {
		i.store.FinishRun(ctx, runID, "parse_error", status, 1, 0, 0, int64(len(body)), time.Since(started), err.Error())
		i.store.UpdateSourceHealth(ctx, "fcdo", false, status, err.Error())
		result.Err = err
		return result
	}
	if upsert.Created {
		result.Created = 1
	}
	if upsert.Updated {
		result.Updated = 1
	}
	i.store.FinishRun(ctx, runID, "ok", status, 1, result.Created, result.Updated, int64(len(body)), time.Since(started), "")
	i.store.UpdateSourceHealth(ctx, "fcdo", true, status, "")
	return result
}

func (i *Ingester) IngestAll(ctx context.Context) ([]IngestResult, error) {
	kmd := i.IngestKMD(ctx)
	fcdo := i.IngestFCDO(ctx)
	results := []IngestResult{kmd, fcdo}
	if kmd.Err != nil && fcdo.Err != nil {
		return results, fmt.Errorf("both sources failed: %v; %v", kmd.Err, fcdo.Err)
	}
	return results, nil
}

func (i *Ingester) fetch(ctx context.Context, url string, maxBytes int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "YTCI-Alerts/1.0 (+https://ytci.example)")
	resp, err := i.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if int64(len(body)) > maxBytes {
		return nil, resp.StatusCode, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	return body, resp.StatusCode, nil
}

func normalizeKMD(alert *CAPAlert) (NormalizedAlert, CAPInfo, bool) {
	if alert == nil || alert.Identifier == "" || len(alert.Infos) == 0 {
		return NormalizedAlert{}, CAPInfo{}, false
	}
	info := alert.Infos[0]
	for _, candidate := range alert.Infos {
		if strings.EqualFold(candidate.Language, "en") {
			info = candidate
			break
		}
	}
	categories := info.Categories
	if len(categories) == 0 {
		categories = []string{"Met"}
	}
	category := categories[0]
	msgType := alert.MsgType
	if msgType == "" {
		msgType = "Alert"
	}
	status := alert.Status
	isTest := !strings.EqualFold(status, "Actual")
	references := splitReferences(alert.References)
	refID := referenceIdentifier(alert.References)

	var areaDescs []string
	for _, area := range info.Areas {
		if strings.TrimSpace(area.AreaDesc) != "" {
			areaDescs = append(areaDescs, area.AreaDesc)
		}
	}
	hashInput, _ := json.Marshal(struct {
		Event, Severity, Urgency, Certainty, Headline, Description, Instruction string
		Expires                                                                 *time.Time
		Areas                                                                   []string
	}{info.Event, info.Severity, info.Urgency, info.Certainty, info.Headline, info.Description, info.Instruction, info.Expires, areaDescs})
	sum := sha256.Sum256(hashInput)

	return NormalizedAlert{
		SourceID:            "kmd",
		SourceIdentifier:    alert.Identifier,
		SourceURL:           info.Web,
		MsgType:             msgType,
		Category:            category,
		Categories:          categories,
		Event:               info.Event,
		Severity:            NormalizeSeverity(info.Severity),
		Urgency:             normalizeCAPValue(info.Urgency, "Unknown"),
		Certainty:           normalizeCAPValue(info.Certainty, "Unknown"),
		Language:            "en",
		Headline:            info.Headline,
		Description:         info.Description,
		Instruction:         info.Instruction,
		WebURL:              info.Web,
		Contact:             info.Contact,
		Sender:              alert.Sender,
		SenderName:          "Kenya Meteorological Department",
		SentAt:              alert.Sent,
		EffectiveAt:         info.Effective,
		OnsetAt:             info.Onset,
		ExpiresAt:           info.Expires,
		ContentHash:         hex.EncodeToString(sum[:]),
		RawAreaDesc:         strings.Join(areaDescs, ", "),
		MappingReview:       isTest,
		CapReferences:       references,
		IsTest:              isTest,
		ReferenceIdentifier: refID,
	}, info, true
}

func splitReferences(value string) []string {
	return strings.Fields(value)
}

func referenceIdentifier(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	parts := strings.Split(fields[0], ",")
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func normalizeCAPValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	switch strings.Title(strings.ToLower(value)) {
	case "Immediate", "Expected", "Future", "Past", "Unknown",
		"Observed", "Likely", "Possible", "Unlikely":
		return strings.Title(strings.ToLower(value))
	default:
		return fallback
	}
}
