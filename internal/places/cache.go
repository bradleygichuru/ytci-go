package places

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
)

type Cache struct {
	pool *pgxpool.Pool
}

func NewCache(pool *pgxpool.Pool) *Cache {
	return &Cache{pool: pool}
}

func (c *Cache) queries() *gen.Queries {
	return gen.New(c.pool)
}

func (c *Cache) GetPlace(ctx context.Context, placeID string) (*gen.GooglePlacesCache, bool, error) {
	row, err := c.queries().GetGooglePlacesCache(ctx, placeID)
	if err != nil {
		return nil, false, nil
	}
	return &row, true, nil
}

func (c *Cache) GetPlaceFresh(ctx context.Context, placeID string) (*gen.GooglePlacesCache, bool, error) {
	row, err := c.queries().GetGooglePlacesCacheFresh(ctx, placeID)
	if err != nil {
		return nil, false, nil
	}
	return &row, true, nil
}

func (c *Cache) SetPlace(ctx context.Context, details *PlaceDetails) error {
	placeID := details.PlaceID

	jsonData, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("places cache: marshal place details: %w", err)
	}

	params := &gen.UpsertGooglePlacesCacheParams{
		PlaceID:          placeID,
		Name:             &details.DisplayName,
		FormattedAddress: &details.FormattedAddress,
		Types:            details.Types,
		Data:             jsonData,
	}
	if details.Location != nil {
		params.Lat = &details.Location.Latitude
		params.Lng = &details.Location.Longitude
	}

	return c.queries().UpsertGooglePlacesCache(ctx, params)
}

func hashQuery(key string) string {
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", h[:16])
}

func (c *Cache) GetAutocompleteResults(ctx context.Context, query string) ([]AutocompleteSuggestion, bool, error) {
	var results []AutocompleteSuggestion
	found, err := c.getSearchResults(ctx, "autocomplete:"+query, &results)
	if err != nil {
		return nil, false, err
	}
	if found {
		return results, true, nil
	}
	return nil, false, nil
}

func (c *Cache) SetAutocompleteResults(ctx context.Context, query string, results []AutocompleteSuggestion) error {
	return c.setSearchResults(ctx, "autocomplete:"+query, results, 10*time.Minute)
}

func (c *Cache) GetTextSearchResults(ctx context.Context, query string) ([]TextSearchResult, bool, error) {
	var results []TextSearchResult
	found, err := c.getSearchResults(ctx, "text_search:"+query, &results)
	if err != nil {
		return nil, false, err
	}
	if found {
		return results, true, nil
	}
	return nil, false, nil
}

func (c *Cache) SetTextSearchResults(ctx context.Context, query string, results []TextSearchResult) error {
	return c.setSearchResults(ctx, "text_search:"+query, results, 30*time.Minute)
}

func (c *Cache) GetNearbyResults(ctx context.Context) ([]NearbyPlace, bool, error) {
	var results []NearbyPlace
	found, err := c.getSearchResults(ctx, "nearby:kenya:popular", &results)
	if err != nil {
		return nil, false, err
	}
	if found {
		return results, true, nil
	}
	return nil, false, nil
}

func (c *Cache) SetNearbyResults(ctx context.Context, results []NearbyPlace) error {
	return c.setSearchResults(ctx, "nearby:kenya:popular", results, 24*time.Hour)
}

func (c *Cache) GetNearbyResultsStale(ctx context.Context) ([]NearbyPlace, bool, error) {
	queryHash := hashQuery("nearby:kenya:popular")
	row, err := c.queries().GetGooglePlacesSearchCacheStale(ctx, queryHash)
	if err != nil {
		return nil, false, nil
	}
	var results []NearbyPlace
	if err := json.Unmarshal(row.Response, &results); err != nil {
		return nil, false, fmt.Errorf("places cache: unmarshal stale results: %w", err)
	}
	return results, true, nil
}

func (c *Cache) getSearchResults(ctx context.Context, key string, target interface{}) (bool, error) {
	queryHash := hashQuery(key)
	row, err := c.queries().GetGooglePlacesSearchCache(ctx, queryHash)
	if err != nil {
		return false, nil
	}
	if err := json.Unmarshal(row.Response, target); err != nil {
		return false, fmt.Errorf("places cache: unmarshal search results: %w", err)
	}
	return true, nil
}

func (c *Cache) setSearchResults(ctx context.Context, key string, data interface{}, ttl time.Duration) error {
	queryHash := hashQuery(key)

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("places cache: marshal search results: %w", err)
	}

	params := &gen.UpsertGooglePlacesSearchCacheParams{
		QueryHash: queryHash,
		Response:  jsonData,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
	}

	return c.queries().UpsertGooglePlacesSearchCache(ctx, params)
}

func (c *Cache) SessionTokenValid(ctx context.Context, token string) (bool, error) {
	queryHash := hashQuery("session:" + token)
	_, err := c.queries().GetGooglePlacesSearchCache(ctx, queryHash)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (c *Cache) CreateSessionToken(ctx context.Context, token string) error {
	queryHash := hashQuery("session:" + token)
	params := &gen.UpsertGooglePlacesSearchCacheParams{
		QueryHash: queryHash,
		Response:  []byte(`{"active":true}`),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(30 * time.Minute), Valid: true},
	}
	return c.queries().UpsertGooglePlacesSearchCache(ctx, params)
}
