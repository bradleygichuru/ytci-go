package analytics

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

// Handler serves the frozen analytics routes.
type Handler struct {
	pool  *pgxpool.Pool
	store *Store
	now   func() time.Time
}

// NewHandler returns an analytics handler over pool.
func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool, store: NewStore(pool), now: func() time.Time { return time.Now().UTC() }}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	return dec.Decode(v)
}

func under18Declared(r *http.Request, actorType string) bool {
	if actorType == ActorUnder18 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("X-Age-Declaration"))) {
	case "under_18", "under-18", "under18":
		return true
	default:
		return false
	}
}

// RegisterInstall mints or reuses an analytics_id for a guest or account.
func (h *Handler) RegisterInstall(w http.ResponseWriter, r *http.Request) {
	var req InstallRequest
	if err := decode(r, &req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if _, err := uuid.Parse(req.InstallID); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_INSTALL_ID", "installId must be a uuid")
		return
	}
	if req.Platform != "ios" && req.Platform != "android" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_PLATFORM", "platform must be ios or android")
		return
	}

	userID := middleware.UserID(r.Context())
	actorType := req.ActorType
	if actorType == "" {
		if userID != "" {
			actorType = ActorAccount
		} else {
			actorType = ActorGuest
		}
	}
	if actorType != ActorGuest && actorType != ActorAccount && actorType != ActorUnder18 {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ACTOR_TYPE", "actorType must be guest or account")
		return
	}
	if actorType == ActorUnder18 || under18Declared(r, req.ActorType) {
		// Ticket #346 is a release gate: identified analytics stays off until
		// consent/notice and the under-18 policy are signed.
		if !Under18IdentifiedEnabled() {
			handler.WriteError(w, http.StatusUnavailableForLegalReasons,
				"UNDER_18_IDENTIFIED_DISABLED",
				"identified analytics is disabled for self-declared under-18 readers")
			return
		}
		actorType = ActorUnder18
	}
	if actorType == ActorAccount && userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required for account installs")
		return
	}

	identity, err := h.store.UpsertIdentity(r.Context(), req, actorType, userID)
	if err != nil {
		var rej *RejectError
		if errors.As(err, &rej) {
			handler.WriteError(w, rej.Status, rej.Code, rej.Message)
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to mint identity", err)
		return
	}
	writeJSON(w, http.StatusCreated, identity)
}

// IngestBatch accepts an idempotent offline-queue flush.
func (h *Handler) IngestBatch(w http.ResponseWriter, r *http.Request) {
	var batch Batch
	if err := decode(r, &batch); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if err := validateBatch(&batch); err != nil {
		handler.WriteError(w, http.StatusBadRequest, err.Code, err.Message)
		return
	}
	if batch.ActorType == ActorUnder18 || under18Declared(r, batch.ActorType) {
		if !Under18IdentifiedEnabled() {
			handler.WriteError(w, http.StatusForbidden, "UNDER_18_IDENTIFIED_DISABLED",
				"identified analytics is disabled for self-declared under-18 readers")
			return
		}
		batch.ActorType = ActorUnder18
	}

	ack, err := h.store.IngestBatch(r.Context(), middleware.UserID(r.Context()), &batch, h.now())
	if err != nil {
		var rej *RejectError
		if errors.As(err, &rej) {
			handler.WriteError(w, rej.Status, rej.Code, rej.Message)
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to ingest batch", err)
		return
	}
	writeJSON(w, http.StatusAccepted, ack)
}

func validateBatch(b *Batch) *RejectError {
	if _, err := uuid.Parse(b.AnalyticsID); err != nil {
		return reject(http.StatusBadRequest, "INVALID_ANALYTICS_ID", "analyticsId must be a uuid")
	}
	if _, err := uuid.Parse(b.InstallID); err != nil {
		return reject(http.StatusBadRequest, "INVALID_INSTALL_ID", "installId must be a uuid")
	}
	if _, err := uuid.Parse(b.SessionID); err != nil {
		return reject(http.StatusBadRequest, "INVALID_SESSION_ID", "sessionId must be a uuid")
	}
	if b.Platform != "ios" && b.Platform != "android" {
		return reject(http.StatusBadRequest, "INVALID_PLATFORM", "platform must be ios or android")
	}
	if strings.TrimSpace(b.ConsentVersion) == "" {
		return reject(http.StatusBadRequest, "INVALID_CONSENT_VERSION", "consentVersion is required")
	}
	if strings.TrimSpace(b.AppVersion) == "" {
		return reject(http.StatusBadRequest, "INVALID_APP_VERSION", "appVersion is required")
	}
	if b.TZOffsetMin != nil && (*b.TZOffsetMin < -840 || *b.TZOffsetMin > 840) {
		return reject(http.StatusBadRequest, "INVALID_TZ_OFFSET", "tzOffsetMin must be between -840 and 840")
	}
	if len(b.Events) == 0 {
		return reject(http.StatusBadRequest, "EMPTY_BATCH", "events must not be empty")
	}
	// The frozen schema caps one batch at 100; the client queue itself holds
	// up to 500 and flushes in several batches.
	if len(b.Events) > 100 {
		return reject(http.StatusBadRequest, "BATCH_TOO_LARGE", "at most 100 events per batch")
	}
	return nil
}

// SetConsent records a versioned consent decision.
func (h *Handler) SetConsent(w http.ResponseWriter, r *http.Request) {
	var req ConsentWrite
	if err := decode(r, &req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	consent, err := h.store.SetConsent(r.Context(), req)
	if err != nil {
		var rej *RejectError
		if errors.As(err, &rej) {
			handler.WriteError(w, rej.Status, rej.Code, rej.Message)
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record consent", err)
		return
	}
	writeJSON(w, http.StatusOK, consent)
}

// WithdrawConsent records a denial and tells the client to clear its queue.
func (h *Handler) WithdrawConsent(w http.ResponseWriter, r *http.Request) {
	analyticsID := r.URL.Query().Get("analyticsId")
	if _, err := uuid.Parse(analyticsID); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ANALYTICS_ID", "analyticsId must be a uuid")
		return
	}
	consent, err := h.store.WithdrawConsent(r.Context(), analyticsID, r.URL.Query().Get("version"))
	if err != nil {
		var rej *RejectError
		if errors.As(err, &rej) {
			handler.WriteError(w, rej.Status, rej.Code, rej.Message)
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to withdraw consent", err)
		return
	}
	writeJSON(w, http.StatusOK, consent)
}

// Export returns the authenticated reader's analytics events and sessions.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	out, err := h.store.ExportByUser(r.Context(), userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to export analytics", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) dateRange(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	fromRaw := r.URL.Query().Get("from")
	toRaw := r.URL.Query().Get("to")
	from, err := time.Parse("2006-01-02", fromRaw)
	if err != nil {
		handler.WriteValidationError(w, []model.ValidationError{{Field: "from", Message: "from is required as YYYY-MM-DD"}})
		return time.Time{}, time.Time{}, false
	}
	to, err := time.Parse("2006-01-02", toRaw)
	if err != nil {
		handler.WriteValidationError(w, []model.ValidationError{{Field: "to", Message: "to is required as YYYY-MM-DD"}})
		return time.Time{}, time.Time{}, false
	}
	if to.Before(from) {
		handler.WriteValidationError(w, []model.ValidationError{{Field: "to", Message: "to must be on or after from"}})
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// AdminOverview serves GET /v1/admin/analytics/overview.
func (h *Handler) AdminOverview(w http.ResponseWriter, r *http.Request) {
	from, to, ok := h.dateRange(w, r)
	if !ok {
		return
	}
	out, err := h.store.Overview(r.Context(), from, to)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read overview", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// AdminJourneys serves GET /v1/admin/analytics/journeys.
func (h *Handler) AdminJourneys(w http.ResponseWriter, r *http.Request) {
	from, to, ok := h.dateRange(w, r)
	if !ok {
		return
	}
	journey := r.URL.Query().Get("journey")
	if journey != "" && !journeys[journey] {
		handler.WriteValidationError(w, []model.ValidationError{{Field: "journey", Message: "unknown journey"}})
		return
	}
	items, err := h.store.Journeys(r.Context(), from, to, journey)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read journeys", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// AdminFunnels serves GET /v1/admin/analytics/funnels.
func (h *Handler) AdminFunnels(w http.ResponseWriter, r *http.Request) {
	from, to, ok := h.dateRange(w, r)
	if !ok {
		return
	}
	items, err := h.store.Funnels(r.Context(), from, to)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read funnels", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// AdminRetention serves GET /v1/admin/analytics/retention.
func (h *Handler) AdminRetention(w http.ResponseWriter, r *http.Request) {
	from, to, ok := h.dateRange(w, r)
	if !ok {
		return
	}
	items, err := h.store.Retention(r.Context(), from, to)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read retention", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// AdminAdoption serves GET /v1/admin/analytics/adoption.
func (h *Handler) AdminAdoption(w http.ResponseWriter, r *http.Request) {
	from, to, ok := h.dateRange(w, r)
	if !ok {
		return
	}
	items, err := h.store.Adoption(r.Context(), from, to)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read adoption", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// AdminQuality serves GET /v1/admin/analytics/quality.
func (h *Handler) AdminQuality(w http.ResponseWriter, r *http.Request) {
	from, to, ok := h.dateRange(w, r)
	if !ok {
		return
	}
	out, err := h.store.Quality(r.Context(), from, to)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read quality", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type adminExportRequest struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Dataset string `json:"dataset,omitempty"`
}

// AdminExport accepts a CSV export job. Exports read aggregates only and
// contain no PII.
func (h *Handler) AdminExport(w http.ResponseWriter, r *http.Request) {
	var req adminExportRequest
	if err := decode(r, &req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	from, err := time.Parse("2006-01-02", req.From)
	if err != nil {
		handler.WriteValidationError(w, []model.ValidationError{{Field: "from", Message: "from is required as YYYY-MM-DD"}})
		return
	}
	to, err := time.Parse("2006-01-02", req.To)
	if err != nil {
		handler.WriteValidationError(w, []model.ValidationError{{Field: "to", Message: "to is required as YYYY-MM-DD"}})
		return
	}
	if to.Before(from) {
		handler.WriteValidationError(w, []model.ValidationError{{Field: "to", Message: "to must be on or after from"}})
		return
	}
	switch req.Dataset {
	case "", "overview", "journeys", "funnels", "retention", "adoption", "quality":
	default:
		handler.WriteValidationError(w, []model.ValidationError{{Field: "dataset", Message: "unknown dataset"}})
		return
	}
	writeJSON(w, http.StatusAccepted, Acknowledgement{
		OK:      true,
		ID:      uuid.NewString(),
		Message: "export job accepted; aggregates only, no personal data",
	})
}

type erasureSubject struct {
	SubjectType string `json:"subjectType"`
	SubjectRef  string `json:"subjectRef"`
}

// AdminErasure requests erasure for a user or analytics_id and tombstones the
// identity so raw rows are removed immediately.
func (h *Handler) AdminErasure(w http.ResponseWriter, r *http.Request) {
	var req erasureSubject
	if err := decode(r, &req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	out, err := h.store.RequestErasureAs(r.Context(), req.SubjectType, req.SubjectRef, middleware.UserID(r.Context()))
	if err != nil {
		var rej *RejectError
		if errors.As(err, &rej) {
			handler.WriteError(w, rej.Status, rej.Code, rej.Message)
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to request erasure", err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
