package composer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SuggestionCache interface {
	Get(ctx context.Context, inputHash string) (SuggestionResponse, bool, error)
	Set(ctx context.Context, inputHash, catalogVersion string, response SuggestionResponse, ttl time.Duration) error
}

type UsageStore interface {
	ConsumeSuggestion(ctx context.Context, userID, day string, limit int) (used int, allowed bool, err error)
}

type DBSuggestionCache struct {
	Pool *pgxpool.Pool
}

func (c *DBSuggestionCache) Get(ctx context.Context, inputHash string) (SuggestionResponse, bool, error) {
	var raw []byte
	err := c.Pool.QueryRow(ctx, `
		SELECT response FROM composer_suggestion_cache
		WHERE input_hash = $1 AND expires_at > now()
	`, inputHash).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return SuggestionResponse{}, false, nil
		}
		return SuggestionResponse{}, false, fmt.Errorf("composer: suggestion cache read: %w", err)
	}
	var response SuggestionResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return SuggestionResponse{}, false, fmt.Errorf("composer: suggestion cache decode: %w", err)
	}
	return response, true, nil
}

func (c *DBSuggestionCache) Set(ctx context.Context, inputHash, catalogVersion string, response SuggestionResponse, ttl time.Duration) error {
	raw, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("composer: suggestion cache encode: %w", err)
	}
	_, err = c.Pool.Exec(ctx, `
		INSERT INTO composer_suggestion_cache (
			input_hash, catalog_version, engine_version, response, created_at, expires_at
		) VALUES ($1, $2, $3, $4, now(), now() + make_interval(secs => $5))
		ON CONFLICT (input_hash) DO UPDATE SET
			catalog_version = EXCLUDED.catalog_version,
			engine_version = EXCLUDED.engine_version,
			response = EXCLUDED.response,
			created_at = now(),
			expires_at = EXCLUDED.expires_at
	`, inputHash, catalogVersion, EngineVersion, raw, int(ttl.Seconds()))
	if err != nil {
		return fmt.Errorf("composer: suggestion cache write: %w", err)
	}
	return nil
}

type DBUsageStore struct {
	Pool *pgxpool.Pool
}

func (s *DBUsageStore) ConsumeSuggestion(ctx context.Context, userID, day string, limit int) (int, bool, error) {
	var used int
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO composer_suggestion_usage (user_id, usage_date, count)
		VALUES ($1, $2::date, 1)
		ON CONFLICT (user_id, usage_date) DO UPDATE
		SET count = composer_suggestion_usage.count + 1
		WHERE composer_suggestion_usage.count < $3
		RETURNING count
	`, userID, day, limit).Scan(&used)
	if err == pgx.ErrNoRows {
		return limit, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("composer: usage: %w", err)
	}
	return used, true, nil
}

func RetryAfterSeconds(now time.Time) int {
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	seconds := int(next.Sub(now).Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}
