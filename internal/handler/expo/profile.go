package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
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
