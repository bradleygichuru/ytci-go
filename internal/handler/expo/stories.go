package expo

import (
	"encoding/json"
	"log/slog"
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

type mediaItem struct {
	ObjectKey    string `json:"objectKey"`
	ThumbnailKey string `json:"thumbnailKey,omitempty"`
	Type         string `json:"type,omitempty"`
	AltText      string `json:"altText,omitempty"`
}

type enrichedStory struct {
	ID        string      `json:"id"`
	Caption   *string     `json:"caption,omitempty"`
	Media     []mediaItem `json:"media"`
	LikeCount int         `json:"likeCount"`
	SaveCount int         `json:"saveCount"`
	CreatedAt string      `json:"createdAt"`
	IsLiked   bool        `json:"isLiked"`
	IsSaved   bool        `json:"isSaved"`
}

func (h *StoriesHandler) ListEnriched(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	hasAuth := userID != ""

	var enrichJoin string
	var query string

	mediaJoin := `LEFT JOIN LATERAL (
		SELECT COALESCE(json_agg(json_build_object(
			'objectKey', ma.object_key,
			'thumbnailKey', ma.thumbnail_key,
			'type', ma.type,
			'altText', ma.alt_text
		) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
		FROM media_assets ma
		WHERE ma.entity_type = 'story' AND ma.entity_id = s.id::text
	) med ON true`

	if hasAuth {
		enrichJoin = `LEFT JOIN LATERAL (
			SELECT EXISTS(SELECT 1 FROM story_interactions si2
			 WHERE si2.story_id = s.id AND si2.user_id = $1 AND si2.interaction_type = 'like') AS liked,
			EXISTS(SELECT 1 FROM story_interactions si3
			 WHERE si3.story_id = s.id AND si3.user_id = $1 AND si3.interaction_type = 'save') AS saved
		) en ON true`
		query = `SELECT s.id, s.caption, med.media, s.like_count, s.save_count, s.created_at, en.liked, en.saved
			 FROM stories s ` + mediaJoin + ` ` + enrichJoin + ` WHERE s.status = 'approved' ORDER BY s.created_at DESC LIMIT 50`
	} else {
		query = `SELECT s.id, s.caption, med.media, s.like_count, s.save_count, s.created_at, false, false
			 FROM stories s ` + mediaJoin + ` WHERE s.status = 'approved' ORDER BY s.created_at DESC LIMIT 50`
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
		var mediaJSON []byte
		if hasAuth {
			rows.Scan(&i.ID, &i.Caption, &mediaJSON, &i.LikeCount, &i.SaveCount, &i.CreatedAt, &i.IsLiked, &i.IsSaved)
		} else {
			rows.Scan(&i.ID, &i.Caption, &mediaJSON, &i.LikeCount, &i.SaveCount, &i.CreatedAt, &i.IsLiked, &i.IsSaved)
		}
		if mediaJSON != nil {
			if err := json.Unmarshal(mediaJSON, &i.Media); err != nil {
				slog.Warn("failed to unmarshal story media", "story_id", i.ID, "error", err)
			}
		}
		if i.Media == nil {
			i.Media = []mediaItem{}
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

type storyDetail struct {
	ID        string      `json:"id"`
	Caption   *string     `json:"caption,omitempty"`
	Journal   *string     `json:"journal,omitempty"`
	Tags      *string     `json:"tags,omitempty"`
	Media     []mediaItem `json:"media"`
	LikeCount int         `json:"likeCount"`
	SaveCount int         `json:"saveCount"`
	ViewCount int         `json:"viewCount"`
	Status    string      `json:"status"`
	CreatedAt string      `json:"createdAt"`
	IsLiked   bool        `json:"isLiked"`
	IsSaved   bool        `json:"isSaved"`
}

func (h *StoriesHandler) StoryDetail(w http.ResponseWriter, r *http.Request) {
	storyID := r.PathValue("id")
	userID := middleware.UserID(r.Context())
	hasAuth := userID != ""

	mediaJoin := `LEFT JOIN LATERAL (
		SELECT COALESCE(json_agg(json_build_object(
			'objectKey', ma.object_key,
			'thumbnailKey', ma.thumbnail_key,
			'type', ma.type,
			'altText', ma.alt_text
		) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
		FROM media_assets ma
		WHERE ma.entity_type = 'story' AND ma.entity_id = s.id::text
	) med ON true`

	var s storyDetail
	var mediaJSON []byte
	var err error
	if hasAuth {
		err = h.pool.QueryRow(r.Context(),
			`SELECT s.id, s.caption, s.journal, s.tags, med.media, s.like_count, s.save_count, s.view_count, s.status, s.created_at,
				EXISTS(SELECT 1 FROM story_interactions si WHERE si.story_id = s.id AND si.user_id = $1 AND si.interaction_type = 'like') AS is_liked,
				EXISTS(SELECT 1 FROM story_interactions si WHERE si.story_id = s.id AND si.user_id = $1 AND si.interaction_type = 'save') AS is_saved
			 FROM stories s `+mediaJoin+` WHERE s.id = $2`, userID, storyID,
		).Scan(&s.ID, &s.Caption, &s.Journal, &s.Tags, &mediaJSON, &s.LikeCount, &s.SaveCount, &s.ViewCount, &s.Status, &s.CreatedAt, &s.IsLiked, &s.IsSaved)
	} else {
		err = h.pool.QueryRow(r.Context(),
			`SELECT s.id, s.caption, s.journal, s.tags, med.media, s.like_count, s.save_count, s.view_count, s.status, s.created_at, false, false
			 FROM stories s `+mediaJoin+` WHERE s.id = $1`, storyID,
		).Scan(&s.ID, &s.Caption, &s.Journal, &s.Tags, &mediaJSON, &s.LikeCount, &s.SaveCount, &s.ViewCount, &s.Status, &s.CreatedAt, &s.IsLiked, &s.IsSaved)
	}
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "story not found")
		return
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get story")
		return
	}

	if mediaJSON != nil {
		if err := json.Unmarshal(mediaJSON, &s.Media); err != nil {
			slog.Warn("failed to unmarshal story detail media", "story_id", s.ID, "error", err)
		}
	}
	if s.Media == nil {
		s.Media = []mediaItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

type myStory struct {
	ID        string      `json:"id"`
	Caption   *string     `json:"caption,omitempty"`
	Media     []mediaItem `json:"media"`
	Status    string      `json:"status"`
	LikeCount int         `json:"likeCount"`
	CreatedAt string      `json:"createdAt"`
}

func (h *StoriesHandler) MyStories(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT s.id, s.caption, med.media, s.status, s.like_count, s.created_at
		 FROM stories s
		 LEFT JOIN LATERAL (
			SELECT COALESCE(json_agg(json_build_object(
				'objectKey', ma.object_key,
				'thumbnailKey', ma.thumbnail_key,
				'type', ma.type,
				'altText', ma.alt_text
			) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
			FROM media_assets ma
			WHERE ma.entity_type = 'story' AND ma.entity_id = s.id::text
		 ) med ON true
		 WHERE s.creator_id = $1 ORDER BY s.created_at DESC LIMIT 50`, userID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list stories")
		return
	}
	defer rows.Close()

	var items []myStory
	for rows.Next() {
		var i myStory
		var mediaJSON []byte
		rows.Scan(&i.ID, &i.Caption, &mediaJSON, &i.Status, &i.LikeCount, &i.CreatedAt)
		if mediaJSON != nil {
			if err := json.Unmarshal(mediaJSON, &i.Media); err != nil {
				slog.Warn("failed to unmarshal my story media", "story_id", i.ID, "error", err)
			}
		}
		if i.Media == nil {
			i.Media = []mediaItem{}
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to iterate stories")
		return
	}
	if items == nil {
		items = []myStory{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[myStory]{Items: items, HasMore: false})
}
