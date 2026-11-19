package recognition

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/contracts"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

// Handler exposes the frozen recognition OpenAPI operations.
type Handler struct {
	engine  *Engine
	enabled bool
}

// NewHandler builds the HTTP layer. RECOGNITION_ENABLED=false keeps the
// deploy dark; the database kill switch remains the operational gate.
func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{engine: New(pool), enabled: featureEnabled()}
}

// NewEngineHandler is used by tests and wiring that already owns an engine.
func NewEngineHandler(e *Engine) *Handler {
	return &Handler{engine: e, enabled: true}
}

// Engine returns the shared engine for source modules that emit events in
// their own transactions.
func (h *Handler) Engine() *Engine { return h.engine }

func featureEnabled() bool {
	v := os.Getenv("RECOGNITION_ENABLED")
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return true
	}
	return b
}

func (h *Handler) ready(w http.ResponseWriter) bool {
	if h.enabled && h.engine != nil && h.engine.pool != nil {
		return true
	}
	handler.WriteError(w, http.StatusServiceUnavailable, "RECOGNITION_DISABLED", "recognition is not enabled")
	return false
}

// ---------------------------------------------------------------------------
// Public
// ---------------------------------------------------------------------------

func (h *Handler) PublicLeaderboard(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	seasonKey := r.URL.Query().Get("seasonKey")
	if seasonKey == "" {
		validation(w, "seasonKey", "seasonKey is required")
		return
	}
	var county *int
	if raw := r.URL.Query().Get("countyGid"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			validation(w, "countyGid", "countyGid must be an integer")
			return
		}
		county = &n
	}
	board, err := h.engine.PublicLeaderboard(r.Context(), seasonKey, county)
	if errors.Is(err, ErrKilled) {
		handler.WriteError(w, http.StatusUnavailableForLegalReasons,
			"PUBLIC_LEADERBOARD_UNAVAILABLE", "the public leaderboard is unavailable")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "season not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load leaderboard", err)
		return
	}
	writeJSON(w, http.StatusOK, board)
}

func (h *Handler) PublicSeason(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	s, err := h.engine.Season(r.Context(), chi.URLParam(r, "key"))
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "season not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load season", err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// ---------------------------------------------------------------------------
// Mobile
// ---------------------------------------------------------------------------

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	out, err := h.engine.Overview(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load recognition", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) ListBadges(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "all"
	}
	items, hasMore, next, counts, err := h.engine.ListBadges(r.Context(),
		middleware.UserID(r.Context()), r.URL.Query().Get("family"), state,
		r.URL.Query().Get("cursor"), limitParam(r, 50))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load badges", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "nextCursor": nullable(next), "hasMore": hasMore, "counts": counts,
	})
}

func (h *Handler) GetBadge(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	out, err := h.engine.Badge(r.Context(), middleware.UserID(r.Context()), chi.URLParam(r, "key"))
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "badge not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load badge", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) SetBadgeVisibility(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	var body struct {
		Hidden *bool `json:"hidden"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Hidden == nil {
		validation(w, "hidden", "hidden is required")
		return
	}
	out, err := h.engine.SetBadgeVisibility(r.Context(), middleware.UserID(r.Context()),
		chi.URLParam(r, "key"), *body.Hidden)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "badge not earned")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update badge", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) Missions(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	items, err := h.engine.Missions(r.Context(), middleware.UserID(r.Context()),
		r.URL.Query().Get("state"))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load missions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) MySeason(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	out, err := h.engine.SeasonProgress(r.Context(), middleware.UserID(r.Context()),
		chi.URLParam(r, "key"))
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "season not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load season progress", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) Passport(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	out, err := h.engine.Passport(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load passport", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) MyLeaderboardState(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	seasonKey := r.URL.Query().Get("seasonKey")
	if seasonKey == "" {
		validation(w, "seasonKey", "seasonKey is required")
		return
	}
	out, err := h.engine.LeaderboardState(r.Context(), middleware.UserID(r.Context()), seasonKey)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load leaderboard state", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) JoinLeaderboard(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	var body struct {
		SeasonKey      string `json:"seasonKey"`
		DisplayName    string `json:"displayName"`
		ConsentVersion string `json:"consentVersion"`
		CountyGID      *int   `json:"countyGid"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.SeasonKey == "" || body.DisplayName == "" {
		validation(w, "displayName", "seasonKey and displayName are required")
		return
	}
	out, err := h.engine.JoinLeaderboard(r.Context(), middleware.UserID(r.Context()),
		body.SeasonKey, body.DisplayName, body.ConsentVersion, body.CountyGID)
	switch {
	case errors.Is(err, ErrConsentMissing):
		handler.WriteError(w, http.StatusForbidden, "CONSENT_MISSING", "recognition consent is required")
		return
	case errors.Is(err, ErrNotEligible):
		handler.WriteError(w, http.StatusForbidden, "NOT_ELIGIBLE", "not eligible for the public leaderboard")
		return
	case err != nil && isValidation(err):
		validation(w, "displayName", err.Error())
		return
	case err != nil:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to join leaderboard", err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) LeaveLeaderboard(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	seasonKey := r.URL.Query().Get("seasonKey")
	if seasonKey == "" {
		validation(w, "seasonKey", "seasonKey is required")
		return
	}
	out, err := h.engine.LeaveLeaderboard(r.Context(), middleware.UserID(r.Context()), seasonKey)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to leave leaderboard", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) UpdateDisplayName(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	var body struct {
		DisplayName string `json:"displayName"`
	}
	if !decode(w, r, &body) {
		return
	}
	out, err := h.engine.UpdateDisplayName(r.Context(), middleware.UserID(r.Context()), body.DisplayName)
	switch {
	case errors.Is(err, ErrChangeWindow):
		handler.WriteError(w, http.StatusConflict, "CHANGE_WINDOW", "display name can change once every 30 days")
		return
	case err != nil && isValidation(err):
		validation(w, "displayName", err.Error())
		return
	case err != nil:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update display name", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) ReportLeaderboardEntry(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	var body struct {
		SeasonKey  string `json:"seasonKey"`
		SubjectRef string `json:"subjectRef"`
		Reason     string `json:"reason"`
		Details    string `json:"details"`
	}
	if !decode(w, r, &body) {
		return
	}
	valid := map[string]bool{"fraud": true, "abuse": true, "impersonation": true, "harassment": true, "display_name": true}
	if body.SeasonKey == "" || body.SubjectRef == "" || !valid[body.Reason] {
		validation(w, "reason", "seasonKey, subjectRef and a known reason are required")
		return
	}
	id, err := h.engine.ReportLeaderboardEntry(r.Context(), middleware.UserID(r.Context()),
		body.SeasonKey, body.SubjectRef, body.Reason, body.Details)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record report", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok": true, "id": id,
		"message": "Report received. We will review it within 48 hours.",
	})
}

// ---------------------------------------------------------------------------
// Admin — rules and badges
// ---------------------------------------------------------------------------

func (h *Handler) AdminListRules(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionRead) {
		return
	}
	items, hasMore, next, err := h.engine.ListRules(r.Context(),
		r.URL.Query().Get("status"), r.URL.Query().Get("cursor"), limitParam(r, 50))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list rules", err)
		return
	}
	writeJSON(w, http.StatusOK, page(items, hasMore, next))
}

func (h *Handler) AdminCreateRule(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionAuthorRule) {
		return
	}
	var body struct {
		RuleKey       string         `json:"ruleKey"`
		SourceType    string         `json:"sourceType"`
		EffectiveFrom *time.Time     `json:"effectiveFrom"`
		Criteria      map[string]any `json:"criteria"`
		XPAmount      int            `json:"xpAmount"`
		BadgeKey      *string        `json:"badgeKey"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.RuleKey == "" || body.SourceType == "" || body.XPAmount < 0 {
		validation(w, "ruleKey", "ruleKey, sourceType and a non-negative xpAmount are required")
		return
	}
	out, err := h.engine.CreateRule(r.Context(), middleware.UserID(r.Context()), body.RuleKey,
		body.SourceType, body.EffectiveFrom, body.Criteria, body.XPAmount, body.BadgeKey)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create rule", err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) AdminPublishRule(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionPublishRule) {
		return
	}
	out, err := h.engine.PublishRule(r.Context(), middleware.UserID(r.Context()),
		chi.URLParam(r, "id"), chimw.GetReqID(r.Context()))
	switch {
	case errors.Is(err, ErrSelfApproval):
		handler.WriteError(w, http.StatusConflict, "SELF_APPROVAL", "the publisher must differ from the author")
		return
	case errors.Is(err, ErrNotSimulated):
		handler.WriteError(w, http.StatusConflict, "NOT_SIMULATED", "simulate the rule before publishing")
		return
	case errors.Is(err, pgx.ErrNoRows):
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "rule not found")
		return
	case err != nil:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to publish rule", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AdminSimulateRule(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionSimulateRule) {
		return
	}
	var body struct {
		From *time.Time `json:"from"`
		To   *time.Time `json:"to"`
	}
	if r.ContentLength != 0 {
		if !decodeOptional(w, r, &body) {
			return
		}
	}
	out, err := h.engine.SimulateRule(r.Context(), middleware.UserID(r.Context()),
		chi.URLParam(r, "id"), body.From, body.To)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "rule not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to simulate rule", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AdminListBadges(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionRead) {
		return
	}
	items, err := h.engine.AdminBadges(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list badges", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) AdminCreateBadge(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionAuthorBadge) {
		return
	}
	var in BadgeDefinition
	if !decode(w, r, &in) {
		return
	}
	out, err := h.engine.CreateBadge(r.Context(), middleware.UserID(r.Context()), in)
	if err != nil {
		if isValidation(err) {
			validation(w, "badgeKey", err.Error())
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create badge", err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) AdminPublishBadge(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionPublishBadge) {
		return
	}
	out, err := h.engine.PublishBadge(r.Context(), middleware.UserID(r.Context()),
		chi.URLParam(r, "key"), chimw.GetReqID(r.Context()))
	switch {
	case errors.Is(err, ErrSelfApproval):
		handler.WriteError(w, http.StatusConflict, "SELF_APPROVAL", "the publisher must differ from the author")
		return
	case errors.Is(err, ErrMissingNameSW):
		handler.WriteError(w, http.StatusConflict, "MISSING_TRANSLATION", "both names must be reviewed before publish")
		return
	case errors.Is(err, pgx.ErrNoRows):
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "badge not found")
		return
	case err != nil:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to publish badge", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// Admin — seasons, moderation, backfill, adjustments, switches, audit
// ---------------------------------------------------------------------------

func (h *Handler) AdminListSeasons(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionRead) {
		return
	}
	items, err := h.engine.AdminSeasons(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list seasons", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) AdminCreateSeason(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionPrepareSeason) {
		return
	}
	var body struct {
		Key             string    `json:"key"`
		NameEn          string    `json:"nameEn"`
		NameSw          string    `json:"nameSw"`
		StartsAt        time.Time `json:"startsAt"`
		EndsAt          time.Time `json:"endsAt"`
		BadgeKey        *string   `json:"badgeKey"`
		XPAward         *int      `json:"xpAward"`
		MissionFamilies []string  `json:"missionFamilies"`
	}
	if !decode(w, r, &body) {
		return
	}
	xp := 500
	if body.XPAward != nil {
		xp = *body.XPAward
	}
	out, err := h.engine.CreateSeason(r.Context(), middleware.UserID(r.Context()), body.Key,
		body.NameEn, body.NameSw, body.StartsAt, body.EndsAt, body.BadgeKey, xp, body.MissionFamilies)
	if err != nil {
		if isValidation(err) {
			validation(w, "key", err.Error())
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create season", err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) AdminPublishSeason(w http.ResponseWriter, r *http.Request) {
	h.adminSeasonAction(w, r, contracts.PermRecognitionPublishSeason, "published", "season.publish")
}

func (h *Handler) AdminFreezeSeason(w http.ResponseWriter, r *http.Request) {
	h.adminSeasonAction(w, r, contracts.PermRecognitionCloseSeason, "frozen", "season.freeze")
}

func (h *Handler) AdminCloseSeason(w http.ResponseWriter, r *http.Request) {
	h.adminSeasonAction(w, r, contracts.PermRecognitionCloseSeason, "closed", "season.close")
}

func (h *Handler) adminSeasonAction(w http.ResponseWriter, r *http.Request, perm, status, action string) {
	if !h.ready(w) {
		return
	}
	if !requirePerm(w, r, perm) {
		return
	}
	out, err := h.engine.SetSeasonStatus(r.Context(), middleware.UserID(r.Context()),
		chi.URLParam(r, "id"), status, action, chimw.GetReqID(r.Context()))
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "season not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update season", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AdminModeration(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionRead) {
		return
	}
	items, hasMore, next, err := h.engine.ModerationCases(r.Context(),
		r.URL.Query().Get("status"), r.URL.Query().Get("cursor"), limitParam(r, 50))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list moderation cases", err)
		return
	}
	writeJSON(w, http.StatusOK, page(items, hasMore, next))
}

func (h *Handler) AdminDecideModeration(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionModerate) {
		return
	}
	var body struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	out, err := h.engine.DecideModerationCase(r.Context(), middleware.UserID(r.Context()),
		chi.URLParam(r, "id"), body.Decision, body.Reason, chimw.GetReqID(r.Context()))
	switch {
	case errors.Is(err, ErrAlreadyDecided):
		handler.WriteError(w, http.StatusConflict, "ALREADY_DECIDED", "the case already has a final decision")
		return
	case errors.Is(err, pgx.ErrNoRows):
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "case not found")
		return
	case err != nil && isValidation(err):
		validation(w, "reason", err.Error())
		return
	case err != nil:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to decide case", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AdminListBackfill(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionRead) {
		return
	}
	items, err := h.engine.ListBackfill(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list backfill batches", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) AdminCreateBackfill(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionBackfill) {
		return
	}
	var body struct {
		RuleKey     string    `json:"ruleKey"`
		RuleVersion int       `json:"ruleVersion"`
		From        time.Time `json:"from"`
		To          time.Time `json:"to"`
	}
	if !decode(w, r, &body) {
		return
	}
	out, err := h.engine.CreateBackfill(r.Context(), middleware.UserID(r.Context()),
		body.RuleKey, body.RuleVersion, body.From, body.To)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "rule version not found")
		return
	}
	if err != nil && isValidation(err) {
		validation(w, "ruleKey", err.Error())
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create backfill batch", err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) AdminApproveBackfill(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionApproveBackfill) {
		return
	}
	out, err := h.engine.ApproveBackfill(r.Context(), middleware.UserID(r.Context()),
		chi.URLParam(r, "id"), chimw.GetReqID(r.Context()))
	switch {
	case errors.Is(err, ErrSelfApproval):
		handler.WriteError(w, http.StatusConflict, "SELF_APPROVAL", "the approver must differ from the requester")
		return
	case errors.Is(err, ErrAlreadyDecided):
		handler.WriteError(w, http.StatusConflict, "ALREADY_APPROVED", "the batch is not awaiting approval")
		return
	case errors.Is(err, ErrKillSwitchOn):
		handler.WriteError(w, http.StatusConflict, "KILL_SWITCH", "the global kill switch is enabled")
		return
	case errors.Is(err, pgx.ErrNoRows):
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "batch not found")
		return
	case err != nil:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to run backfill", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AdminCreateAdjustment(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionAdjustXP) {
		return
	}
	var body struct {
		SubjectRef      string  `json:"subjectRef"`
		Amount          int     `json:"amount"`
		Reason          string  `json:"reason"`
		Note            string  `json:"note"`
		CompensatingFor *string `json:"compensatingFor"`
	}
	if !decode(w, r, &body) {
		return
	}
	// The frozen body has no approver field, so the approver is carried in an
	// explicit header (documented on the operation). The engine verifies it is
	// a distinct, active account holding recognition.approve_adjustment;
	// a header string alone is not an approval.
	approver := strings.TrimSpace(r.Header.Get("X-Second-Approver"))
	out, err := h.engine.CreateAdjustment(r.Context(), middleware.UserID(r.Context()), approver,
		body.SubjectRef, body.Amount, body.Reason, body.Note, body.CompensatingFor,
		chimw.GetReqID(r.Context()))
	switch {
	case errors.Is(err, ErrSelfApproval):
		handler.WriteError(w, http.StatusForbidden, "SECOND_APPROVER_REQUIRED",
			"a second approver is required and must differ from the actor")
		return
	case errors.Is(err, ErrApproverUnknown):
		handler.WriteError(w, http.StatusForbidden, "SECOND_APPROVER_UNKNOWN",
			"the second approver must identify an existing user")
		return
	case errors.Is(err, ErrApproverInactive):
		handler.WriteError(w, http.StatusForbidden, "SECOND_APPROVER_INACTIVE",
			"the second approver must identify an active user")
		return
	case errors.Is(err, ErrApproverRole):
		handler.WriteError(w, http.StatusForbidden, "SECOND_APPROVER_FORBIDDEN",
			"the second approver must hold recognition.approve_adjustment")
		return
	case err != nil && isValidation(err):
		validation(w, "note", err.Error())
		return
	case err != nil:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record adjustment", err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) AdminListKillSwitches(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionRead) {
		return
	}
	items, err := h.engine.KillSwitches(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list kill switches", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) AdminSetKillSwitch(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionKillSwitch) {
		return
	}
	var body struct {
		Enabled *bool  `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		validation(w, "enabled", "enabled is required")
		return
	}
	out, err := h.engine.SetKillSwitch(r.Context(), middleware.UserID(r.Context()),
		chi.URLParam(r, "key"), *body.Enabled, body.Reason, chimw.GetReqID(r.Context()))
	if err != nil && isValidation(err) {
		validation(w, "reason", err.Error())
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update kill switch", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AdminAudit(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) || !requirePerm(w, r, contracts.PermRecognitionAuditRead) {
		return
	}
	items, hasMore, next, err := h.engine.Audit(r.Context(),
		r.URL.Query().Get("entityType"), r.URL.Query().Get("cursor"), limitParam(r, 50))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list audit trail", err)
		return
	}
	writeJSON(w, http.StatusOK, page(items, hasMore, next))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func requirePerm(w http.ResponseWriter, r *http.Request, perm string) bool {
	if contracts.MustRoles().Can(string(middleware.RoleFromCtx(r.Context())), perm) {
		return true
	}
	handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "permission denied: "+perm)
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return false
	}
	return true
}

func validation(w http.ResponseWriter, field, message string) {
	handler.WriteValidationError(w, []model.ValidationError{{Field: field, Message: message}})
}

func page(items any, hasMore bool, next string) map[string]any {
	return map[string]any{"items": items, "nextCursor": nullable(next), "hasMore": hasMore}
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func limitParam(r *http.Request, fallback int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	if n > 100 {
		return 100
	}
	return n
}

func isValidation(err error) bool {
	msg := err.Error()
	switch {
	case errors.Is(err, ErrInvalidInput),
		errors.Is(err, ErrConsentMissing),
		errors.Is(err, ErrNotEligible):
		return true
	}
	return strings.Contains(msg, "must") || strings.Contains(msg, "required") ||
		strings.Contains(msg, "unsupported") || strings.Contains(msg, "invalid")
}

// decodeOptional accepts an empty body, for operations whose request body is
// optional in the frozen contract.
func decodeOptional(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return false
	}
	return true
}
