package repository

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/model"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

// Handler serves the repository catalogue: public reads, anonymous
// submissions, upload intents and the admin moderation surface.
type Handler struct {
	pool *pgxpool.Pool
	r2   r2.Store
}

func New(pool *pgxpool.Pool, store r2.Store) *Handler {
	return &Handler{pool: pool, r2: store}
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writePublicCache marks a public catalogue response as shortly cacheable.
// The window is deliberately shorter than the media URL TTL so an SSR page
// never serves a URL that expires while the response is still hot.
func writePublicCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, s-maxage=60, stale-while-revalidate=300")
}

type validationCollector struct {
	errs []model.ValidationError
}

func (v *validationCollector) add(field, message string) {
	v.errs = append(v.errs, model.ValidationError{Field: field, Message: message})
}

func (v *validationCollector) empty() bool { return len(v.errs) == 0 }

func (v *validationCollector) write(w http.ResponseWriter) bool {
	if v.empty() {
		return false
	}
	handler.WriteValidationError(w, v.errs)
	return true
}

// decodeBody enforces a request-size ceiling and decodes a JSON object.
func decodeBody(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Query parsing
// ---------------------------------------------------------------------------

// parseLimit clamps to 1..50 with a default of 20, matching the contract.
func parseLimit(raw string) int {
	if raw == "" {
		return publicDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return publicDefault
	}
	if n > publicListLimit {
		return publicListLimit
	}
	return n
}

// parseCursor decodes the opaque cursor and rejects malformed values instead
// of silently starting from the first page.
func parseCursor(raw string) (*pagination.Cursor, error) {
	if raw == "" {
		return nil, nil
	}
	cur, ok := pagination.DecodeCursor(raw)
	if !ok {
		return nil, fmt.Errorf("malformed cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, cur.SortValue); err != nil {
		return nil, fmt.Errorf("malformed cursor timestamp")
	}
	if _, err := uuid.Parse(cur.ID); err != nil {
		return nil, fmt.Errorf("malformed cursor id")
	}
	return &cur, nil
}

// parseKinds accepts repeated and/or comma-separated kind filters.
func parseKinds(values []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			value := strings.TrimSpace(part)
			if value == "" {
				continue
			}
			if !knownKind(value) {
				return nil, fmt.Errorf("unknown kind %q", value)
			}
			if !seen[value] {
				seen[value] = true
				out = append(out, value)
			}
		}
	}
	return out, nil
}

func knownKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Value helpers
// ---------------------------------------------------------------------------

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func timePtr(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	v := t.UTC().Format(time.RFC3339)
	return &v
}

func timeValue(t *time.Time) string {
	if v := timePtr(t); v != nil {
		return *v
	}
	return ""
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// ---------------------------------------------------------------------------
// Slugs and reference codes
// ---------------------------------------------------------------------------

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = strings.NewReplacer("'", "", "’", "", "&", " and ").Replace(s)
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "entry"
	}
	runes := []rune(s)
	if len(runes) > 80 {
		s = strings.Trim(string(runes[:80]), "-")
	}
	return s
}

var codeAlphabet = []rune("ABCDEFGHJKLMNPQRSTUVWXYZ23456789")

func randomCode(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is not recoverable in practice; fall back to a
		// time-derived suffix so the request still fails safe on uniqueness.
		sum := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte(sum >> (i * 3))
		}
	}
	out := make([]rune, n)
	for i, b := range buf {
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(out)
}

func newReferenceCode() string {
	return "YTCI-" + randomCode(6)
}

// ---------------------------------------------------------------------------
// Content validation
// ---------------------------------------------------------------------------

func validEmail(raw string) bool {
	addr, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.Contains(addr.Address, "@") && addr.Address == strings.TrimSpace(raw)
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// embedProvider allowlists YouTube, Vimeo and Matterport (decision #2).
func embedProvider(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	switch {
	case host == "youtube.com" || host == "m.youtube.com" || host == "youtu.be" || host == "youtube-nocookie.com":
		return "youtube", true
	case host == "vimeo.com" || host == "player.vimeo.com":
		return "vimeo", true
	case host == "matterport.com" || strings.HasSuffix(host, ".matterport.com"):
		return "matterport", true
	}
	return "", false
}

func normalizeTags(tags []string) ([]string, bool) {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, raw := range tags {
		tag := strings.TrimSpace(strings.ToLower(raw))
		if tag == "" {
			continue
		}
		if len([]rune(tag)) > 40 || len(out) >= 10 {
			return nil, false
		}
		if !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out, true
}

func normalizeTeam(members []teamMember) ([]teamMember, bool) {
	out := make([]teamMember, 0, len(members))
	for _, m := range members {
		name := strings.TrimSpace(m.Name)
		role := strings.TrimSpace(m.Role)
		if name == "" || len([]rune(name)) > 120 || len([]rune(role)) > 120 {
			return nil, false
		}
		out = append(out, teamMember{Name: name, Role: role})
		if len(out) > 10 {
			return nil, false
		}
	}
	return out, true
}

// safeFileName strips directory components and characters that would change
// the object-key shape.
func safeFileName(raw string) string {
	raw = strings.TrimSpace(raw)
	if idx := strings.LastIndexAny(raw, `/\`); idx >= 0 {
		raw = raw[idx+1:]
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		out = "upload"
	}
	if len(out) > 100 {
		ext := ""
		if i := strings.LastIndexByte(out, '.'); i > 0 {
			ext = out[i:]
		}
		out = out[:100-len(ext)] + ext
	}
	return out
}
