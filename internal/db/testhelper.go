package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func SetupTestDB(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()

	container, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgis/postgis:17-3.5"),
		postgres.WithDatabase("ytci_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	t.Cleanup(func() { container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	migrationPath := filepath.Join("..", "..", "testdata", "migrations")
	entries, err := os.ReadDir(migrationPath)
	if err != nil {
		t.Fatalf("failed to read migrations dir: %v", err)
	}

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		sqlBytes, err := os.ReadFile(filepath.Join(migrationPath, entry.Name()))
		if err != nil {
			t.Fatalf("failed to read migration %s: %v", entry.Name(), err)
		}
		_, err = pool.Exec(ctx, string(sqlBytes))
		if err != nil {
			t.Fatalf("failed to execute migration %s: %v", entry.Name(), err)
		}
		fmt.Printf("applied migration: %s\n", entry.Name())
	}

	return pool
}
