package expo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

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

	var languages, preferences, ageRange *string
	var onboardingCompleted bool
	err := h.pool.QueryRow(r.Context(),
		`SELECT languages, preferences, age_range, onboarding_completed FROM user_profiles WHERE user_id = $1`,
		userID).Scan(&languages, &preferences, &ageRange, &onboardingCompleted)
	if err == pgx.ErrNoRows {
		err = nil
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to get profile", err)
		return
	}

	resp := map[string]any{
		"countiesVisited":     countyCount,
		"storiesSubmitted":    stories,
		"challengesCompleted": 0,
		"coursesEnrolled":     enrollments,
		"conservationActions": conservation,
		"languages":           languages,
		"preferences":         preferences,
		"ageRange":            ageRange,
		"onboardingCompleted": onboardingCompleted,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *ProfileHandler) Update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgeRange            *string `json:"ageRange"`
		County              *string `json:"county"`
		Languages           *string `json:"languages"`
		Preferences         *string `json:"preferences"`
		DisplayName         *string `json:"displayName"`
		OnboardingCompleted *bool   `json:"onboardingCompleted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())

	// When the caller doesn't supply a displayName, fall back to the
	// sign-up name stored in the better-auth users table so that
	// user_profiles.display_name is never left NULL for new users.
	displayName := req.DisplayName
	if displayName == nil || *displayName == "" {
		var authName string
		if err := h.pool.QueryRow(r.Context(),
			`SELECT name FROM users WHERE id = $1`, userID).Scan(&authName); err == nil && authName != "" {
			displayName = &authName
		}
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO user_profiles (user_id, created_by, age_range, county, languages, preferences, display_name, onboarding_completed)
		 VALUES ($1, $1, $2, $3, $4, $5, $6, COALESCE($7, false))
		 ON CONFLICT (user_id) DO UPDATE SET
		 age_range = COALESCE($2, user_profiles.age_range),
		 county = COALESCE($3, user_profiles.county),
		 languages = COALESCE($4, user_profiles.languages),
		 preferences = COALESCE($5, user_profiles.preferences),
		 display_name = COALESCE($6, user_profiles.display_name),
		 onboarding_completed = COALESCE($7, user_profiles.onboarding_completed),
		 updated_at = now()`,
		userID, req.AgeRange, req.County, req.Languages, req.Preferences, displayName, req.OnboardingCompleted)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update profile", err)
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
		`SELECT id, badge_name, badge_icon_url, awarded_at::text, source_type, source_id::text, source_title
		 FROM badges
		 WHERE user_id = $1
		 ORDER BY awarded_at DESC LIMIT 50`, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to get badges", err)
		return
	}
	defer rows.Close()

	var items []badge
	for rows.Next() {
		var b badge
		rows.Scan(&b.ID, &b.BadgeName, &b.BadgeIcon, &b.EarnedAt, &b.Source, &b.SourceID, &b.SourceTitle)
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate badges", err)
		return
	}
	if items == nil {
		items = []badge{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[badge]{Items: items, HasMore: false})
}

const currentConsentVersion = "2026-11-18"

type consentState struct {
	Location             bool
	Camera               bool
	Notifications        bool
	Ugc                  bool
	Recognition          bool
	LocationVerification bool
	PublicProfile        bool
	Leaderboard          bool
	Analytics            bool
	Version              string
	UpdatedAt            *string
	GrantedAt            *string
	WithdrawnAt          *string
}

type consentResponse struct {
	Location             bool    `json:"location"`
	Camera               bool    `json:"camera"`
	Notifications        bool    `json:"notifications"`
	Ugc                  bool    `json:"ugc"`
	Recognition          bool    `json:"recognition"`
	LocationVerification bool    `json:"locationVerification"`
	PublicProfile        bool    `json:"publicProfile"`
	Leaderboard          bool    `json:"leaderboard"`
	Analytics            bool    `json:"analytics"`
	Version              string  `json:"version"`
	UpdatedAt            *string `json:"updatedAt,omitempty"`
	ConsentGrantedAt     *string `json:"consentGrantedAt,omitempty"`
	ConsentWithdrawnAt   *string `json:"consentWithdrawnAt,omitempty"`
}

func (h *ProfileHandler) loadConsent(ctx context.Context, userID string) (consentState, error) {
	state := consentState{Version: currentConsentVersion}
	err := h.pool.QueryRow(ctx, `
		SELECT COALESCE(consent_location, false), COALESCE(consent_camera, false),
		       COALESCE(consent_notifications, false), COALESCE(consent_ugc, false),
		       COALESCE(consent_recognition, false), COALESCE(consent_location_verification, false),
		       COALESCE(consent_public_profile, false), COALESCE(consent_leaderboard, false),
		       COALESCE(consent_analytics, false), COALESCE(consent_version, $2),
		       updated_at::text, consent_granted_at::text, consent_withdrawn_at::text
		FROM user_profiles WHERE user_id = $1`, userID, currentConsentVersion,
	).Scan(
		&state.Location, &state.Camera, &state.Notifications, &state.Ugc,
		&state.Recognition, &state.LocationVerification, &state.PublicProfile,
		&state.Leaderboard, &state.Analytics, &state.Version,
		&state.UpdatedAt, &state.GrantedAt, &state.WithdrawnAt,
	)
	if err == pgx.ErrNoRows {
		return state, nil
	}
	return state, err
}

func (h *ProfileHandler) ConsentGet(w http.ResponseWriter, r *http.Request) {
	state, err := h.loadConsent(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to get consent", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(consentResponse{
		Location:             state.Location,
		Camera:               state.Camera,
		Notifications:        state.Notifications,
		Ugc:                  state.Ugc,
		Recognition:          state.Recognition,
		LocationVerification: state.LocationVerification,
		PublicProfile:        state.PublicProfile,
		Leaderboard:          state.Leaderboard,
		Analytics:            state.Analytics,
		Version:              state.Version,
		UpdatedAt:            state.UpdatedAt,
		ConsentGrantedAt:     state.GrantedAt,
		ConsentWithdrawnAt:   state.WithdrawnAt,
	})
}

type consentUpdateRequest struct {
	Location             *bool   `json:"location,omitempty"`
	Camera               *bool   `json:"camera,omitempty"`
	Notifications        *bool   `json:"notifications,omitempty"`
	Ugc                  *bool   `json:"ugc,omitempty"`
	Recognition          *bool   `json:"recognition,omitempty"`
	LocationVerification *bool   `json:"locationVerification,omitempty"`
	PublicProfile        *bool   `json:"publicProfile,omitempty"`
	Leaderboard          *bool   `json:"leaderboard,omitempty"`
	Analytics            *bool   `json:"analytics,omitempty"`
	Version              *string `json:"version,omitempty"`
}

func applyConsentBool(target *bool, value *bool) {
	if value != nil {
		*target = *value
	}
}

func (h *ProfileHandler) ConsentUpdate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req consentUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	state, err := h.loadConsent(r.Context(), userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load consent", err)
		return
	}
	previousRecognition := state.Recognition
	applyConsentBool(&state.Location, req.Location)
	applyConsentBool(&state.Camera, req.Camera)
	applyConsentBool(&state.Notifications, req.Notifications)
	applyConsentBool(&state.Ugc, req.Ugc)
	applyConsentBool(&state.Recognition, req.Recognition)
	applyConsentBool(&state.LocationVerification, req.LocationVerification)
	applyConsentBool(&state.PublicProfile, req.PublicProfile)
	applyConsentBool(&state.Leaderboard, req.Leaderboard)
	applyConsentBool(&state.Analytics, req.Analytics)
	if req.Version != nil && strings.TrimSpace(*req.Version) != "" {
		state.Version = strings.TrimSpace(*req.Version)
	}
	if state.Version == "" {
		state.Version = currentConsentVersion
	}

	if state.PublicProfile || state.Leaderboard {
		if !state.Recognition {
			handler.WriteError(w, http.StatusBadRequest, "RECOGNITION_REQUIRED", "public profile and leaderboard require recognition consent")
			return
		}
	}
	if state.LocationVerification && !state.Recognition {
		handler.WriteError(w, http.StatusBadRequest, "RECOGNITION_REQUIRED", "location verification requires recognition consent")
		return
	}
	if state.Recognition {
		var ageRange *string
		if err := h.pool.QueryRow(r.Context(),
			`SELECT age_range FROM user_profiles WHERE user_id = $1`, userID).Scan(&ageRange); err != nil && err != pgx.ErrNoRows {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check age range", err)
			return
		}
		if ageRange == nil || strings.TrimSpace(*ageRange) == "" {
			handler.WriteError(w, http.StatusBadRequest, "AGE_REQUIRED", "set an age range before enabling recognition")
			return
		}
		if strings.EqualFold(strings.TrimSpace(*ageRange), "Under 18") {
			handler.WriteError(w, http.StatusForbidden, "AGE_RESTRICTED", "recognition is available to users 18 and over")
			return
		}
	}
	if previousRecognition && !state.Recognition {
		now := time.Now().UTC().Format(time.RFC3339)
		state.WithdrawnAt = &now
	}

	// Populate display_name from the auth sign-up name when creating
	// a new user_profiles row so it is never left NULL.
	var displayName *string
	var authName string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT name FROM users WHERE id = $1`, userID).Scan(&authName); err == nil && authName != "" {
		displayName = &authName
	}

	anyConsent := state.Location || state.Camera || state.Notifications || state.Ugc ||
		state.Recognition || state.LocationVerification || state.PublicProfile ||
		state.Leaderboard || state.Analytics

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to begin consent transaction", err)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO user_profiles (
			user_id, created_by, display_name,
			consent_location, consent_camera, consent_notifications, consent_ugc,
			consent_recognition, consent_location_verification, consent_public_profile,
			consent_leaderboard, consent_analytics, consent_version,
			consent_granted_at, consent_withdrawn_at
		) VALUES (
			$1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			CASE WHEN $13 THEN now() ELSE NULL END, NULL
		)
		ON CONFLICT (user_id) DO UPDATE SET
			display_name = COALESCE($2, user_profiles.display_name),
			consent_location = $3,
			consent_camera = $4,
			consent_notifications = $5,
			consent_ugc = $6,
			consent_recognition = $7,
			consent_location_verification = $8,
			consent_public_profile = $9,
			consent_leaderboard = $10,
			consent_analytics = $11,
			consent_version = $12,
			consent_granted_at = CASE WHEN $13 THEN COALESCE(user_profiles.consent_granted_at, now()) ELSE user_profiles.consent_granted_at END,
			consent_withdrawn_at = CASE WHEN $7 = false AND user_profiles.consent_recognition = true THEN now() ELSE user_profiles.consent_withdrawn_at END,
			updated_at = now()`,
		userID, displayName, state.Location, state.Camera, state.Notifications, state.Ugc,
		state.Recognition, state.LocationVerification, state.PublicProfile, state.Leaderboard,
		state.Analytics, state.Version, anyConsent); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update consent", err)
		return
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO consent_records (
			user_id, version, location, camera, notifications, ugc,
			recognition, location_verification, public_profile, leaderboard, analytics
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		userID, state.Version, state.Location, state.Camera, state.Notifications, state.Ugc,
		state.Recognition, state.LocationVerification, state.PublicProfile, state.Leaderboard,
		state.Analytics); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record consent version", err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit consent", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated", "version": state.Version})
}
