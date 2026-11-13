package expo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/collab"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/push"
)

type CollaborationHandler struct {
	pool *pgxpool.Pool
	hub  *collab.Hub

	createLimiter      *collab.Limiter
	redeemIPLimiter    *collab.Limiter
	redeemUserLimiter  *collab.Limiter
	previewIPLimiter   *collab.Limiter
	previewCodeLimiter *collab.Limiter
}

func NewCollaborationHandler(pool *pgxpool.Pool, hub *collab.Hub) *CollaborationHandler {
	return &CollaborationHandler{
		pool:               pool,
		hub:                hub,
		createLimiter:      collab.NewLimiter(10, 24*time.Hour),
		redeemIPLimiter:    collab.NewLimiter(10, time.Hour),
		redeemUserLimiter:  collab.NewLimiter(20, 24*time.Hour),
		previewIPLimiter:   collab.NewLimiter(120, time.Minute),
		previewCodeLimiter: collab.NewLimiter(60, time.Minute),
	}
}

func writeCollabJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeCollabError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errInviteInvalid):
		handler.WriteError(w, http.StatusGone, "INVITE_INVALID", "this invite is invalid, expired, or revoked")
	case errors.Is(err, errTripFull):
		handler.WriteError(w, http.StatusConflict, "TRIP_FULL", "this trip already has the maximum number of participants")
	case errors.Is(err, errEditorLimit):
		handler.WriteError(w, http.StatusConflict, "EDITOR_LIMIT", "this trip already has the maximum number of editors")
	case errors.Is(err, errItineraryNotFound):
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
	case errors.Is(err, errItineraryForbidden):
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you do not have permission to do that")
	case errors.Is(err, collab.ErrTooManyStreams):
		w.Header().Set("Retry-After", "5")
		handler.WriteError(w, http.StatusServiceUnavailable, "TOO_MANY_STREAMS", "too many open trip streams")
	default:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "collaboration request failed", err)
	}
}

func decodeCollabBody(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func (h *CollaborationHandler) membersVersion(r *http.Request, itineraryID string) int {
	var version int
	if err := h.pool.QueryRow(r.Context(),
		`SELECT members_version FROM itineraries WHERE id = $1`, itineraryID,
	).Scan(&version); err != nil {
		return 0
	}
	return version
}

func (h *CollaborationHandler) publishMembersChange(r *http.Request, itineraryID, action, actorID string) {
	version := h.membersVersion(r, itineraryID)
	_ = collab.PublishChange(r.Context(), h.pool, itineraryID, "members", action, version, actorID)
}

func (h *CollaborationHandler) itineraryTitle(r *http.Request, itineraryID string) string {
	var title string
	_ = h.pool.QueryRow(r.Context(), `SELECT title FROM itineraries WHERE id = $1`, itineraryID).Scan(&title)
	return title
}

func (h *CollaborationHandler) pushUser(r *http.Request, userID, title, body string, itineraryID string) {
	if userID == "" {
		return
	}
	_ = push.EnqueueUser(r.Context(), h.pool, userID, title, body, map[string]any{
		"type":        "trip",
		"itineraryId": itineraryID,
		"route":       "/itinerary/" + itineraryID,
	})
}

func (h *CollaborationHandler) pushTripEditors(r *http.Request, itineraryID, excludeUserID, title, body string) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT user_id FROM itinerary_members
		 WHERE itinerary_id = $1 AND role IN ('owner', 'editor') AND user_id <> $2`,
		itineraryID, excludeUserID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err == nil {
			h.pushUser(r, userID, title, body, itineraryID)
		}
	}
}

func (h *CollaborationHandler) requireMembersVersion(w http.ResponseWriter, r *http.Request, access *itineraryAccess, expected int) bool {
	if expected > 0 && expected != access.MembersVersion {
		writeVersionConflictFor(w, "members", access.MembersVersion)
		return false
	}
	return true
}

// checkMembersVersionTx is the authoritative check for concurrent writes. The
// pre-transaction check above is only a fast path; this one holds the row lock.
func (h *CollaborationHandler) checkMembersVersionTx(w http.ResponseWriter, r *http.Request, tx pgx.Tx, itineraryID string, expected int) bool {
	if expected <= 0 {
		return true
	}
	var current int
	if err := tx.QueryRow(r.Context(),
		`SELECT members_version FROM itineraries WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
		itineraryID,
	).Scan(&current); err != nil {
		writeCollabError(w, r, err)
		return false
	}
	if current != expected {
		writeVersionConflictFor(w, "members", current)
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Members and invites
// ---------------------------------------------------------------------------

func (h *CollaborationHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()), roleViewer)
	if err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}
	members, err := ListMembers(r.Context(), h.pool, itineraryID)
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	writeCollabJSON(w, http.StatusOK, map[string]any{
		"members":        members,
		"membersVersion": access.MembersVersion,
	})
}

type createInviteRequest struct {
	Role            string `json:"role"`
	MaxUses         int    `json:"maxUses"`
	ExpectedVersion int    `json:"expectedVersion"`
}

func (h *CollaborationHandler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	userID := middleware.UserID(r.Context())
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, userID, roleEditor)
	if err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}
	if !h.createLimiter.Allow(userID) {
		w.Header().Set("Retry-After", "3600")
		handler.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many invites created today")
		return
	}
	var req createInviteRequest
	if err := decodeCollabBody(r, &req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Role == "" {
		req.Role = roleViewer
	}
	if req.Role != roleEditor && req.Role != roleViewer {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ROLE", "invite role must be editor or viewer")
		return
	}
	if access.Role == roleEditor && req.Role == roleEditor {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "editors can only invite viewers")
		return
	}
	if !h.requireMembersVersion(w, r, access, req.ExpectedVersion) {
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if !h.checkMembersVersionTx(w, r, tx, itineraryID, req.ExpectedVersion) {
		return
	}

	code, inviteID, err := CreateInvite(r.Context(), tx, itineraryID, userID, req.Role, req.MaxUses)
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	var maxUses int
	var expiresAt time.Time
	if err := tx.QueryRow(r.Context(),
		`SELECT max_uses, expires_at FROM itinerary_invites WHERE id = $1`, inviteID,
	).Scan(&maxUses, &expiresAt); err != nil {
		writeCollabError(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeCollabError(w, r, err)
		return
	}
	h.publishMembersChange(r, itineraryID, "invite_created", userID)
	writeCollabJSON(w, http.StatusCreated, map[string]any{
		"code":      code,
		"inviteId":  inviteID,
		"role":      req.Role,
		"maxUses":   maxUses,
		"expiresAt": expiresAt,
	})
}

type inviteListItem struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	MaxUses   int       `json:"maxUses"`
	UseCount  int       `json:"useCount"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

func (h *CollaborationHandler) ListInvites(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if _, err := requireItineraryRole(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()), roleEditor); err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, role, max_uses, use_count, expires_at, created_at
		 FROM itinerary_invites
		 WHERE itinerary_id = $1 AND revoked_at IS NULL AND expires_at > now()
		 ORDER BY created_at DESC`,
		itineraryID)
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	defer rows.Close()
	invites := make([]inviteListItem, 0)
	for rows.Next() {
		var item inviteListItem
		if err := rows.Scan(&item.ID, &item.Role, &item.MaxUses, &item.UseCount, &item.ExpiresAt, &item.CreatedAt); err != nil {
			writeCollabError(w, r, err)
			return
		}
		invites = append(invites, item)
	}
	writeCollabJSON(w, http.StatusOK, map[string]any{"invites": invites})
}

func (h *CollaborationHandler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	inviteID := r.PathValue("inviteId")
	userID := middleware.UserID(r.Context())
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, userID, roleOwner)
	if err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}

	var inviteItineraryID, inviteRole string
	err = h.pool.QueryRow(r.Context(),
		`SELECT itinerary_id, role FROM itinerary_invites WHERE id = $1`, inviteID,
	).Scan(&inviteItineraryID, &inviteRole)
	if errors.Is(err, pgx.ErrNoRows) || inviteItineraryID != itineraryID {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "invite not found")
		return
	}
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	var req struct {
		ExpectedVersion int `json:"expectedVersion"`
	}
	if r.ContentLength > 0 {
		_ = decodeCollabBody(r, &req)
	}
	if !h.requireMembersVersion(w, r, access, req.ExpectedVersion) {
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if !h.checkMembersVersionTx(w, r, tx, itineraryID, req.ExpectedVersion) {
		return
	}
	if err := RevokeInvite(r.Context(), tx, inviteID, userID); err != nil {
		writeCollabError(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeCollabError(w, r, err)
		return
	}
	h.publishMembersChange(r, itineraryID, "invite_revoked", userID)
	writeCollabJSON(w, http.StatusOK, map[string]any{"status": "revoked"})
}

type redeemInviteRequest struct {
	Code string `json:"code"`
}

func (h *CollaborationHandler) RedeemInvite(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	var req redeemInviteRequest
	if err := decodeCollabBody(r, &req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CODE", "invite code is required")
		return
	}
	ip := remoteIP(r)
	if !h.redeemIPLimiter.Allow(ip) || !h.redeemUserLimiter.Allow(userID) {
		w.Header().Set("Retry-After", "3600")
		handler.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many invite attempts")
		return
	}

	var usesBefore int
	_ = h.pool.QueryRow(r.Context(),
		`SELECT use_count FROM itinerary_invites WHERE code_hash = $1`, hashInviteCode(req.Code),
	).Scan(&usesBefore)

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	itineraryID, role, err := RedeemInvite(r.Context(), tx, req.Code, userID)
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeCollabError(w, r, err)
		return
	}
	var usesAfter int
	_ = h.pool.QueryRow(r.Context(),
		`SELECT use_count FROM itinerary_invites WHERE code_hash = $1`, hashInviteCode(req.Code),
	).Scan(&usesAfter)
	if usesAfter > usesBefore {
		h.publishMembersChange(r, itineraryID, "invite_redeemed", userID)
		h.pushTripEditors(r, itineraryID, userID, "Someone joined your trip", "A traveler joined "+h.itineraryTitle(r, itineraryID)+".")
	}
	writeCollabJSON(w, http.StatusOK, map[string]any{
		"itineraryId": itineraryID,
		"role":        role,
	})
}

func (h *CollaborationHandler) SetMemberRole(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	memberID := r.PathValue("userId")
	userID := middleware.UserID(r.Context())
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, userID, roleOwner)
	if err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}
	var req struct {
		Role            string `json:"role"`
		ExpectedVersion int    `json:"expectedVersion"`
	}
	if err := decodeCollabBody(r, &req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Role != roleEditor && req.Role != roleViewer {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ROLE", "member role must be editor or viewer")
		return
	}
	if !h.requireMembersVersion(w, r, access, req.ExpectedVersion) {
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if !h.checkMembersVersionTx(w, r, tx, itineraryID, req.ExpectedVersion) {
		return
	}
	if err := SetMemberRole(r.Context(), tx, itineraryID, memberID, req.Role, userID); err != nil {
		writeCollabError(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeCollabError(w, r, err)
		return
	}
	h.publishMembersChange(r, itineraryID, "role_changed", userID)
	h.pushUser(r, memberID, "Your trip role changed", "You are now a "+req.Role+" on "+h.itineraryTitle(r, itineraryID)+".", itineraryID)
	writeCollabJSON(w, http.StatusOK, map[string]any{"status": "updated"})
}

func (h *CollaborationHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	memberID := r.PathValue("userId")
	userID := middleware.UserID(r.Context())
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, userID, roleOwner)
	if err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}
	var req struct {
		ExpectedVersion int `json:"expectedVersion"`
	}
	if r.ContentLength > 0 {
		_ = decodeCollabBody(r, &req)
	}
	if !h.requireMembersVersion(w, r, access, req.ExpectedVersion) {
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if !h.checkMembersVersionTx(w, r, tx, itineraryID, req.ExpectedVersion) {
		return
	}
	if err := RemoveMember(r.Context(), tx, itineraryID, memberID, userID); err != nil {
		writeCollabError(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeCollabError(w, r, err)
		return
	}
	h.publishMembersChange(r, itineraryID, "member_removed", userID)
	if h.hub != nil {
		h.hub.DisconnectUser(itineraryID, memberID)
	}
	h.pushUser(r, memberID, "You were removed from a trip", "You no longer have access to "+h.itineraryTitle(r, itineraryID)+".", itineraryID)
	writeCollabJSON(w, http.StatusOK, map[string]any{"status": "removed"})
}

func (h *CollaborationHandler) LeaveTrip(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	userID := middleware.UserID(r.Context())
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, userID, roleViewer)
	if err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}
	if access.Role == roleOwner {
		handler.WriteError(w, http.StatusBadRequest, "OWNER_CANNOT_LEAVE", "transfer ownership or delete the trip instead")
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if err := RemoveMember(r.Context(), tx, itineraryID, userID, userID); err != nil {
		writeCollabError(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeCollabError(w, r, err)
		return
	}
	h.publishMembersChange(r, itineraryID, "member_left", userID)
	if h.hub != nil {
		h.hub.DisconnectUser(itineraryID, userID)
	}
	h.pushTripEditors(r, itineraryID, userID, "A traveler left your trip", "A traveler left "+h.itineraryTitle(r, itineraryID)+".")
	writeCollabJSON(w, http.StatusOK, map[string]any{"status": "left"})
}

// ---------------------------------------------------------------------------
// Presence and SSE
// ---------------------------------------------------------------------------

func (h *CollaborationHandler) GetPresence(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if _, err := requireItineraryRole(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()), roleViewer); err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}
	viewers, err := collab.Roster(r.Context(), h.pool, itineraryID)
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	writeCollabJSON(w, http.StatusOK, map[string]any{"viewers": viewers})
}

func (h *CollaborationHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	userID := middleware.UserID(r.Context())
	if _, err := requireItineraryRole(r.Context(), h.pool, itineraryID, userID, roleViewer); err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}
	var req struct {
		Scope   string `json:"scope"`
		Visible *bool  `json:"visible"`
	}
	if r.ContentLength > 0 {
		_ = decodeCollabBody(r, &req)
	}
	if req.Visible != nil && !*req.Visible {
		_ = collab.DeletePresence(r.Context(), h.pool, itineraryID, userID)
		_ = collab.PublishPresence(r.Context(), h.pool, itineraryID)
		writeCollabJSON(w, http.StatusOK, map[string]any{"viewers": []collab.Viewer{}})
		return
	}
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = "plan"
	}
	if len(scope) > 32 {
		scope = scope[:32]
	}
	viewers, err := collab.UpsertPresence(r.Context(), h.pool, itineraryID, userID, scope)
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	_ = collab.PublishPresence(r.Context(), h.pool, itineraryID)
	writeCollabJSON(w, http.StatusOK, map[string]any{"viewers": viewers})
}

func (h *CollaborationHandler) Stream(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	userID := middleware.UserID(r.Context())
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, userID, roleViewer)
	if err != nil {
		// A revoked member should stop reconnecting rather than hammer.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if h.hub == nil {
		w.Header().Set("Retry-After", "15")
		handler.WriteError(w, http.StatusServiceUnavailable, "STREAM_UNAVAILABLE", "trip events are unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		handler.WriteError(w, http.StatusInternalServerError, "STREAM_UNAVAILABLE", "streaming is unavailable")
		return
	}
	sub, err := h.hub.Subscribe(itineraryID, userID)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		writeCollabError(w, r, err)
		return
	}
	defer func() {
		h.hub.Unsubscribe(sub)
		if !h.hub.HasSubscription(itineraryID, userID) {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = collab.DeletePresence(cleanupCtx, h.pool, itineraryID, userID)
			_ = collab.PublishPresence(cleanupCtx, h.pool, itineraryID)
			cleanupCancel()
		}
	}()

	viewers, err := collab.UpsertPresence(r.Context(), h.pool, itineraryID, userID, "plan")
	if err != nil {
		writeCollabError(w, r, err)
		return
	}

	headers := w.Header()
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-store")
	headers.Set("X-Accel-Buffering", "no")
	headers.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	controller := http.NewResponseController(w)
	write := func(payload string) bool {
		// HTTP/2 may not support write deadlines. A failed deadline is not a
		// reason to drop the stream; a failed write is.
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := fmt.Fprint(w, payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	write("retry: 3000\n\n")
	ready, _ := json.Marshal(map[string]any{
		"itineraryId": itineraryID,
		"version":     access.PlanVersion,
		"serverTime":  time.Now().UTC(),
	})
	if !write("event: ready\ndata: " + string(ready) + "\n\n") {
		return
	}
	presence, _ := json.Marshal(map[string]any{"itineraryId": itineraryID, "viewers": viewers})
	if !write("event: presence\ndata: " + string(presence) + "\n\n") {
		return
	}
	_ = collab.PublishPresence(r.Context(), h.pool, itineraryID)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.hub.Done():
			write("event: shutdown\ndata: {\"reason\":\"server\"}\n\n")
			return
		case ev, ok := <-sub.Events:
			if !ok {
				return
			}
			if ev.Type == "shutdown" {
				write("event: shutdown\ndata: {\"reason\":\"server\"}\n\n")
				return
			}
			if ev.Type == "revoked" {
				write("event: shutdown\ndata: {\"reason\":\"revoked\"}\n\n")
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if ev.Type == "change" {
				if !write("event: change\ndata: " + string(payload) + "\n\n") {
					return
				}
			} else if !write("event: " + ev.Type + "\ndata: " + string(payload) + "\n\n") {
				return
			}
		case <-ticker.C:
			if !write(": keepalive\n\n") {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Signed-out preview
// ---------------------------------------------------------------------------

type previewStop struct {
	Title           string `json:"title"`
	Description     string `json:"description,omitempty"`
	StartTime       string `json:"startTime,omitempty"`
	Category        string `json:"category,omitempty"`
	DestinationName string `json:"destinationName,omitempty"`
}

type previewDay struct {
	Day   int           `json:"day"`
	Stops []previewStop `json:"stops"`
}

func (h *CollaborationHandler) Preview(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CODE", "invite code is required")
		return
	}
	ip := remoteIP(r)
	if !h.previewIPLimiter.Allow(ip) || !h.previewCodeLimiter.Allow(code) {
		w.Header().Set("Retry-After", "60")
		handler.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many preview requests")
		return
	}

	var (
		itineraryID string
		role        string
		maxUses     int
		useCount    int
		expiresAt   time.Time
		revoked     bool
		title       string
		status      string
		inputsRaw   string
	)
	err := h.pool.QueryRow(r.Context(),
		`SELECT inv.itinerary_id, inv.role, inv.max_uses, inv.use_count, inv.expires_at,
		        (inv.revoked_at IS NOT NULL), i.title, i.status, i.inputs::text
		 FROM itinerary_invites inv
		 JOIN itineraries i ON i.id = inv.itinerary_id
		 WHERE inv.code_hash = $1 AND i.deleted_at IS NULL`,
		hashInviteCode(code),
	).Scan(&itineraryID, &role, &maxUses, &useCount, &expiresAt, &revoked, &title, &status, &inputsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		writeCollabError(w, r, errInviteInvalid)
		return
	}
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	if revoked || !expiresAt.After(time.Now()) || useCount >= maxUses {
		writeCollabError(w, r, errInviteInvalid)
		return
	}

	participants, err := countActiveParticipants(r.Context(), h.pool, itineraryID)
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	if participants >= maxItineraryParticipants {
		writeCollabError(w, r, errTripFull)
		return
	}
	if role == roleEditor {
		_, editors, err := countMembers(r.Context(), h.pool, itineraryID)
		if err != nil {
			writeCollabError(w, r, err)
			return
		}
		if editors >= maxItineraryEditors {
			writeCollabError(w, r, errEditorLimit)
			return
		}
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT s.day, COALESCE(s.title, ''), COALESCE(s.description, ''),
		        COALESCE(s.start_time, ''), COALESCE(s.category, ''),
		        COALESCE(d.name, '')
		 FROM itinerary_stops s
		 LEFT JOIN destinations d ON d.id = s.destination_id
		 WHERE s.itinerary_id = $1
		 ORDER BY s.day, s.display_order`,
		itineraryID)
	if err != nil {
		writeCollabError(w, r, err)
		return
	}
	defer rows.Close()

	dayMap := map[int]*previewDay{}
	dayOrder := make([]int, 0)
	for rows.Next() {
		var day int
		var stop previewStop
		if err := rows.Scan(&day, &stop.Title, &stop.Description, &stop.StartTime, &stop.Category, &stop.DestinationName); err != nil {
			writeCollabError(w, r, err)
			return
		}
		if dayMap[day] == nil {
			dayMap[day] = &previewDay{Day: day, Stops: make([]previewStop, 0)}
			dayOrder = append(dayOrder, day)
		}
		dayMap[day].Stops = append(dayMap[day].Stops, stop)
	}
	if err := rows.Err(); err != nil {
		writeCollabError(w, r, err)
		return
	}
	days := make([]previewDay, 0, len(dayOrder))
	for _, day := range dayOrder {
		days = append(days, *dayMap[day])
	}

	// The guest sees the plan shape only: no budget band, costs, receipts,
	// traveler names, history, presence, or member data.
	safeInputs := map[string]any{}
	var parsed map[string]any
	if json.Unmarshal([]byte(inputsRaw), &parsed) == nil {
		for _, key := range []string{"origin", "days", "startDate", "endDate", "interests", "transport", "pace", "groupType"} {
			if value, ok := parsed[key]; ok {
				safeInputs[key] = value
			}
		}
	}

	writeCollabJSON(w, http.StatusOK, map[string]any{
		"itineraryId": itineraryID,
		"title":       title,
		"status":      status,
		"inputs":      safeInputs,
		"days":        days,
	})
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
