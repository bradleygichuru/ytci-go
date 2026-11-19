package mice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/contracts"
	authmw "github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
)

// Nairobi is the default event timezone (ADR 0009). Loaded once; the fixed
// offset is the fallback when the tz database is unavailable.
var nairobi = func() *time.Location {
	if loc, err := time.LoadLocation("Africa/Nairobi"); err == nil {
		return loc
	}
	return time.FixedZone("EAT", 3*60*60)
}()

// Handler serves the MICE contract.
type Handler struct {
	pool                 *pgxpool.Pool
	ticketSecret         []byte
	notificationsEnabled bool
	now                  func() time.Time
}

// Options carries the MICE runtime configuration.
type Options struct {
	TicketSecret         string
	NotificationsEnabled bool
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

// New builds the MICE handler.
func New(pool *pgxpool.Pool, cfg *config.Config) *Handler {
	return NewWithOptions(pool, Options{
		TicketSecret:         cfg.MiceTicketSecret,
		NotificationsEnabled: cfg.MiceNotificationsEnabled,
	})
}

// NewWithOptions builds the handler without the process config (tests).
func NewWithOptions(pool *pgxpool.Pool, opts Options) *Handler {
	secret := []byte(opts.TicketSecret)
	if len(secret) == 0 {
		// Deterministic development fallback. Production must set
		// MICE_TICKET_SECRET; the value is only used to sign short-lived
		// tickets and never authenticates a user.
		secret = []byte("ytci-mice-development-ticket-secret")
		slog.Warn("mice: MICE_TICKET_SECRET is unset; using the development ticket key")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Handler{
		pool:                 pool,
		ticketSecret:         secret,
		notificationsEnabled: opts.NotificationsEnabled,
		now:                  now,
	}
}

func (h *Handler) clock() time.Time { return h.now().UTC() }

// dbTX is satisfied by both *pgxpool.Pool and pgx.Tx so helpers can run inside
// or outside a transaction.
type dbTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ---------------------------------------------------------------------------
// Actor helpers
// ---------------------------------------------------------------------------

func userID(r *http.Request) string { return authmw.UserID(r.Context()) }

func role(r *http.Request) authmw.Role { return authmw.RoleFromCtx(r.Context()) }

// can reports whether the request actor holds a permission from the frozen
// role contract.
func can(r *http.Request, permission string) bool {
	return contracts.MustRoles().Can(string(role(r)), permission)
}

// requireUser writes 401 unless an account is present.
func requireUser(w http.ResponseWriter, r *http.Request) bool {
	if userID(r) == "" {
		writeErr(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return false
	}
	return true
}

// requirePermission writes 403 unless the actor holds the permission.
func requirePermission(w http.ResponseWriter, r *http.Request, permission string) bool {
	if !can(r, permission) {
		writeErr(w, r, http.StatusForbidden, "FORBIDDEN", "missing permission "+permission, nil)
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
}

func writeErr(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	env := errorEnvelope{Error: errorBody{Code: code, Message: message, Details: details}}
	if r != nil {
		env.Error.RequestID = middleware.GetReqID(r.Context())
	}
	_ = json.NewEncoder(w).Encode(env)
}

func writeValidation(w http.ResponseWriter, failures ...ValidationError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": failures})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeServer(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("mice handler error", "error", err, "path", r.URL.Path, "request_id", middleware.GetReqID(r.Context()))
	writeErr(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "internal error", nil)
}

// decodeJSON reads a JSON body with a 1 MiB guard.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, r, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON: "+err.Error(), nil)
		return false
	}
	return true
}

// paginate applies the frozen `limit`/`cursor` page contract. rows must be
// fetched with limit+1; extra is trimmed and the cursor is returned.
func writePage[T any](w http.ResponseWriter, limit int, rows []T, cursor func(T) (string, bool)) {
	hasMore := len(rows) > limit
	items := rows
	if hasMore {
		items = rows[:limit]
	}
	page := Page[T]{Items: items, HasMore: hasMore}
	if page.Items == nil {
		page.Items = []T{}
	}
	if hasMore && len(items) > 0 {
		if c, ok := cursor(items[len(items)-1]); ok {
			page.NextCursor = &c
		}
	}
	writeJSON(w, http.StatusOK, page)
}

func parsePage(r *http.Request) (limit int, cursor *string) {
	pr := pagination.ParseRequest(r)
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		return pr.Limit, &raw
	}
	return pr.Limit, nil
}

// ---------------------------------------------------------------------------
// Small conversion helpers
// ---------------------------------------------------------------------------

func nullText(v pgtype.Text) string {
	if v.Valid {
		return v.String
	}
	return ""
}

func nullInt(v pgtype.Int4) *int {
	if v.Valid {
		n := int(v.Int32)
		return &n
	}
	return nil
}

func nullTime(v pgtype.Timestamptz) *time.Time {
	if v.Valid {
		t := v.Time
		return &t
	}
	return nil
}

func nullNumeric(v pgtype.Numeric) *float64 {
	if !v.Valid {
		return nil
	}
	f, err := v.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	return &f.Float64
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// trustLabel maps a tier to the frozen public label (ADR 0012 §3). The
// community tier never uses "Verified".
func trustLabel(tier, verificationStatus string) string {
	switch tier {
	case "programme_partner":
		return "YTCI programme partner"
	case "verified_organizer":
		if verificationStatus == "verified" || verificationStatus == "expired" {
			return "Verified organizer"
		}
		return ""
	case "community_organizer":
		return "Community organizer"
	default:
		return ""
	}
}

// normaliseStatus maps the deprecated `scheduled` alias to `published` on the
// way out. Writes always use canonical values.
func normaliseStatus(status string) string {
	if status == "scheduled" {
		return "published"
	}
	return status
}

// eventStart derives the canonical start instant from the legacy date/time
// columns when no session exists.
func eventStart(date time.Time, startTime string, tz string) time.Time {
	loc := loadLocation(tz)
	hh, mm := 0, 0
	if startTime != "" {
		if parsed, err := time.Parse("15:04", startTime); err == nil {
			hh, mm = parsed.Hour(), parsed.Minute()
		}
	}
	return time.Date(date.Year(), date.Month(), date.Day(), hh, mm, 0, 0, loc).UTC()
}

func eventEnd(date time.Time, endDate *time.Time, endTime, tz string) time.Time {
	loc := loadLocation(tz)
	d := date
	if endDate != nil {
		d = *endDate
	}
	hh, mm := 23, 59
	if endTime != "" {
		if parsed, err := time.Parse("15:04", endTime); err == nil {
			hh, mm = parsed.Hour(), parsed.Minute()
		}
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, loc).UTC()
}

func loadLocation(tz string) *time.Location {
	if tz == "" {
		return nairobi
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	return nairobi
}

// checkInWindow reports whether check-in is open: two hours before start
// through event end (ADR 0011 §4).
func checkInWindow(now, start, end time.Time) (open bool, reason string) {
	opens := start.Add(-2 * time.Hour)
	if now.Before(opens) {
		return false, "CHECKIN_NOT_OPEN"
	}
	if now.After(end) {
		return false, "CHECKIN_CLOSED"
	}
	return true, ""
}

// inQuietHours reports whether t falls inside 22:00-06:00 Africa/Nairobi.
func inQuietHours(t time.Time) bool {
	local := t.In(nairobi)
	hour := local.Hour()
	return hour >= 22 || hour < 6
}

// nextQuietHoursEnd returns the instant quiet hours end (06:00 Nairobi) after t.
func nextQuietHoursEnd(t time.Time) time.Time {
	local := t.In(nairobi)
	end := time.Date(local.Year(), local.Month(), local.Day(), 6, 0, 0, 0, nairobi)
	if !end.After(local) {
		end = end.Add(24 * time.Hour)
	}
	return end.UTC()
}

// maybeQuiet shifts a delivery instant out of quiet hours.
func maybeQuiet(t time.Time) time.Time {
	if inQuietHours(t) {
		return nextQuietHoursEnd(t)
	}
	return t
}

// uuidText renders a UUID column scanned as text.
func uuidText(s string) string { return strings.TrimSpace(s) }

// formatKES renders a price display string.
func formatPrice(amount float64, currency string) string {
	if currency == "" {
		return ""
	}
	return fmt.Sprintf("%s %.0f", currency, amount)
}
