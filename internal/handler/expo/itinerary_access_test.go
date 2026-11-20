package expo

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func newCollaborationTestUser(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (id, name, email, email_verified, created_at, updated_at, role)
		 VALUES (gen_random_uuid()::text, $1, gen_random_uuid()::text || '@example.com', true, now(), now(), 'user')
		 RETURNING id`,
		name,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func newCollaborationTestItinerary(t *testing.T, pool *pgxpool.Pool, ownerID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO itineraries (user_id, title, inputs)
		 VALUES ($1, 'Test trip', '{}'::jsonb)
		 RETURNING id`,
		ownerID,
	).Scan(&id)
	require.NoError(t, err)
	require.NoError(t, AddMember(context.Background(), pool, id, ownerID, roleOwner, ""))
	return id
}

func withCollaborationTx(t *testing.T, pool *pgxpool.Pool, fn func(pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	fn(tx)
	require.NoError(t, tx.Commit(ctx))
}

func TestInviteLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	joinerID := newCollaborationTestUser(t, pool, "Joiner")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)

	var code, inviteID string
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		var err error
		code, inviteID, err = CreateInvite(ctx, tx, itineraryID, ownerID, roleViewer, 0)
		require.NoError(t, err)
	})
	assert.Len(t, code, 8)
	assert.NotEmpty(t, inviteID)

	var role string
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		var err error
		_, role, err = RedeemInvite(ctx, tx, code, joinerID)
		require.NoError(t, err)
	})
	assert.Equal(t, roleViewer, role)

	var useCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT use_count FROM itinerary_invites WHERE id = $1`, inviteID).Scan(&useCount))
	assert.Equal(t, 1, useCount)

	var memberRole string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT role FROM itinerary_members WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, joinerID).Scan(&memberRole))
	assert.Equal(t, roleViewer, memberRole)

	// Redeeming again is idempotent and does not consume another use.
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		var err error
		_, role, err = RedeemInvite(ctx, tx, code, joinerID)
		require.NoError(t, err)
	})
	assert.Equal(t, roleViewer, role)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT use_count FROM itinerary_invites WHERE id = $1`, inviteID).Scan(&useCount))
	assert.Equal(t, 1, useCount)
}

func TestInviteExpiryRevocationAndMaxUses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	firstID := newCollaborationTestUser(t, pool, "First")
	secondID := newCollaborationTestUser(t, pool, "Second")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)

	var expiredCode string
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		var err error
		expiredCode, _, err = CreateInvite(ctx, tx, itineraryID, ownerID, roleViewer, 0)
		require.NoError(t, err)
	})
	_, err := pool.Exec(ctx,
		`UPDATE itinerary_invites SET expires_at = now() - interval '1 day' WHERE code_hash = $1`,
		hashInviteCode(expiredCode))
	require.NoError(t, err)
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		_, _, err := RedeemInvite(ctx, tx, expiredCode, firstID)
		assert.ErrorIs(t, err, errInviteInvalid)
	})

	var revokedCode, revokedInviteID string
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		var err error
		revokedCode, revokedInviteID, err = CreateInvite(ctx, tx, itineraryID, ownerID, roleViewer, 0)
		require.NoError(t, err)
		require.NoError(t, RevokeInvite(ctx, tx, revokedInviteID, ownerID))
	})
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		_, _, err := RedeemInvite(ctx, tx, revokedCode, firstID)
		assert.ErrorIs(t, err, errInviteInvalid)
	})

	var oneUseCode string
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		var err error
		oneUseCode, _, err = CreateInvite(ctx, tx, itineraryID, ownerID, roleViewer, 1)
		require.NoError(t, err)
	})
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		_, role, err := RedeemInvite(ctx, tx, oneUseCode, firstID)
		require.NoError(t, err)
		assert.Equal(t, roleViewer, role)
	})
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		_, _, err := RedeemInvite(ctx, tx, oneUseCode, secondID)
		assert.ErrorIs(t, err, errInviteInvalid)
	})
}

func TestInviteCapacityAndRoles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)

	// Owner + 9 editors reaches the 10-editor cap.
	for i := 0; i < 9; i++ {
		userID := newCollaborationTestUser(t, pool, "Editor")
		require.NoError(t, AddMember(ctx, pool, itineraryID, userID, roleEditor, ownerID))
	}
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		_, _, err := CreateInvite(ctx, tx, itineraryID, ownerID, roleEditor, 0)
		assert.ErrorIs(t, err, errEditorLimit)
	})

	// Fill the remaining participant slots with viewers.
	for i := 0; i < 10; i++ {
		userID := newCollaborationTestUser(t, pool, "Viewer")
		require.NoError(t, AddMember(ctx, pool, itineraryID, userID, roleViewer, ownerID))
	}
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		_, _, err := CreateInvite(ctx, tx, itineraryID, ownerID, roleViewer, 0)
		assert.ErrorIs(t, err, errTripFull)
	})
}

func TestMemberRoleAndRemoval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	memberID := newCollaborationTestUser(t, pool, "Member")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	require.NoError(t, AddMember(ctx, pool, itineraryID, memberID, roleViewer, ownerID))

	var membersVersion int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT members_version FROM itineraries WHERE id = $1`, itineraryID).Scan(&membersVersion))
	assert.Equal(t, 1, membersVersion)

	require.NoError(t, SetMemberRole(ctx, pool, itineraryID, memberID, roleEditor, ownerID))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT members_version FROM itineraries WHERE id = $1`, itineraryID).Scan(&membersVersion))
	assert.Equal(t, 2, membersVersion)

	members, err := ListMembers(ctx, pool, itineraryID)
	require.NoError(t, err)
	require.Len(t, members, 2)
	assert.Equal(t, roleOwner, members[0].Role)
	assert.Equal(t, roleEditor, members[1].Role)

	assert.ErrorIs(t, SetMemberRole(ctx, pool, itineraryID, ownerID, roleViewer, ownerID), errItineraryForbidden)
	assert.ErrorIs(t, RemoveMember(ctx, pool, itineraryID, ownerID, ownerID), errItineraryForbidden)

	require.NoError(t, RemoveMember(ctx, pool, itineraryID, memberID, ownerID))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT members_version FROM itineraries WHERE id = $1`, itineraryID).Scan(&membersVersion))
	assert.Equal(t, 3, membersVersion)

	members, err = ListMembers(ctx, pool, itineraryID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, roleOwner, members[0].Role)
}

func TestAccessRoleHierarchy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	editorID := newCollaborationTestUser(t, pool, "Editor")
	viewerID := newCollaborationTestUser(t, pool, "Viewer")
	outsiderID := newCollaborationTestUser(t, pool, "Outsider")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	require.NoError(t, AddMember(ctx, pool, itineraryID, editorID, roleEditor, ownerID))
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewerID, roleViewer, ownerID))

	_, err := requireItineraryRole(ctx, pool, itineraryID, viewerID, roleEditor)
	assert.ErrorIs(t, err, errItineraryForbidden)
	_, err = requireItineraryRole(ctx, pool, itineraryID, outsiderID, roleViewer)
	assert.ErrorIs(t, err, errItineraryNotFound)

	access, err := requireItineraryRole(ctx, pool, itineraryID, editorID, roleEditor)
	require.NoError(t, err)
	assert.Equal(t, roleEditor, access.Role)
	assert.Equal(t, 1, access.PlanVersion)

	// Soft-deleted trips disappear from access checks.
	_, err = pool.Exec(ctx, `UPDATE itineraries SET deleted_at = now() WHERE id = $1`, itineraryID)
	require.NoError(t, err)
	_, err = requireItineraryRole(ctx, pool, itineraryID, ownerID, roleOwner)
	assert.ErrorIs(t, err, errItineraryNotFound)
}

func TestRedeemInviteRejectsUnknownCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	joinerID := newCollaborationTestUser(t, pool, "Joiner")
	_ = newCollaborationTestItinerary(t, pool, ownerID)

	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		_, _, err := RedeemInvite(ctx, tx, "NOPE1234", joinerID)
		assert.ErrorIs(t, err, errInviteInvalid)
	})
}

func TestOwnerMembershipBackfill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Legacy Owner")
	var itineraryID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO itineraries (user_id, title, inputs)
		 VALUES ($1, 'Legacy trip', '{}'::jsonb)
		 RETURNING id`,
		ownerID,
	).Scan(&itineraryID))

	// The migration backfills existing itineraries with an owner membership.
	_, err := pool.Exec(ctx,
		`INSERT INTO itinerary_members (itinerary_id, user_id, role)
		 SELECT i.id, u.id, 'owner'
		 FROM itineraries i
		 JOIN users u ON u.id = i.user_id::text
		 WHERE i.user_id IS NOT NULL
		 ON CONFLICT DO NOTHING`)
	require.NoError(t, err)

	access, err := requireItineraryRole(ctx, pool, itineraryID, ownerID, roleOwner)
	require.NoError(t, err)
	assert.Equal(t, roleOwner, access.Role)
}
