package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

type FeedItem struct {
	TrendingDestinations []json.RawMessage `json:"trendingDestinations,omitempty"`
	UpcomingEvents       []json.RawMessage `json:"upcomingEvents,omitempty"`
	FeaturedStories      []json.RawMessage `json:"featuredStories,omitempty"`
	ActiveCampaigns      []json.RawMessage `json:"activeCampaigns,omitempty"`
	HighlightedCourses   []json.RawMessage `json:"highlightedCourses,omitempty"`
	ActiveChallenges     []json.RawMessage `json:"activeChallenges,omitempty"`
}

type FeedHandler struct {
	pool *pgxpool.Pool
}

func NewFeedHandler(pool *pgxpool.Pool) *FeedHandler {
	return &FeedHandler{pool: pool}
}

func (h *FeedHandler) GetFeed(w http.ResponseWriter, r *http.Request) {
	resp := FeedItem{
		TrendingDestinations: []json.RawMessage{},
		UpcomingEvents:       []json.RawMessage{},
		FeaturedStories:      []json.RawMessage{},
		ActiveCampaigns:      []json.RawMessage{},
		HighlightedCourses:   []json.RawMessage{},
		ActiveChallenges:     []json.RawMessage{},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
