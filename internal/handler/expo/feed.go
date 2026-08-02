package expo

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type FeedItem struct {
	TrendingDestinations  []json.RawMessage `json:"trendingDestinations"`
	UpcomingEvents        []json.RawMessage `json:"upcomingEvents"`
	FeaturedStories       []json.RawMessage `json:"featuredStories"`
	ActiveCampaigns       []json.RawMessage `json:"activeCampaigns"`
	HighlightedCourses    []json.RawMessage `json:"highlightedCourses"`
	ActiveChallenges      []json.RawMessage `json:"activeChallenges"`
	ConservationActivities []json.RawMessage `json:"conservationActivities"`
}

type FeedHandler struct {
	pool *pgxpool.Pool
}

func NewFeedHandler(pool *pgxpool.Pool) *FeedHandler {
	return &FeedHandler{pool: pool}
}

func (h *FeedHandler) GetFeed(w http.ResponseWriter, r *http.Request) {
	var feed FeedItem
	var wg sync.WaitGroup
	ctx := r.Context()

	wg.Add(7)
	go func() {
		defer wg.Done()
		rows, err := h.pool.Query(ctx,
			`SELECT COALESCE(json_agg(sub), '[]'::json) FROM (
				SELECT d.id, d.name, d.slug, d.short_description, d.county, d.updated_at,
					COALESCE(med.media, '[]'::json) AS media
				FROM destinations d
				LEFT JOIN LATERAL (
					SELECT COALESCE(json_agg(json_build_object(
						'objectKey', ma.object_key,
						'thumbnailKey', ma.thumbnail_key,
						'type', ma.type,
						'altText', ma.alt_text
					) ORDER BY ma.display_order) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
					FROM media_assets ma
					WHERE ma.entity_type = 'destination' AND ma.entity_id = d.id::text
				) med ON true
				WHERE d.status = 'published'
				ORDER BY d.updated_at DESC LIMIT 5
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
		if feed.TrendingDestinations == nil {
			feed.TrendingDestinations = []json.RawMessage{}
		}
	}()

	go func() {
		defer wg.Done()
		rows, err := h.pool.Query(ctx,
			`SELECT COALESCE(json_agg(sub), '[]'::json) FROM (
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
		if feed.UpcomingEvents == nil {
			feed.UpcomingEvents = []json.RawMessage{}
		}
	}()

	go func() {
		defer wg.Done()
		rows, err := h.pool.Query(ctx,
			`SELECT COALESCE(json_agg(sub), '[]'::json) FROM (
				SELECT s.id, s.caption, s.like_count, s.created_at,
					(SELECT COUNT(*) FROM story_comments sc WHERE sc.story_id = s.id AND sc.parent_id IS NULL AND sc.status != 'deleted') AS comment_count,
					COALESCE(med.media, '[]'::json) AS media
				FROM stories s
				LEFT JOIN LATERAL (
					SELECT COALESCE(json_agg(json_build_object(
						'objectKey', ma.object_key
					)) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
					FROM media_assets ma
					WHERE ma.entity_type = 'story' AND ma.entity_id = s.id::text
				) med ON true
				WHERE s.status != 'rejected'
				ORDER BY s.created_at DESC LIMIT 5
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
		if feed.FeaturedStories == nil {
			feed.FeaturedStories = []json.RawMessage{}
		}
	}()

	go func() {
		defer wg.Done()
		rows, err := h.pool.Query(ctx,
			`SELECT COALESCE(json_agg(sub), '[]'::json) FROM (
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
		if feed.ActiveCampaigns == nil {
			feed.ActiveCampaigns = []json.RawMessage{}
		}
	}()

	go func() {
		defer wg.Done()
		rows, err := h.pool.Query(ctx,
			`SELECT COALESCE(json_agg(sub), '[]'::json) FROM (
				SELECT id, title, description, difficulty, image_url, created_at
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
		if feed.HighlightedCourses == nil {
			feed.HighlightedCourses = []json.RawMessage{}
		}
	}()

	go func() {
		defer wg.Done()
		rows, err := h.pool.Query(ctx,
			`SELECT COALESCE(json_agg(sub), '[]'::json) FROM (
				SELECT id, title, description, badge_name, badge_icon_url,
					status, start_date, end_date, created_at
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
		if feed.ActiveChallenges == nil {
			feed.ActiveChallenges = []json.RawMessage{}
		}
	}()

	go func() {
		defer wg.Done()
		rows, err := h.pool.Query(ctx,
			`SELECT COALESCE(json_agg(sub), '[]'::json) FROM (
				SELECT id, title, organizer, event_date, impact_metric,
					current_participants, location_label
				FROM conservation_activities
				WHERE status = 'open' AND privacy_level = 'public'
				ORDER BY created_at DESC LIMIT 5
			) sub`)
		if err == nil {
			if rows.Next() {
				rows.Scan(&feed.ConservationActivities)
			}
			rows.Close()
		} else {
			slog.Warn("feed: conservation activities", "error", err)
			feed.ConservationActivities = []json.RawMessage{}
		}
		if feed.ConservationActivities == nil {
			feed.ConservationActivities = []json.RawMessage{}
		}
	}()

	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(feed)
}
