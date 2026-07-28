package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type StoriesHandler struct {
	pool *pgxpool.Pool
}

func NewStoriesHandler(pool *pgxpool.Pool) *StoriesHandler {
	return &StoriesHandler{pool: pool}
}

type enrichedStory struct {
	ID        string  `json:"id"`
	Caption   *string `json:"caption,omitempty"`
	LikeCount int     `json:"likeCount"`
	SaveCount int     `json:"saveCount"`
	CreatedAt string  `json:"createdAt"`
	IsLiked   bool    `json:"isLiked"`
	IsSaved   bool    `json:"isSaved"`
}

func (h *StoriesHandler) ListEnriched(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	hasAuth := userID != ""

	var enrichJoin string
	var query string

	if hasAuth {
		enrichJoin = `LEFT JOIN LATERAL (
			SELECT EXISTS(SELECT 1 FROM story_interactions si2
			 WHERE si2.story_id = s.id AND si2.user_id = $1 AND si2.interaction_type = 'like') AS liked,
			EXISTS(SELECT 1 FROM story_interactions si3
			 WHERE si3.story_id = s.id AND si3.user_id = $1 AND si3.interaction_type = 'save') AS saved
		) en ON true`
		query = `SELECT s.id, s.caption, s.like_count, s.save_count, s.created_at, en.liked, en.saved
			 FROM stories s ` + enrichJoin + ` WHERE s.status = 'approved' ORDER BY s.created_at DESC LIMIT 50`
	} else {
		query = `SELECT s.id, s.caption, s.like_count, s.save_count, s.created_at
			 FROM stories s WHERE s.status = 'approved' ORDER BY s.created_at DESC LIMIT 50`
	}

	var rows pgx.Rows
	var err error
	if hasAuth {
		rows, err = h.pool.Query(r.Context(), query, userID)
	} else {
		rows, err = h.pool.Query(r.Context(), query)
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list stories")
		return
	}
	defer rows.Close()

	var items []enrichedStory
	for rows.Next() {
		var i enrichedStory
		if hasAuth {
			rows.Scan(&i.ID, &i.Caption, &i.LikeCount, &i.SaveCount, &i.CreatedAt, &i.IsLiked, &i.IsSaved)
		} else {
			rows.Scan(&i.ID, &i.Caption, &i.LikeCount, &i.SaveCount, &i.CreatedAt)
		}
		items = append(items, i)
	}
	if items == nil {
		items = []enrichedStory{}
	}

	resp := model.Paginated[enrichedStory]{Items: items, HasMore: false}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
