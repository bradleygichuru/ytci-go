package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestSetupTestDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := db.SetupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	var exists bool
	err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_tables WHERE schemaname = 'public')").Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists)

	var tableCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'").Scan(&tableCount)
	require.NoError(t, err)
	t.Logf("tables created: %d", tableCount)
	assert.Greater(t, tableCount, 5)

	var postgisOk bool
	err = pool.QueryRow(ctx, "SELECT count(*) > 0 FROM pg_extension WHERE extname = 'postgis'").Scan(&postgisOk)
	require.NoError(t, err)
	assert.True(t, postgisOk)
}
