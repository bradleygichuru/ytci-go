package expo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/push"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

type AccountHandler struct {
	pool    *pgxpool.Pool
	r2store r2.Store
}

func NewAccountHandler(pool *pgxpool.Pool, r2store r2.Store) *AccountHandler {
	return &AccountHandler{pool: pool, r2store: r2store}
}

type deleteAccountRequest struct {
	Confirm   bool                 `json:"confirm"`
	Transfers []accountTransferReq `json:"transfers,omitempty"`
}

type accountTransferReq struct {
	ItineraryID     string `json:"itineraryId"`
	SuccessorUserID string `json:"successorUserId,omitempty"`
	Delete          bool   `json:"delete,omitempty"`
}

type deletionPlanMember struct {
	UserID             string     `json:"userId"`
	Name               string     `json:"name"`
	Role               string     `json:"role"`
	JoinedAt           time.Time  `json:"joinedAt"`
	WriteActions90Days int        `json:"writeActions90d"`
	LastActivity       *time.Time `json:"lastActivity,omitempty"`
}

type deletionPlanTrip struct {
	ID                      string               `json:"id"`
	Title                   string               `json:"title"`
	Deleted                 bool                 `json:"deleted"`
	Members                 []deletionPlanMember `json:"members"`
	ProposedSuccessorUserID string               `json:"proposedSuccessorUserId,omitempty"`
}

type deletionPlanResponse struct {
	OwnedTrips      []deletionPlanTrip `json:"ownedTrips"`
	MemberTripCount int                `json:"memberTripCount"`
	GeneratedAt     time.Time          `json:"generatedAt"`
}

type cleanupOp struct {
	sql   string
	label string
}

var deleteOps = []cleanupOp{
	{`DELETE FROM program_update_deliveries WHERE user_id = $1`, "program update deliveries"},
	{`DELETE FROM alert_deliveries WHERE user_id = $1`, "alert deliveries"},
	{`DELETE FROM alert_read_state WHERE user_id = $1`, "alert read state"},
	{`DELETE FROM program_update_read_state WHERE user_id = $1`, "program update read state"},
	{`DELETE FROM user_alert_location WHERE user_id = $1`, "alert location"},
	{`DELETE FROM alert_preferences WHERE user_id = $1`, "alert preferences"},
	{`DELETE FROM sessions WHERE user_id = $1`, "sessions"},
	{`DELETE FROM accounts WHERE user_id = $1`, "accounts"},
	{`DELETE FROM bucket_list_items WHERE user_id = $1`, "bucket list"},
	{`DELETE FROM push_tokens WHERE user_id = $1`, "push tokens"},
	{`DELETE FROM app_opens WHERE user_id = $1`, "app opens"},
	{`DELETE FROM analytics_events WHERE user_id = $1`, "analytics events"},
	{`DELETE FROM analytics_id_map WHERE user_id = $1`, "analytics mapping"},
	{`DELETE FROM user_profiles WHERE user_id = $1`, "user profile"},
	{`DELETE FROM story_interactions WHERE user_id = $1`, "interactions"},
	{`DELETE FROM comment_interactions WHERE user_id = $1`, "comment interactions"},
	{`DELETE FROM event_saves WHERE user_id = $1`, "event saves"},
	{`DELETE FROM audit_logs WHERE user_id = $1`, "audit logs"},
	{`DELETE FROM pending_media_uploads WHERE user_id = $1`, "pending uploads"},
}

var anonymizeOps = []cleanupOp{
	{`UPDATE stories SET creator_id = NULL WHERE creator_id = $1`, "stories"},
	{`UPDATE story_comments SET author_id = NULL WHERE author_id = $1`, "comments"},
	{`UPDATE story_reports SET reported_by = NULL WHERE reported_by = $1`, "reports"},
	{`UPDATE report_jobs SET requested_by = NULL WHERE requested_by = $1`, "report jobs"},
	{`UPDATE challenge_progress SET user_id = NULL WHERE user_id = $1`, "challenge progress"},
	{`UPDATE course_enrollments SET user_id = NULL WHERE user_id = $1`, "enrollments"},
	{`UPDATE conservation_evidence SET user_id = NULL WHERE user_id = $1`, "conservation evidence"},
	{`UPDATE consent_records SET user_id = 'deleted-' || gen_random_uuid()::text WHERE user_id = $1`, "consent records"},
}

// DeletionPlan lists every trip the user owns and proposes a successor for
// each one. The client must resolve every owned trip before deletion.
func (h *AccountHandler) DeletionPlan(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT i.id::text, i.title, (i.deleted_at IS NOT NULL)
		 FROM itineraries i
		 JOIN itinerary_members m ON m.itinerary_id = i.id
		 WHERE m.user_id = $1 AND m.role = 'owner'
		 ORDER BY (i.deleted_at IS NOT NULL), i.created_at DESC`,
		userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load owned trips", err)
		return
	}
	defer rows.Close()

	trips := make([]deletionPlanTrip, 0)
	for rows.Next() {
		var trip deletionPlanTrip
		if err := rows.Scan(&trip.ID, &trip.Title, &trip.Deleted); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to scan owned trip", err)
			return
		}
		members, err := h.deletionPlanMembers(r.Context(), h.pool, trip.ID, userID)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load trip members", err)
			return
		}
		trip.Members = members
		if len(members) > 0 && !trip.Deleted {
			trip.ProposedSuccessorUserID = members[0].UserID
		}
		trips = append(trips, trip)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate owned trips", err)
		return
	}

	var memberTripCount int
	_ = h.pool.QueryRow(r.Context(),
		`SELECT count(*)::int FROM itinerary_members WHERE user_id = $1 AND role <> 'owner'`,
		userID).Scan(&memberTripCount)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(deletionPlanResponse{
		OwnedTrips:      trips,
		MemberTripCount: memberTripCount,
		GeneratedAt:     time.Now().UTC(),
	})
}

type accountQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (h *AccountHandler) deletionPlanMembers(ctx context.Context, q accountQuerier, itineraryID, ownerID string) ([]deletionPlanMember, error) {
	rows, err := q.Query(ctx,
		`SELECT m.user_id, COALESCE(u.name, 'Former member'), m.role, m.joined_at,
		        COUNT(e.id) FILTER (WHERE e.created_at >= now() - interval '90 days')::int,
		        MAX(e.created_at)
		 FROM itinerary_members m
		 LEFT JOIN users u ON u.id = m.user_id
		 LEFT JOIN trip_events e ON e.itinerary_id = m.itinerary_id AND e.actor_id = m.user_id
		 WHERE m.itinerary_id = $1 AND m.user_id <> $2
		 GROUP BY m.user_id, u.name, m.role, m.joined_at
		 ORDER BY 5 DESC,
		          CASE m.role WHEN 'editor' THEN 0 WHEN 'viewer' THEN 1 ELSE 2 END,
		          m.joined_at ASC, MAX(e.created_at) DESC NULLS LAST`,
		itineraryID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := make([]deletionPlanMember, 0)
	for rows.Next() {
		var member deletionPlanMember
		if err := rows.Scan(&member.UserID, &member.Name, &member.Role, &member.JoinedAt,
			&member.WriteActions90Days, &member.LastActivity); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (h *AccountHandler) Delete(w http.ResponseWriter, r *http.Request) {
	var req deleteAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if !req.Confirm {
		handler.WriteError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "set confirm to true to proceed")
		return
	}

	userID := middleware.UserID(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to begin transaction", err)
		return
	}
	defer tx.Rollback(r.Context())

	type ownedTrip struct {
		ID      string
		Title   string
		Deleted bool
	}
	rows, err := tx.Query(r.Context(),
		`SELECT i.id::text, i.title, (i.deleted_at IS NOT NULL)
		 FROM itineraries i
		 JOIN itinerary_members m ON m.itinerary_id = i.id AND m.user_id = $1 AND m.role = 'owner'
		 FOR UPDATE OF i`,
		userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to lock owned trips", err)
		return
	}
	owned := make([]ownedTrip, 0)
	ownedIDs := make(map[string]bool)
	for rows.Next() {
		var trip ownedTrip
		if err := rows.Scan(&trip.ID, &trip.Title, &trip.Deleted); err != nil {
			rows.Close()
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to scan owned trip", err)
			return
		}
		owned = append(owned, trip)
		ownedIDs[trip.ID] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate owned trips", err)
		return
	}

	decisions := make(map[string]accountTransferReq, len(req.Transfers))
	for _, transfer := range req.Transfers {
		if transfer.ItineraryID == "" {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_TRANSFER", "itineraryId is required")
			return
		}
		if _, exists := decisions[transfer.ItineraryID]; exists {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_TRANSFER", "duplicate itinerary decision")
			return
		}
		if !ownedIDs[transfer.ItineraryID] {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_TRANSFER", "decision is not for an owned trip")
			return
		}
		decisions[transfer.ItineraryID] = transfer
	}

	missing := make([]string, 0)
	for _, trip := range owned {
		decision, ok := decisions[trip.ID]
		if !ok {
			missing = append(missing, trip.ID)
			continue
		}
		if trip.Deleted && !decision.Delete {
			handler.WriteError(w, http.StatusBadRequest, "TRIP_DELETED", "a deleted trip can only be deleted")
			return
		}
		if !decision.Delete {
			if decision.SuccessorUserID == "" {
				handler.WriteError(w, http.StatusBadRequest, "NO_SUCCESSOR", "choose a successor or delete the trip")
				return
			}
			var successorName string
			err := tx.QueryRow(r.Context(),
				`SELECT COALESCE(u.name, 'Member')
				 FROM itinerary_members m JOIN users u ON u.id = m.user_id
				 WHERE m.itinerary_id = $1 AND m.user_id = $2 AND m.role <> 'owner'`,
				trip.ID, decision.SuccessorUserID).Scan(&successorName)
			if errors.Is(err, pgx.ErrNoRows) {
				handler.WriteError(w, http.StatusBadRequest, "INVALID_SUCCESSOR", "the chosen successor is not a member of this trip")
				return
			}
			if err != nil {
				handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to validate successor", err)
				return
			}
		}
	}
	if len(missing) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":         "DECISION_REQUIRED",
				"message":      "every owned trip needs a successor or deletion decision",
				"itineraryIds": missing,
			},
		})
		return
	}

	type ownershipNotice struct {
		UserID string
		Title  string
	}
	notices := make([]ownershipNotice, 0)
	for _, trip := range owned {
		decision := decisions[trip.ID]
		if decision.Delete {
			cmd, err := tx.Exec(r.Context(),
				`UPDATE itineraries
				 SET deleted_at = COALESCE(deleted_at, now()),
				     members_version = members_version + 1, updated_at = now()
				 WHERE id = $1`,
				trip.ID)
			if err != nil {
				handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete trip", err)
				return
			}
			if cmd.RowsAffected() > 0 && !trip.Deleted {
				if err := recordTripEvent(r.Context(), tx, trip.ID, userID, "plan", "deleted",
					"itinerary", trip.ID, "Deleted trip during account deletion", nil, nil, ""); err != nil {
					handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record trip deletion", err)
					return
				}
			}
			continue
		}

		if _, err := tx.Exec(r.Context(),
			`UPDATE itinerary_members SET role = 'editor'
			 WHERE itinerary_id = $1 AND user_id = $2`,
			trip.ID, userID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to demote owner", err)
			return
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE itinerary_members SET role = 'owner'
			 WHERE itinerary_id = $1 AND user_id = $2`,
			trip.ID, decision.SuccessorUserID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to promote successor", err)
			return
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE itineraries
			 SET user_id = $2, members_version = members_version + 1, updated_at = now()
			 WHERE id = $1`,
			trip.ID, decision.SuccessorUserID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to transfer trip", err)
			return
		}
		var successorName string
		_ = tx.QueryRow(r.Context(), `SELECT name FROM users WHERE id = $1`, decision.SuccessorUserID).Scan(&successorName)
		if err := recordTripEvent(r.Context(), tx, trip.ID, userID, "members", "ownership_transferred",
			"member", decision.SuccessorUserID, "Transferred ownership to "+successorName,
			map[string]any{"ownerId": userID}, map[string]any{"ownerId": decision.SuccessorUserID}, ""); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record ownership transfer", err)
			return
		}
		notices = append(notices, ownershipNotice{UserID: decision.SuccessorUserID, Title: trip.Title})
	}

	// Every remaining membership is about to disappear with the user. Bump the
	// version and record the departure so clients refetch the new member list.
	if _, err := tx.Exec(r.Context(),
		`UPDATE itineraries SET members_version = members_version + 1, updated_at = now()
		 WHERE id IN (SELECT itinerary_id FROM itinerary_members WHERE user_id = $1)`,
		userID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to bump member versions", err)
		return
	}
	memberRows, err := tx.Query(r.Context(),
		`SELECT itinerary_id FROM itinerary_members WHERE user_id = $1 AND role <> 'owner'`, userID)
	if err == nil {
		var memberTrips []string
		for memberRows.Next() {
			var id string
			if memberRows.Scan(&id) == nil {
				memberTrips = append(memberTrips, id)
			}
		}
		memberRows.Close()
		for _, id := range memberTrips {
			_ = recordTripEvent(r.Context(), tx, id, userID, "members", "member_removed",
				"member", userID, "Account deleted", nil, nil, "")
		}
	}

	// Keep participant rows and expense shares for the ledger, but anonymize
	// the name so account deletion still removes personal data.
	if _, err := tx.Exec(r.Context(),
		`UPDATE trip_participants
		 SET removed_at = COALESCE(removed_at, now()), display_name = 'Former member'
		 WHERE user_id = $1`,
		userID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to retain participant records", err)
		return
	}

	var remainingOwners int
	if err := tx.QueryRow(r.Context(),
		`SELECT count(*)::int
		 FROM itineraries i JOIN itinerary_members m ON m.itinerary_id = i.id
		 WHERE m.user_id = $1 AND m.role = 'owner' AND i.deleted_at IS NULL`,
		userID).Scan(&remainingOwners); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to verify ownership", err)
		return
	}
	if remainingOwners > 0 {
		handler.WriteError(w, http.StatusConflict, "OWNERSHIP_REQUIRED",
			"every active owned trip needs a successor or deletion decision")
		return
	}

	pendingKeys := h.collectPendingMediaKeys(r.Context(), tx, userID)

	if !h.execCleanupOps(r.Context(), tx, w, r, "delete", deleteOps, userID) {
		return
	}
	if !h.execCleanupOps(r.Context(), tx, w, r, "anonymize", anonymizeOps, userID) {
		return
	}

	if _, err := tx.Exec(r.Context(), `DELETE FROM users WHERE id = $1`, userID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete user", err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit transaction", err)
		return
	}

	for _, notice := range notices {
		_ = push.EnqueueUser(context.Background(), h.pool, notice.UserID,
			"You now own a trip", "Ownership of \""+notice.Title+"\" was transferred to you.",
			map[string]any{"type": "trip", "title": notice.Title})
	}

	if h.r2store != nil && len(pendingKeys) > 0 {
		go h.cleanupR2(context.Background(), pendingKeys)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status": "deleted", "transferred": len(notices),
		"deletedTrips": len(owned) - len(notices),
	})
}

func (h *AccountHandler) execCleanupOps(ctx context.Context, tx pgx.Tx, w http.ResponseWriter, r *http.Request, verb string, ops []cleanupOp, userID string) bool {
	for _, op := range ops {
		if _, err := tx.Exec(ctx, op.sql, userID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR",
				fmt.Sprintf("failed to %s %s", verb, op.label), err)
			return false
		}
	}
	return true
}

func (h *AccountHandler) collectPendingMediaKeys(ctx context.Context, tx pgx.Tx, userID string) []string {
	if h.r2store == nil {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT object_key FROM pending_media_uploads WHERE user_id = $1`, userID)
	if err != nil {
		slog.Warn("failed to query pending media uploads", "user_id", userID, "error", err)
		return nil
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			slog.Warn("failed to scan pending media key", "user_id", userID, "error", err)
			continue
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("error iterating pending media uploads", "user_id", userID, "error", err)
	}
	return keys
}

func (h *AccountHandler) cleanupR2(ctx context.Context, keys []string) {
	for _, key := range keys {
		if err := h.r2store.DeleteObject(ctx, key); err != nil {
			slog.Warn("failed to delete pending media from R2", "object_key", key, "error", err)
		}
	}
}
