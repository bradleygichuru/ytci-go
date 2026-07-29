package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type ProfileHandler struct {
	pool *pgxpool.Pool
}

func NewProfileHandler(pool *pgxpool.Pool) *ProfileHandler {
	return &ProfileHandler{pool: pool}
}

func (h *ProfileHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())

	var stories, enrollments, conservation int
	h.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM stories WHERE creator_id = $1`, userID).Scan(&stories)
	h.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM course_enrollments WHERE user_id = $1`, userID).Scan(&enrollments)
	h.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM conservation_evidence WHERE user_id = $1 AND status = 'approved'`,
		userID).Scan(&conservation)

	var countyCount int
	h.pool.QueryRow(r.Context(),
		`SELECT COUNT(DISTINCT d.county) FROM bucket_list_items b
		 JOIN destinations d ON d.id = b.destination_id
		 WHERE b.user_id = $1 AND b.visited = true`, userID).Scan(&countyCount)

	resp := map[string]any{
		"countiesVisited":     countyCount,
		"storiesSubmitted":    stories,
		"challengesCompleted": 0,
		"coursesEnrolled":     enrollments,
		"conservationHours":   conservation,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *ProfileHandler) Update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgeRange    *string `json:"ageRange"`
		County      *string `json:"county"`
		Languages   *string `json:"languages"`
		Preferences *string `json:"preferences"`
		DisplayName *string `json:"displayName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO user_profiles (user_id, created_by, age_range, county, languages, preferences, display_name)
		 VALUES ($1, $1, $2, $3, $4, $5, $6)
		 ON CONFLICT (user_id) DO UPDATE SET
		 age_range = COALESCE($2, user_profiles.age_range),
		 county = COALESCE($3, user_profiles.county),
		 languages = COALESCE($4, user_profiles.languages),
		 preferences = COALESCE($5, user_profiles.preferences),
		 display_name = COALESCE($6, user_profiles.display_name),
		 updated_at = now()`,
		userID, req.AgeRange, req.County, req.Languages, req.Preferences, req.DisplayName)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update profile")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

type badge struct {
	ID          string  `json:"id"`
	BadgeName   string  `json:"badgeName"`
	BadgeIcon   *string `json:"badgeIconUrl,omitempty"`
	EarnedAt    string  `json:"earnedAt"`
	Source      string  `json:"source"`
	SourceID    string  `json:"sourceId"`
	SourceTitle string  `json:"sourceTitle"`
}

func (h *ProfileHandler) Badges(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT cp.id, c.badge_name, c.badge_icon_url, cp.badge_awarded_at, c.id, c.title
		 FROM challenge_progress cp
		 JOIN challenges c ON c.id = cp.challenge_id
		 WHERE cp.user_id = $1 AND cp.badge_awarded_at IS NOT NULL
		 ORDER BY cp.badge_awarded_at DESC LIMIT 50`, userID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get badges")
		return
	}
	defer rows.Close()

	var items []badge
	for rows.Next() {
		var b badge
		rows.Scan(&b.ID, &b.BadgeName, &b.BadgeIcon, &b.EarnedAt, &b.SourceID, &b.SourceTitle)
		b.Source = "challenge"
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to iterate badges")
		return
	}
	if items == nil {
		items = []badge{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[badge]{Items: items, HasMore: false})
}

func (h *ProfileHandler) ConsentGet(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())

	var consentGrantedAt *string
	err := h.pool.QueryRow(r.Context(),
		`SELECT consent_granted_at::text FROM user_profiles WHERE user_id = $1`, userID,
	).Scan(&consentGrantedAt)
	if err == pgx.ErrNoRows {
		err = nil
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get consent")
		return
	}

	resp := map[string]any{
		"consentGrantedAt": consentGrantedAt,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *ProfileHandler) ConsentUpdate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		ConsentGrantedAt *string `json:"consentGrantedAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO user_profiles (user_id, created_by, consent_granted_at)
		 VALUES ($1, $1, COALESCE($2::timestamp, now()))
		 ON CONFLICT (user_id) DO UPDATE SET
		 consent_granted_at = COALESCE($2::timestamp, now()),
		 updated_at = now()`,
		userID, req.ConsentGrantedAt)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update consent")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}
