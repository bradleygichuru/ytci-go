package expo

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

)

type FeedItem struct {
	TrendingDestinations []json.RawMessage `json:"trendingDestinations"`
	UpcomingEvents       []json.RawMessage `json:"upcomingEvents"`
	FeaturedStories      []json.RawMessage `json:"featuredStories"`
	ActiveCampaigns      []json.RawMessage `json:"activeCampaigns"`
	HighlightedCourses   []json.RawMessage `json:"highlightedCourses"`
	ActiveChallenges     []json.RawMessage `json:"activeChallenges"`
}

type FeedHandler struct {
	pool *pgxpool.Pool
}

func NewFeedHandler(pool *pgxpool.Pool) *FeedHandler {
	return &FeedHandler{pool: pool}
}

func (h *FeedHandler) GetFeed(w http.ResponseWriter, r *http.Request) {
	var feed FeedItem

	rows, err := h.pool.Query(r.Context(),
		`SELECT json_agg(sub) FROM (
			SELECT id, name, slug, short_description, county, updated_at
			FROM destinations WHERE status = 'published'
			ORDER BY updated_at DESC LIMIT 5
		) sub`)
	if err == nil {
		if rows.Next() {
			rows.Scan(&feed.TrendingDestinations)
		}
		rows.Close()
	} else {
		slog.Warn("feed: trending destinations", "error", err)
		feed.TrendingDestinations = []json.RawMessage{}
	}

	rows, err = h.pool.Query(r.Context(),
		`SELECT json_agg(sub) FROM (
			SELECT id, title, county, venue, event_date, type
			FROM events WHERE status = 'scheduled'
			ORDER BY event_date ASC LIMIT 5
		) sub`)
	if err == nil {
		if rows.Next() {
			rows.Scan(&feed.UpcomingEvents)
		}
		rows.Close()
	} else {
		slog.Warn("feed: upcoming events", "error", err)
		feed.UpcomingEvents = []json.RawMessage{}
	}

	rows, err = h.pool.Query(r.Context(),
		`SELECT json_agg(sub) FROM (
			SELECT id, caption, like_count, created_at
			FROM stories WHERE status = 'approved'
			ORDER BY created_at DESC LIMIT 5
		) sub`)
	if err == nil {
		if rows.Next() {
			rows.Scan(&feed.FeaturedStories)
		}
		rows.Close()
	} else {
		slog.Warn("feed: featured stories", "error", err)
		feed.FeaturedStories = []json.RawMessage{}
	}

	rows, err = h.pool.Query(r.Context(),
		`SELECT json_agg(sub) FROM (
			SELECT id, title, banner_url, type, start_date
			FROM campaigns WHERE status = 'active'
			ORDER BY start_date ASC LIMIT 5
		) sub`)
	if err == nil {
		if rows.Next() {
			rows.Scan(&feed.ActiveCampaigns)
		}
		rows.Close()
	} else {
		slog.Warn("feed: active campaigns", "error", err)
		feed.ActiveCampaigns = []json.RawMessage{}
	}

	rows, err = h.pool.Query(r.Context(),
		`SELECT json_agg(sub) FROM (
			SELECT id, title, difficulty
			FROM courses WHERE status = 'published'
			ORDER BY updated_at DESC LIMIT 5
		) sub`)
	if err == nil {
		if rows.Next() {
			rows.Scan(&feed.HighlightedCourses)
		}
		rows.Close()
	} else {
		slog.Warn("feed: highlighted courses", "error", err)
		feed.HighlightedCourses = []json.RawMessage{}
	}

	rows, err = h.pool.Query(r.Context(),
		`SELECT json_agg(sub) FROM (
			SELECT id, title, badge_name, end_date
			FROM challenges WHERE status = 'active'
			ORDER BY end_date ASC LIMIT 3
		) sub`)
	if err == nil {
		if rows.Next() {
			rows.Scan(&feed.ActiveChallenges)
		}
		rows.Close()
	} else {
		slog.Warn("feed: active challenges", "error", err)
		feed.ActiveChallenges = []json.RawMessage{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(feed)
}
