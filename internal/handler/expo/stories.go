package expo

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

type previewComment struct {
	ID         string `json:"id"`
	AuthorName string `json:"authorName"`
	Body       string `json:"body"`
}

type enrichedStory struct {
	ID            string           `json:"id"`
	Caption       *string          `json:"caption,omitempty"`
	Media         []mediaItem      `json:"media"`
	LikeCount     int              `json:"likeCount"`
	SaveCount     int              `json:"saveCount"`
	CommentCount  int              `json:"commentCount"`
	PreviewComments []previewComment `json:"previewComments"`
	CreatedAt     string           `json:"createdAt"`
	AuthorName    string           `json:"authorName"`
	AuthorID      string           `json:"authorId"`
	IsLiked       bool             `json:"isLiked"`
	IsSaved       bool             `json:"isSaved"`
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
		)) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
		FROM media_assets ma
		WHERE ma.entity_type = 'story' AND ma.entity_id = s.id::text
	) med ON true`

	commentsJoin := `LEFT JOIN LATERAL (
		SELECT
			(SELECT COUNT(*) FROM story_comments sc WHERE sc.story_id = s.id AND sc.parent_id IS NULL AND sc.status != 'deleted') AS comment_count,
			COALESCE(json_agg(json_build_object(
				'id', sc.id,
				'authorName', COALESCE(up.display_name, 'Anonymous'),
				'body', sc.body
			) ORDER BY sc.created_at DESC) FILTER (WHERE sc.id IS NOT NULL), '[]') AS preview_comments
		FROM (
			SELECT * FROM story_comments
			WHERE story_id = s.id AND parent_id IS NULL AND status != 'deleted'
			ORDER BY created_at DESC LIMIT 2
		) sc
		LEFT JOIN user_profiles up ON up.user_id = sc.author_id
	) comments ON true`

	if hasAuth {
		enrichJoin = `LEFT JOIN LATERAL (
			SELECT EXISTS(SELECT 1 FROM story_interactions si2
			 WHERE si2.story_id = s.id AND si2.user_id = $1 AND si2.interaction_type = 'like') AS liked,
			EXISTS(SELECT 1 FROM story_interactions si3
			 WHERE si3.story_id = s.id AND si3.user_id = $1 AND si3.interaction_type = 'save') AS saved
		) en ON true
		LEFT JOIN user_profiles up ON up.user_id = s.creator_id`
		query = `SELECT s.id, s.caption, med.media, COALESCE(s.like_count, 0), COALESCE(s.save_count, 0), comments.comment_count, comments.preview_comments, s.created_at,
				COALESCE(up.display_name, 'Anonymous') AS author_name, s.creator_id, en.liked, en.saved
			 FROM stories s ` + mediaJoin + ` ` + commentsJoin + ` ` + enrichJoin + ` WHERE s.status != 'rejected' ORDER BY s.created_at DESC LIMIT 50`
	} else {
		query = `SELECT s.id, s.caption, med.media, COALESCE(s.like_count, 0), COALESCE(s.save_count, 0), comments.comment_count, comments.preview_comments, s.created_at,
				COALESCE(up.display_name, 'Anonymous') AS author_name, s.creator_id, false, false
			 FROM stories s ` + mediaJoin + ` ` + commentsJoin + ` LEFT JOIN user_profiles up ON up.user_id = s.creator_id WHERE s.status != 'rejected' ORDER BY s.created_at DESC LIMIT 50`
	}

	var rows pgx.Rows
	var err error
	if hasAuth {
		rows, err = h.pool.Query(r.Context(), query, userID)
	} else {
		rows, err = h.pool.Query(r.Context(), query)
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list stories", err)
		return
	}
	defer rows.Close()

	var items []enrichedStory
	for rows.Next() {
		var i enrichedStory
		var mediaJSON, previewJSON []byte
		var createdAt pgtype.Timestamp
		if hasAuth {
			rows.Scan(&i.ID, &i.Caption, &mediaJSON, &i.LikeCount, &i.SaveCount, &i.CommentCount, &previewJSON, &createdAt, &i.AuthorName, &i.AuthorID, &i.IsLiked, &i.IsSaved)
		} else {
			rows.Scan(&i.ID, &i.Caption, &mediaJSON, &i.LikeCount, &i.SaveCount, &i.CommentCount, &previewJSON, &createdAt, &i.AuthorName, &i.AuthorID, &i.IsLiked, &i.IsSaved)
		}
		i.CreatedAt = createdAt.Time.Format(time.RFC3339)
		if mediaJSON != nil {
			if err := json.Unmarshal(mediaJSON, &i.Media); err != nil {
				slog.Warn("failed to unmarshal story media", "story_id", i.ID, "error", err)
			}
			slog.Debug("ListEnriched media", "story_id", i.ID, "media_json", string(mediaJSON), "parsed_count", len(i.Media))
		} else {
			slog.Debug("ListEnriched media", "story_id", i.ID, "media_json", "nil")
		}
		if i.Media == nil {
			i.Media = []mediaItem{}
		}
		if previewJSON != nil {
			if err := json.Unmarshal(previewJSON, &i.PreviewComments); err != nil {
				slog.Warn("failed to unmarshal preview comments", "story_id", i.ID, "error", err)
			}
		}
		if i.PreviewComments == nil {
			i.PreviewComments = []previewComment{}
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list stories", err)
		return
	}
	if items == nil {
		items = []enrichedStory{}
	}

	resp := model.Paginated[enrichedStory]{Items: items, HasMore: false}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type storyDetail struct {
	ID           string      `json:"id"`
	Caption      *string     `json:"caption,omitempty"`
	Journal      *string     `json:"journal,omitempty"`
	Tags         *string     `json:"tags,omitempty"`
	Media        []mediaItem `json:"media"`
	LikeCount    int         `json:"likeCount"`
	SaveCount    int         `json:"saveCount"`
	CommentCount int         `json:"commentCount"`
	ViewCount    int         `json:"viewCount"`
	Status       string      `json:"status"`
	CreatedAt    string      `json:"createdAt"`
	AuthorName   string      `json:"authorName"`
	AuthorID     string      `json:"authorId"`
	IsLiked      bool        `json:"isLiked"`
	IsSaved      bool        `json:"isSaved"`
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
		)) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
		FROM media_assets ma
		WHERE ma.entity_type = 'story' AND ma.entity_id = s.id::text
	) med ON true`

	var s storyDetail
	var mediaJSON []byte
	var createdAt pgtype.Timestamp
	var err error
	commentCountSub := `(SELECT COUNT(*) FROM story_comments sc WHERE sc.story_id = s.id AND sc.parent_id IS NULL AND sc.status != 'deleted')`

	if hasAuth {
		err = h.pool.QueryRow(r.Context(),
			`SELECT s.id, s.caption, s.journal, s.tags, med.media, COALESCE(s.like_count, 0), COALESCE(s.save_count, 0), `+commentCountSub+`, COALESCE(s.view_count, 0), s.status, s.created_at,
				COALESCE(up.display_name, 'Anonymous') AS author_name, s.creator_id,
				EXISTS(SELECT 1 FROM story_interactions si WHERE si.story_id = s.id AND si.user_id = $1 AND si.interaction_type = 'like') AS is_liked,
				EXISTS(SELECT 1 FROM story_interactions si WHERE si.story_id = s.id AND si.user_id = $1 AND si.interaction_type = 'save') AS is_saved
			 FROM stories s `+mediaJoin+`
			 LEFT JOIN user_profiles up ON up.user_id = s.creator_id
			 WHERE s.id = $2`, userID, storyID,
		).Scan(&s.ID, &s.Caption, &s.Journal, &s.Tags, &mediaJSON, &s.LikeCount, &s.SaveCount, &s.CommentCount, &s.ViewCount, &s.Status, &createdAt, &s.AuthorName, &s.AuthorID, &s.IsLiked, &s.IsSaved)
	} else {
		err = h.pool.QueryRow(r.Context(),
			`SELECT s.id, s.caption, s.journal, s.tags, med.media, COALESCE(s.like_count, 0), COALESCE(s.save_count, 0), `+commentCountSub+`, COALESCE(s.view_count, 0), s.status, s.created_at,
				COALESCE(up.display_name, 'Anonymous') AS author_name, s.creator_id, false, false
			 FROM stories s `+mediaJoin+`
			 LEFT JOIN user_profiles up ON up.user_id = s.creator_id
			 WHERE s.id = $1`, storyID,
		).Scan(&s.ID, &s.Caption, &s.Journal, &s.Tags, &mediaJSON, &s.LikeCount, &s.SaveCount, &s.CommentCount, &s.ViewCount, &s.Status, &createdAt, &s.AuthorName, &s.AuthorID, &s.IsLiked, &s.IsSaved)
	}
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "story not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to get story", err)
		return
	}
	s.CreatedAt = createdAt.Time.Format(time.RFC3339)

	if mediaJSON != nil {
		if err := json.Unmarshal(mediaJSON, &s.Media); err != nil {
			slog.Warn("failed to unmarshal story detail media", "story_id", s.ID, "error", err)
		}
		slog.Debug("StoryDetail media", "story_id", s.ID, "media_json", string(mediaJSON), "parsed_count", len(s.Media))
	} else {
		slog.Debug("StoryDetail media", "story_id", s.ID, "media_json", "nil")
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

func (h *StoriesHandler) SavedStories(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT s.id, s.caption, med.media, s.status, COALESCE(s.like_count, 0), s.created_at
		 FROM story_interactions si
		 JOIN stories s ON s.id = si.story_id
		 LEFT JOIN LATERAL (
			SELECT COALESCE(json_agg(json_build_object(
				'objectKey', ma.object_key,
				'thumbnailKey', ma.thumbnail_key,
				'type', ma.type,
				'altText', ma.alt_text
			)) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
			FROM media_assets ma
			WHERE ma.entity_type = 'story' AND ma.entity_id = s.id::text
		 ) med ON true
		 WHERE si.user_id = $1 AND si.interaction_type = 'save'
		 ORDER BY si.created_at DESC LIMIT 50`, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list saved stories", err)
		return
	}
	defer rows.Close()

	var items []myStory
	for rows.Next() {
		var i myStory
		var mediaJSON []byte
		var createdAt pgtype.Timestamp
		rows.Scan(&i.ID, &i.Caption, &mediaJSON, &i.Status, &i.LikeCount, &createdAt)
		i.CreatedAt = createdAt.Time.Format(time.RFC3339)
		if mediaJSON != nil {
			if err := json.Unmarshal(mediaJSON, &i.Media); err != nil {
				slog.Warn("failed to unmarshal saved story media", "story_id", i.ID, "error", err)
			}
		}
		if i.Media == nil {
			i.Media = []mediaItem{}
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate stories", err)
		return
	}
	if items == nil {
		items = []myStory{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[myStory]{Items: items, HasMore: false})
}

func (h *StoriesHandler) MyStories(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT s.id, s.caption, med.media, s.status, COALESCE(s.like_count, 0), s.created_at
		 FROM stories s
		 LEFT JOIN LATERAL (
			SELECT COALESCE(json_agg(json_build_object(
				'objectKey', ma.object_key,
				'thumbnailKey', ma.thumbnail_key,
				'type', ma.type,
				'altText', ma.alt_text
			)) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
			FROM media_assets ma
			WHERE ma.entity_type = 'story' AND ma.entity_id = s.id::text
		 ) med ON true
		 WHERE s.creator_id = $1 ORDER BY s.created_at DESC LIMIT 50`, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list stories", err)
		return
	}
	defer rows.Close()

	var items []myStory
	for rows.Next() {
		var i myStory
		var mediaJSON []byte
		var createdAt pgtype.Timestamp
		rows.Scan(&i.ID, &i.Caption, &mediaJSON, &i.Status, &i.LikeCount, &createdAt)
		i.CreatedAt = createdAt.Time.Format(time.RFC3339)
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
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate stories", err)
		return
	}
	if items == nil {
		items = []myStory{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[myStory]{Items: items, HasMore: false})
}
