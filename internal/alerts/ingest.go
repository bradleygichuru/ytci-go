package alerts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
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
	state, _ := i.store.SourceState(ctx, "kmd")
	runID, err := i.store.StartRun(ctx, "kmd")
	if err != nil {
		result.Err = err
		return result
	}
	started := time.Now()

	body, status, etag, lastModified, err := i.fetch(ctx, i.kmdURL, 2<<20, state.ETag, state.LastModified)
	if errors.Is(err, errNotModified) {
		i.store.FinishRun(ctx, runID, "not_modified", status, 0, 0, 0, 0, time.Since(started), "")
		i.store.UpdateSourceHealth(ctx, "kmd", true, status, "")
		return result
	}
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
	// The RSS index is newest-first. Process oldest-first so an update or
	// cancel that references an earlier message sees that message first.
	sort.SliceStable(feed.Items[:limit], func(a, b int) bool {
		left, right := feed.Items[a].PubDate, feed.Items[b].PubDate
		if left.IsZero() != right.IsZero() {
			return !left.IsZero()
		}
		return left.Before(right)
	})
	capFailures := 0
	seenIDs := make([]string, 0, limit)
	for _, item := range feed.Items[:limit] {
		if item.Link == "" {
			continue
		}
		capBody, capStatus, _, _, err := i.fetch(ctx, item.Link, i.maxXML, "", "")
		if err != nil {
			capFailures++
			slog.Warn("alerts: KMD CAP fetch failed", "link", item.Link, "status", capStatus, "error", err)
			continue
		}
		capAlert, err := ParseCAP(strings.NewReader(string(capBody)))
		if err != nil {
			capFailures++
			slog.Warn("alerts: KMD CAP parse failed", "link", item.Link, "error", err)
			continue
		}
		n, info, ok := normalizeKMD(capAlert)
		if !ok {
			continue
		}
		seenIDs = append(seenIDs, n.SourceIdentifier)
		mapping, err := i.mapper.MapInfo(ctx, info)
		if err != nil {
			capFailures++
			slog.Warn("alerts: KMD mapping failed", "identifier", n.SourceIdentifier, "error", err)
			continue
		}
		n.MappingMethod = mapping.Method
		n.MappingReview = mapping.Review
		pushEnabled := state.PushEnabled && !n.IsTest &&
			(n.MsgType == "Cancel" ||
				(mapping.Method != MethodUnmapped && !mapping.Review && n.ExpiresAt != nil))
		upsert, err := i.store.UpsertAlert(ctx, n, mapping.Counties, pushEnabled)
		if err != nil {
			capFailures++
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

	possibleGap := state.HasSucceeded && len(feed.Items) >= 5 && len(seenIDs) >= 5 &&
		i.knownBefore(ctx, "kmd", seenIDs, started) == 0
	if possibleGap {
		slog.Warn("alerts: possible KMD feed gap; every listed item was new", "items", len(feed.Items))
	}

	i.store.SaveSourceValidators(ctx, "kmd", etag, lastModified, formatHTTPTime(feed.LastBuildDate))
	runStatus, runError := "ok", ""
	if possibleGap {
		runStatus = "partial"
		runError = fmt.Sprintf("possible_gap: all %d feed items were new", len(feed.Items))
	}
	if capFailures > 0 {
		if result.Created+result.Updated == 0 {
			runStatus = "parse_error"
		} else {
			runStatus = "partial"
		}
		detail := fmt.Sprintf("%d CAP items could not be processed", capFailures)
		if runError == "" {
			runError = detail
		} else {
			runError += "; " + detail
		}
	}
	i.store.FinishRun(ctx, runID, runStatus, status, result.Seen, result.Created, result.Updated, int64(len(body)), time.Since(started), runError)
	if capFailures > 0 || possibleGap {
		i.store.UpdateSourceHealth(ctx, "kmd", false, status, runError)
	} else {
		i.store.UpdateSourceHealth(ctx, "kmd", true, status, "")
	}
	return result
}

func (i *Ingester) IngestFCDO(ctx context.Context) IngestResult {
	result := IngestResult{Source: "fcdo"}
	state, _ := i.store.SourceState(ctx, "fcdo")
	runID, err := i.store.StartRun(ctx, "fcdo")
	if err != nil {
		result.Err = err
		return result
	}
	started := time.Now()

	body, status, etag, lastModified, err := i.fetch(ctx, i.fcdoURL, 2<<20, state.ETag, state.LastModified)
	if errors.Is(err, errNotModified) {
		i.store.FinishRun(ctx, runID, "not_modified", status, 0, 0, 0, 0, time.Since(started), "")
		i.store.UpdateSourceHealth(ctx, "fcdo", true, status, "")
		return result
	}
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
	i.store.SaveSourceValidators(ctx, "fcdo", etag, lastModified, "")
	i.store.FinishRun(ctx, runID, "ok", status, 1, result.Created, result.Updated, int64(len(body)), time.Since(started), "")
	i.store.UpdateSourceHealth(ctx, "fcdo", true, status, "")
	return result
}

func (i *Ingester) IngestAll(ctx context.Context) ([]IngestResult, error) {
	kmd := IngestResult{Source: "kmd"}
	if i.due(ctx, "kmd", 5*time.Minute) {
		kmd = i.IngestKMD(ctx)
	}
	fcdo := IngestResult{Source: "fcdo"}
	if i.due(ctx, "fcdo", 15*time.Minute) {
		fcdo = i.IngestFCDO(ctx)
	}
	results := []IngestResult{kmd, fcdo}
	if kmd.Err != nil && fcdo.Err != nil {
		return results, fmt.Errorf("both sources failed: %v; %v", kmd.Err, fcdo.Err)
	}
	return results, nil
}

func (i *Ingester) due(ctx context.Context, sourceID string, interval time.Duration) bool {
	state, err := i.store.SourceState(ctx, sourceID)
	if err != nil || !state.HasChecked {
		return true
	}
	wait := interval
	if state.ConsecutiveFailures > 0 {
		shift := state.ConsecutiveFailures
		if shift > 4 {
			shift = 4
		}
		wait = interval * time.Duration(1<<shift)
		if wait > 30*time.Minute {
			wait = 30 * time.Minute
		}
	}
	// Cron scheduling is not exact; allow a minute of jitter.
	if wait > time.Minute {
		wait -= time.Minute
	}
	return time.Since(state.LastCheckedAt) >= wait
}

func (i *Ingester) knownBefore(ctx context.Context, sourceID string, ids []string, since time.Time) int {
	if len(ids) == 0 {
		return 0
	}
	var n int
	if err := i.store.Pool().QueryRow(ctx, `
		SELECT count(*) FROM alert_source_messages
		WHERE source_id=$1 AND source_identifier = ANY($2) AND first_seen_at < $3`,
		sourceID, ids, since).Scan(&n); err != nil {
		return 0
	}
	return n
}

var errNotModified = errors.New("not modified")

func (i *Ingester) fetch(ctx context.Context, url string, maxBytes int64, etag, lastModified string) ([]byte, int, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, "", "", err
	}
	req.Header.Set("User-Agent", "YTCI-Alerts/1.0 (+https://ytci.example)")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
	resp, err := i.client.Do(req)
	if err != nil {
		return nil, 0, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, resp.StatusCode, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), errNotModified
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, resp.StatusCode, "", "", err
	}
	if int64(len(body)) > maxBytes {
		return nil, resp.StatusCode, "", "", fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	return body, resp.StatusCode, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), nil
}

func formatHTTPTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(http.TimeFormat)
}

func normalizeKMD(alert *CAPAlert) (NormalizedAlert, CAPInfo, bool) {
	if alert == nil || alert.Identifier == "" {
		return NormalizedAlert{}, CAPInfo{}, false
	}
	if len(alert.Infos) == 0 {
		if !strings.EqualFold(alert.MsgType, "Cancel") {
			return NormalizedAlert{}, CAPInfo{}, false
		}
		isTest := !strings.EqualFold(alert.Status, "Actual")
		if scope := strings.TrimSpace(alert.Scope); scope != "" && !strings.EqualFold(scope, "Public") {
			isTest = true
		}
		sum := sha256.Sum256([]byte(alert.Identifier + "|Cancel|" + alert.References))
		return NormalizedAlert{
			SourceID: "kmd", SourceIdentifier: alert.Identifier, MsgType: "Cancel",
			Category: "Other", Categories: []string{}, Event: "Cancelled alert",
			Severity: "Unknown", Urgency: "Unknown", Certainty: "Unknown", Language: "en",
			Headline: "Alert cancelled", Description: "The official source withdrew this alert.",
			SentAt: alert.Sent, IsTest: isTest, ContentHash: hex.EncodeToString(sum[:]),
			MappingMethod: MethodManual, CapReferences: splitReferences(alert.References),
			ReferenceIdentifier: referenceIdentifier(alert.References),
		}, CAPInfo{}, true
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
	msgType := strings.TrimSpace(alert.MsgType)
	if msgType == "" {
		msgType = "Alert"
	}
	status := alert.Status
	isTest := !strings.EqualFold(status, "Actual")
	if scope := strings.TrimSpace(alert.Scope); scope != "" && !strings.EqualFold(scope, "Public") {
		isTest = true
	}
	switch msgType {
	case "Alert", "Update", "Cancel":
	default:
		// Ack/Error and unknown message types are not public alerts.
		isTest = true
	}
	references := splitReferences(alert.References)
	refID := referenceIdentifier(alert.References)

	var areaDescs []string
	for _, area := range info.Areas {
		if strings.TrimSpace(area.AreaDesc) != "" {
			areaDescs = append(areaDescs, area.AreaDesc)
		}
	}
	type areaHash struct {
		Desc     string   `json:"desc"`
		Polygons []string `json:"polygons,omitempty"`
	}
	areas := make([]areaHash, 0, len(info.Areas))
	for _, area := range info.Areas {
		areas = append(areas, areaHash{Desc: area.AreaDesc, Polygons: area.Polygons})
	}
	hashInput, _ := json.Marshal(struct {
		MsgType, Event, Severity, Urgency, Certainty, Headline, Description, Instruction string
		Onset, Expires                                                                   *time.Time
		Areas                                                                            []areaHash
	}{msgType, info.Event, info.Severity, info.Urgency, info.Certainty, info.Headline, info.Description, info.Instruction, info.Onset, info.Expires, areas})
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
