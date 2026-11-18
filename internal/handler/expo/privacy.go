package expo

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

// PrivacyHandler serves the reader's data export. V1 heritage visits are
// device-local, so the mobile client merges its local list into this payload.
type PrivacyHandler struct {
	pool *pgxpool.Pool
}

func NewPrivacyHandler(pool *pgxpool.Pool) *PrivacyHandler {
	return &PrivacyHandler{pool: pool}
}

func consentMap(state consentState) map[string]any {
	return map[string]any{
		"location":             state.Location,
		"camera":               state.Camera,
		"notifications":        state.Notifications,
		"ugc":                  state.Ugc,
		"recognition":          state.Recognition,
		"locationVerification": state.LocationVerification,
		"publicProfile":        state.PublicProfile,
		"leaderboard":          state.Leaderboard,
		"analytics":            state.Analytics,
		"version":              state.Version,
		"updatedAt":            state.UpdatedAt,
		"consentGrantedAt":     state.GrantedAt,
		"consentWithdrawnAt":   state.WithdrawnAt,
	}
}

func (h *PrivacyHandler) Export(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	state, err := (&ProfileHandler{pool: h.pool}).loadConsent(r.Context(), userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load consent", err)
		return
	}

	historyRows, err := h.pool.Query(r.Context(), `
		SELECT version, location, camera, notifications, ugc, recognition,
		       location_verification, public_profile, leaderboard, analytics,
		       created_at::text
		FROM consent_records WHERE user_id = $1 ORDER BY created_at ASC`, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load consent history", err)
		return
	}
	defer historyRows.Close()
	history := make([]map[string]any, 0)
	for historyRows.Next() {
		var version, createdAt string
		var location, camera, notifications, ugc, recognition, locationVerification,
			publicProfile, leaderboard, analytics bool
		if err := historyRows.Scan(
			&version, &location, &camera, &notifications, &ugc, &recognition,
			&locationVerification, &publicProfile, &leaderboard, &analytics, &createdAt,
		); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read consent history", err)
			return
		}
		history = append(history, map[string]any{
			"version": version, "location": location, "camera": camera,
			"notifications": notifications, "ugc": ugc, "recognition": recognition,
			"locationVerification": locationVerification, "publicProfile": publicProfile,
			"leaderboard": leaderboard, "analytics": analytics, "at": createdAt,
		})
	}
	if err := historyRows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate consent history", err)
		return
	}

	visitRows, err := h.pool.Query(r.Context(), `
		SELECT d.name, d.slug, b.visited_at::text
		FROM bucket_list_items b
		JOIN destinations d ON d.id = b.destination_id
		WHERE b.user_id = $1 AND b.visited = true
		ORDER BY b.visited_at DESC`, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load destination visits", err)
		return
	}
	defer visitRows.Close()
	visits := make([]map[string]any, 0)
	for visitRows.Next() {
		var name, slug string
		var visitedAt *string
		if err := visitRows.Scan(&name, &slug, &visitedAt); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read destination visit", err)
			return
		}
		visits = append(visits, map[string]any{"name": name, "slug": slug, "visitedAt": visitedAt})
	}
	if err := visitRows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate destination visits", err)
		return
	}

	badgeRows, err := h.pool.Query(r.Context(), `
		SELECT badge_name, COALESCE(source_title, ''), source_type, awarded_at::text
		FROM badges WHERE user_id = $1 ORDER BY awarded_at DESC`, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load badges", err)
		return
	}
	defer badgeRows.Close()
	badges := make([]map[string]any, 0)
	for badgeRows.Next() {
		var name, sourceTitle, sourceType, awardedAt string
		if err := badgeRows.Scan(&name, &sourceTitle, &sourceType, &awardedAt); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read badge", err)
			return
		}
		badges = append(badges, map[string]any{
			"name": name, "sourceTitle": sourceTitle, "sourceType": sourceType, "awardedAt": awardedAt,
		})
	}
	if err := badgeRows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate badges", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="ytci-privacy-export.json"`)
	json.NewEncoder(w).Encode(map[string]any{
		"exportedAt":        time.Now().UTC().Format(time.RFC3339),
		"contact":           "ytci@tourism.go.ke",
		"consent":           consentMap(state),
		"consentHistory":    history,
		"destinationVisits": visits,
		"badges":            badges,
		"recognition": map[string]string{
			"status": "local_only",
			"note":   "Heritage visits and recognition are stored on this device in v1. No server-side heritage visits, XP, badges, passport, or leaderboards are stored.",
		},
		"analytics": map[string]any{
			"model":   "pseudonymous",
			"consent": state.Analytics,
			"note":    "Product analytics use a random analytics ID, not your account ID.",
		},
	})
}
