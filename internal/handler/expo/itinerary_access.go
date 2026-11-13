package expo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

const (
	roleOwner  = "owner"
	roleEditor = "editor"
	roleViewer = "viewer"

	maxItineraryParticipants = 20
	maxItineraryEditors      = 10
	inviteCodeAlphabet       = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	defaultInviteMaxUses     = 5
	inviteTTL                = 7 * 24 * time.Hour
)

var (
	errItineraryNotFound  = errors.New("itinerary not found")
	errItineraryForbidden = errors.New("itinerary access forbidden")
	errInviteInvalid      = errors.New("invite is invalid, expired, or revoked")
	errTripFull           = errors.New("trip is full")
	errEditorLimit        = errors.New("editor limit reached")
)

// dbQuerier is satisfied by both *pgxpool.Pool and pgx.Tx, so the same
// access/audit helpers can run inside or outside a transaction.
type dbQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type itineraryAccess struct {
	Role           string
	PlanVersion    int
	BudgetVersion  int
	MembersVersion int
}

func roleRank(role string) int {
	switch role {
	case roleOwner:
		return 3
	case roleEditor:
		return 2
	case roleViewer:
		return 1
	default:
		return 0
	}
}

func loadItineraryAccess(ctx context.Context, q dbQuerier, itineraryID, userID string) (*itineraryAccess, error) {
	if itineraryID == "" || userID == "" {
		return nil, errItineraryNotFound
	}

	var a itineraryAccess
	err := q.QueryRow(ctx,
		`SELECT m.role, i.plan_version, i.budget_version, i.members_version
		 FROM itineraries i
		 JOIN itinerary_members m ON m.itinerary_id = i.id
		 WHERE i.id = $1 AND m.user_id = $2 AND i.deleted_at IS NULL`,
		itineraryID, userID,
	).Scan(&a.Role, &a.PlanVersion, &a.BudgetVersion, &a.MembersVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errItineraryNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func requireItineraryRole(ctx context.Context, q dbQuerier, itineraryID, userID, minRole string) (*itineraryAccess, error) {
	a, err := loadItineraryAccess(ctx, q, itineraryID, userID)
	if err != nil {
		return nil, err
	}
	if roleRank(a.Role) < roleRank(minRole) {
		return nil, errItineraryForbidden
	}
	return a, nil
}

func writeItineraryAccessError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errItineraryNotFound):
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
	case errors.Is(err, errItineraryForbidden):
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you do not have permission to do that")
	default:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check itinerary access", err)
	}
}

func writeVersionConflict(w http.ResponseWriter, currentVersion int) {
	writeVersionConflictFor(w, "plan", currentVersion)
}

func writeVersionConflictFor(w http.ResponseWriter, scope string, currentVersion int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    "VERSION_CONFLICT",
			"message": "This trip changed while you were editing.",
		},
		"scope":          scope,
		"currentVersion": currentVersion,
	})
}

// ---------------------------------------------------------------------------
// Plan snapshots and audit events
// ---------------------------------------------------------------------------

type planSnapshotStop struct {
	ID            string `json:"id"`
	DestinationID string `json:"destinationId,omitempty"`
	Day           int    `json:"day"`
	DisplayOrder  int    `json:"displayOrder"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	StartTime     string `json:"startTime,omitempty"`
	Category      string `json:"category,omitempty"`
	ImageURL      string `json:"imageUrl,omitempty"`
	EstimatedCost string `json:"estimatedCost,omitempty"`
}

type planSnapshot struct {
	Title  string             `json:"title"`
	Status string             `json:"status"`
	Inputs json.RawMessage    `json:"inputs"`
	Stops  []planSnapshotStop `json:"stops"`
}

func readPlanSnapshot(ctx context.Context, q dbQuerier, itineraryID string) ([]byte, error) {
	var title, status, inputs string
	if err := q.QueryRow(ctx,
		`SELECT title, status, inputs::text FROM itineraries WHERE id = $1`,
		itineraryID,
	).Scan(&title, &status, &inputs); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errItineraryNotFound
		}
		return nil, err
	}

	rows, err := q.Query(ctx,
		`SELECT id::text, COALESCE(destination_id::text, ''), day, display_order,
		        COALESCE(title, ''), COALESCE(description, ''), COALESCE(start_time, ''),
		        COALESCE(category, ''), COALESCE(image_url, ''), COALESCE(estimated_cost, '')
		 FROM itinerary_stops
		 WHERE itinerary_id = $1
		 ORDER BY day, display_order`,
		itineraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stops := make([]planSnapshotStop, 0)
	for rows.Next() {
		var s planSnapshotStop
		if err := rows.Scan(&s.ID, &s.DestinationID, &s.Day, &s.DisplayOrder,
			&s.Title, &s.Description, &s.StartTime, &s.Category, &s.ImageURL, &s.EstimatedCost); err != nil {
			return nil, err
		}
		stops = append(stops, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return json.Marshal(planSnapshot{
		Title:  title,
		Status: status,
		Inputs: json.RawMessage(inputs),
		Stops:  stops,
	})
}

func insertPlanRevision(ctx context.Context, q dbQuerier, itineraryID string, version int, actorID, reason string) (string, error) {
	snapshot, err := readPlanSnapshot(ctx, q, itineraryID)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(snapshot)

	var actor any
	if actorID != "" {
		actor = actorID
	}

	var revisionID string
	err = q.QueryRow(ctx,
		`INSERT INTO plan_revisions (itinerary_id, version, snapshot, actor_id, reason, checksum)
		 VALUES ($1, $2, $3::jsonb, $4, $5, $6)
		 RETURNING id`,
		itineraryID, version, string(snapshot), actor, reason, hex.EncodeToString(sum[:]),
	).Scan(&revisionID)
	return revisionID, err
}

func bumpPlanVersion(ctx context.Context, q dbQuerier, itineraryID string) (int, error) {
	var version int
	err := q.QueryRow(ctx,
		`UPDATE itineraries
		 SET plan_version = plan_version + 1, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL
		 RETURNING plan_version`,
		itineraryID,
	).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errItineraryNotFound
	}
	return version, err
}

func bumpPlanVersionAndRevision(ctx context.Context, q dbQuerier, itineraryID, actorID, reason string) (int, string, error) {
	version, err := bumpPlanVersion(ctx, q, itineraryID)
	if err != nil {
		return 0, "", err
	}
	revisionID, err := insertPlanRevision(ctx, q, itineraryID, version, actorID, reason)
	if err != nil {
		return 0, "", err
	}
	return version, revisionID, nil
}

func bumpMembersVersion(ctx context.Context, q dbQuerier, itineraryID string) error {
	_, err := q.Exec(ctx,
		`UPDATE itineraries
		 SET members_version = members_version + 1, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		itineraryID)
	return err
}

func recordTripEvent(ctx context.Context, q dbQuerier, itineraryID, actorID, scope, action, entityType, entityID, summary string, before, after any, revisionID string) error {
	var beforeJSON, afterJSON string
	if before != nil {
		b, err := json.Marshal(before)
		if err != nil {
			return err
		}
		beforeJSON = string(b)
	}
	if after != nil {
		a, err := json.Marshal(after)
		if err != nil {
			return err
		}
		afterJSON = string(a)
	}

	var actor any
	if actorID != "" {
		actor = actorID
	}
	requestID := chimw.GetReqID(ctx)

	_, err := q.Exec(ctx,
		`INSERT INTO trip_events
		 (itinerary_id, actor_id, scope, action, entity_type, entity_id, summary,
		  before_json, after_json, revision_id, request_id)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7,
		         NULLIF($8, '')::jsonb, NULLIF($9, '')::jsonb, NULLIF($10, '')::uuid, NULLIF($11, ''))`,
		itineraryID, actor, scope, action, entityType, entityID, summary,
		beforeJSON, afterJSON, revisionID, requestID,
	)
	return err
}

// ---------------------------------------------------------------------------
// Invites
// ---------------------------------------------------------------------------

func generateInviteCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = inviteCodeAlphabet[int(buf[i])%len(inviteCodeAlphabet)]
	}
	return string(buf), nil
}

func normalizeInviteCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

func hashInviteCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeInviteCode(code)))
	return hex.EncodeToString(sum[:])
}

func countMembers(ctx context.Context, q dbQuerier, itineraryID string) (members, editors int, err error) {
	err = q.QueryRow(ctx,
		`SELECT COUNT(*)::int,
		        COUNT(*) FILTER (WHERE role IN ('owner', 'editor'))::int
		 FROM itinerary_members
		 WHERE itinerary_id = $1`,
		itineraryID,
	).Scan(&members, &editors)
	return members, editors, err
}

func countActiveParticipants(ctx context.Context, q dbQuerier, itineraryID string) (int, error) {
	var count int
	err := q.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM trip_participants
		 WHERE itinerary_id = $1 AND removed_at IS NULL`,
		itineraryID).Scan(&count)
	return count, err
}

func createInviteCap(role string, participants, editors int) int {
	remaining := maxItineraryParticipants - participants
	if role == roleEditor {
		editorRemaining := maxItineraryEditors - editors
		if editorRemaining < remaining {
			remaining = editorRemaining
		}
	}
	if remaining < 0 {
		return 0
	}
	return remaining
}

// CreateInvite creates a server-persisted invite and returns the plaintext code
// once. Only the code hash is stored.
func CreateInvite(ctx context.Context, q dbQuerier, itineraryID, createdBy, role string, maxUses int) (code, inviteID string, err error) {
	if role != roleEditor && role != roleViewer {
		return "", "", fmt.Errorf("invalid invite role %q", role)
	}

	var deleted bool
	if err := q.QueryRow(ctx,
		`SELECT deleted_at IS NOT NULL FROM itineraries WHERE id = $1`, itineraryID,
	).Scan(&deleted); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", errItineraryNotFound
		}
		return "", "", err
	}
	if deleted {
		return "", "", errItineraryNotFound
	}

	participants, err := countActiveParticipants(ctx, q, itineraryID)
	if err != nil {
		return "", "", err
	}
	_, editors, err := countMembers(ctx, q, itineraryID)
	if err != nil {
		return "", "", err
	}
	capacity := createInviteCap(role, participants, editors)
	if capacity <= 0 {
		if role == roleEditor && editors >= maxItineraryEditors {
			return "", "", errEditorLimit
		}
		return "", "", errTripFull
	}

	if maxUses <= 0 {
		maxUses = defaultInviteMaxUses
	}
	if maxUses > capacity {
		maxUses = capacity
	}

	expiresAt := time.Now().Add(inviteTTL)
	var createdByArg any
	if createdBy != "" {
		createdByArg = createdBy
	}

	for attempt := 0; attempt < 4; attempt++ {
		code, err = generateInviteCode()
		if err != nil {
			return "", "", err
		}
		err = q.QueryRow(ctx,
			`INSERT INTO itinerary_invites
			 (itinerary_id, code_hash, role, created_by, expires_at, max_uses)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 RETURNING id`,
			itineraryID, hashInviteCode(code), role, createdByArg, expiresAt, maxUses,
		).Scan(&inviteID)
		if err == nil {
			break
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue
		}
		return "", "", err
	}
	if err != nil {
		return "", "", err
	}

	if err := bumpMembersVersion(ctx, q, itineraryID); err != nil {
		return "", "", err
	}

	_ = recordTripEvent(ctx, q, itineraryID, createdBy, "members", "invite_created",
		"invite", inviteID, "Created a "+role+" invite", nil,
		map[string]any{"role": role, "expiresAt": expiresAt, "maxUses": maxUses}, "")
	return code, inviteID, nil
}

// RedeemInvite validates and consumes an invite, adding the user as a member.
// It returns the existing role without consuming the invite if the user is
// already a member.
func RedeemInvite(ctx context.Context, tx pgx.Tx, code, userID string) (itineraryID, role string, err error) {
	var (
		inviteID  string
		createdBy string
		expiresAt time.Time
		maxUses   int
		useCount  int
		revoked   bool
	)
	err = tx.QueryRow(ctx,
		`SELECT id, itinerary_id, role, COALESCE(created_by, ''), expires_at, max_uses, use_count,
		        (revoked_at IS NOT NULL)
		 FROM itinerary_invites
		 WHERE code_hash = $1
		 FOR UPDATE`,
		hashInviteCode(code),
	).Scan(&inviteID, &itineraryID, &role, &createdBy, &expiresAt, &maxUses, &useCount, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", errInviteInvalid
	}
	if err != nil {
		return "", "", err
	}
	if revoked || !expiresAt.After(time.Now()) || useCount >= maxUses {
		return "", "", errInviteInvalid
	}

	var deleted bool
	if err := tx.QueryRow(ctx,
		`SELECT deleted_at IS NOT NULL FROM itineraries WHERE id = $1`, itineraryID,
	).Scan(&deleted); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", errInviteInvalid
		}
		return "", "", err
	}
	if deleted {
		return "", "", errInviteInvalid
	}

	var existingRole string
	err = tx.QueryRow(ctx,
		`SELECT role FROM itinerary_members WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, userID,
	).Scan(&existingRole)
	if err == nil {
		return itineraryID, existingRole, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}

	participants, err := countActiveParticipants(ctx, tx, itineraryID)
	if err != nil {
		return "", "", err
	}
	if participants >= maxItineraryParticipants {
		return "", "", errTripFull
	}
	if role == roleEditor {
		_, editors, err := countMembers(ctx, tx, itineraryID)
		if err != nil {
			return "", "", err
		}
		if editors >= maxItineraryEditors {
			return "", "", errEditorLimit
		}
	}

	var invitedBy any
	if createdBy != "" {
		invitedBy = createdBy
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO itinerary_members (itinerary_id, user_id, role, invited_by)
		 VALUES ($1, $2, $3, $4)`,
		itineraryID, userID, role, invitedBy,
	); err != nil {
		return "", "", err
	}
	if err := ensureMemberParticipant(ctx, tx, itineraryID, userID); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE itinerary_invites SET use_count = use_count + 1 WHERE id = $1`, inviteID,
	); err != nil {
		return "", "", err
	}
	if err := bumpMembersVersion(ctx, tx, itineraryID); err != nil {
		return "", "", err
	}
	if err := recordTripEvent(ctx, tx, itineraryID, userID, "members", "invite_redeemed",
		"member", userID, "Joined the trip as "+role, nil, map[string]any{"role": role}, ""); err != nil {
		return "", "", err
	}
	return itineraryID, role, nil
}

// RevokeInvite invalidates an invite immediately.
func RevokeInvite(ctx context.Context, q dbQuerier, inviteID, actorID string) error {
	var itineraryID string
	err := q.QueryRow(ctx,
		`UPDATE itinerary_invites
		 SET revoked_at = now()
		 WHERE id = $1 AND revoked_at IS NULL
		 RETURNING itinerary_id`,
		inviteID,
	).Scan(&itineraryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errInviteInvalid
	}
	if err != nil {
		return err
	}
	if err := bumpMembersVersion(ctx, q, itineraryID); err != nil {
		return err
	}
	return recordTripEvent(ctx, q, itineraryID, actorID, "members", "invite_revoked",
		"invite", inviteID, "Revoked an invite", nil, nil, "")
}

// ---------------------------------------------------------------------------
// Members
// ---------------------------------------------------------------------------

type itineraryMember struct {
	UserID   string    `json:"userId"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joinedAt"`
}

func ensureMemberParticipant(ctx context.Context, q dbQuerier, itineraryID, userID string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO trip_participants (itinerary_id, user_id, display_name, kind)
		 SELECT $1, $2, COALESCE(u.name, 'Member'), 'member'
		 FROM users u WHERE u.id = $2
		 ON CONFLICT (itinerary_id, user_id) DO UPDATE
		 SET removed_at = NULL,
		     display_name = COALESCE((SELECT name FROM users WHERE id = $2), trip_participants.display_name)`,
		itineraryID, userID)
	return err
}

func AddMember(ctx context.Context, q dbQuerier, itineraryID, userID, role, invitedBy string) error {
	var invitedByArg any
	if invitedBy != "" {
		invitedByArg = invitedBy
	}
	_, err := q.Exec(ctx,
		`INSERT INTO itinerary_members (itinerary_id, user_id, role, invited_by)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (itinerary_id, user_id) DO NOTHING`,
		itineraryID, userID, role, invitedByArg,
	)
	if err != nil {
		return err
	}
	return ensureMemberParticipant(ctx, q, itineraryID, userID)
}

func ListMembers(ctx context.Context, q dbQuerier, itineraryID string) ([]itineraryMember, error) {
	rows, err := q.Query(ctx,
		`SELECT m.user_id, COALESCE(u.name, 'Former member'), m.role, m.joined_at
		 FROM itinerary_members m
		 LEFT JOIN users u ON u.id = m.user_id
		 WHERE m.itinerary_id = $1
		 ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'editor' THEN 1 ELSE 2 END, m.joined_at`,
		itineraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := make([]itineraryMember, 0)
	for rows.Next() {
		var m itineraryMember
		if err := rows.Scan(&m.UserID, &m.Name, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func SetMemberRole(ctx context.Context, q dbQuerier, itineraryID, userID, role, actorID string) error {
	if role != roleEditor && role != roleViewer {
		return fmt.Errorf("invalid member role %q", role)
	}
	var oldRole string
	err := q.QueryRow(ctx,
		`SELECT role FROM itinerary_members
		 WHERE itinerary_id = $1 AND user_id = $2 AND role <> 'owner'
		 FOR UPDATE`,
		itineraryID, userID,
	).Scan(&oldRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return errItineraryForbidden
	}
	if err != nil {
		return err
	}
	if role == roleEditor && oldRole != roleEditor {
		_, editors, err := countMembers(ctx, q, itineraryID)
		if err != nil {
			return err
		}
		if editors >= maxItineraryEditors {
			return errEditorLimit
		}
	}
	if _, err := q.Exec(ctx,
		`UPDATE itinerary_members SET role = $3 WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, userID, role,
	); err != nil {
		return err
	}
	if err := bumpMembersVersion(ctx, q, itineraryID); err != nil {
		return err
	}
	return recordTripEvent(ctx, q, itineraryID, actorID, "members", "role_changed",
		"member", userID, "Changed role to "+role, map[string]any{"role": oldRole}, map[string]any{"role": role}, "")
}

func RemoveMember(ctx context.Context, q dbQuerier, itineraryID, userID, actorID string) error {
	var removedRole string
	err := q.QueryRow(ctx,
		`DELETE FROM itinerary_members
		 WHERE itinerary_id = $1 AND user_id = $2 AND role <> 'owner'
		 RETURNING role`,
		itineraryID, userID,
	).Scan(&removedRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return errItineraryForbidden
	}
	if err != nil {
		return err
	}
	if err := bumpMembersVersion(ctx, q, itineraryID); err != nil {
		return err
	}
	_, _ = q.Exec(ctx,
		`UPDATE trip_participants SET removed_at = now()
		 WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, userID)
	return recordTripEvent(ctx, q, itineraryID, actorID, "members", "member_removed",
		"member", userID, "Removed a member", map[string]any{"role": removedRole}, nil, "")
}
