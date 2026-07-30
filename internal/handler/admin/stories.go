package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

type StoriesHandler struct {
	pool *pgxpool.Pool
	r2   r2.Store
}

func NewStoriesHandler(pool *pgxpool.Pool, r2client r2.Store) *StoriesHandler {
	return &StoriesHandler{pool: pool, r2: r2client}
}

type listStory struct {
	ID            string  `json:"id"`
	CreatorID     *string `json:"creatorId"`
	DestinationID *string `json:"destinationId"`
	Caption       *string `json:"caption"`
	Journal       *string `json:"journal"`
	Tags          *string `json:"tags"`
	Status        string  `json:"status"`
	LikeCount     *int32  `json:"likeCount"`
	SaveCount     *int32  `json:"saveCount"`
	ViewCount     *int32  `json:"viewCount"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
}

const listStoriesQuery = `SELECT id, creator_id, destination_id, caption, journal, tags, status,
	like_count, save_count, view_count, created_at, updated_at
	FROM stories
	WHERE status = 'approved'
	ORDER BY created_at DESC, id DESC
	LIMIT $1`

const listStoriesAfterQuery = `SELECT id, creator_id, destination_id, caption, journal, tags, status,
	like_count, save_count, view_count, created_at, updated_at
	FROM stories
	WHERE status = 'approved'
	  AND (created_at < $1 OR (created_at = $1 AND id < $2))
	ORDER BY created_at DESC, id DESC
	LIMIT $3`

func scanListStory(row interface{ Scan(dest ...any) error }) (listStory, error) {
	var s listStory
	var createdAt, updatedAt time.Time
	err := row.Scan(&s.ID, &s.CreatorID, &s.DestinationID, &s.Caption, &s.Journal,
		&s.Tags, &s.Status, &s.LikeCount, &s.SaveCount, &s.ViewCount,
		&createdAt, &updatedAt)
	if err != nil {
		return s, err
	}
	s.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	s.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return s, nil
}

func (h *StoriesHandler) List(w http.ResponseWriter, r *http.Request) {
	pg := pagination.NewCursorPaginator[listStory]()

	pg.WritePage(w, r,
		func(limit int32) ([]listStory, error) {
			rows, err := h.pool.Query(r.Context(), listStoriesQuery, limit)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var items []listStory
			for rows.Next() {
				s, err := scanListStory(rows)
				if err != nil {
					slog.Warn("scan story list", "error", err)
					continue
				}
				items = append(items, s)
			}
			if items == nil {
				items = []listStory{}
			}
			return items, rows.Err()
		},
		func(limit int32, sortValue, id string) ([]listStory, error) {
			rows, err := h.pool.Query(r.Context(), listStoriesAfterQuery, sortValue, id, limit)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var items []listStory
			for rows.Next() {
				s, err := scanListStory(rows)
				if err != nil {
					slog.Warn("scan story list", "error", err)
					continue
				}
				items = append(items, s)
			}
			if items == nil {
				items = []listStory{}
			}
			return items, rows.Err()
		},
		func(s listStory) (string, bool) {
			return pagination.EncodeCursor(s.CreatedAt, s.ID), true
		},
	)
}

func (h *StoriesHandler) Moderate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	storyID := r.PathValue("id")
	if storyID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "story id is required")
		return
	}

	status := "rejected"
	if req.Action == "approve" {
		status = "approved"
	}

	moderatorID := middleware.UserID(r.Context())

	result, err := h.pool.Exec(r.Context(),
		`UPDATE stories SET status = $2, moderated_by = $3, moderation_note = $4, moderated_at = now(), updated_at = now() WHERE id = $1`,
		storyID, status, moderatorID, req.Reason)
	if err != nil {
		slog.Error("moderate story", "error", err, "story_id", storyID)
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to moderate story")
		return
	}
	if result.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "story not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func (h *StoriesHandler) ModerationList(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")

	query := `SELECT s.id, s.creator_id, u.name AS creator_handle, COALESCE(s.caption, '') AS caption, COALESCE(s.journal, '') AS journal,
		COALESCE(ma.media_type, '') AS media_type,
		COALESCE(ma.thumbnail_key, '') AS thumbnail_key,
		COALESCE(ma.object_key, '') AS object_key,
		COALESCE(d.name, '') AS location,
		COALESCE(s.tags, '[]') AS tags,
		s.status, COALESCE(s.like_count, 0), COALESCE(s.save_count, 0), s.created_at
		FROM stories s
		JOIN users u ON u.id = s.creator_id
		LEFT JOIN destinations d ON d.id = s.destination_id
		LEFT JOIN LATERAL (
			SELECT ma.type AS media_type, ma.thumbnail_key, ma.object_key
			FROM media_assets ma
			WHERE ma.entity_type = 'story' AND ma.entity_id = s.id::text
			ORDER BY ma.display_order ASC, ma.created_at ASC
			LIMIT 1
		) ma ON true`
	args := []any{}

	if statusFilter != "" {
		query += ` WHERE s.status = $1`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY s.created_at DESC LIMIT 50`

	rows, err := h.pool.Query(r.Context(), query, args...)
	if err != nil {
		slog.Error("list moderation stories", "error", err)
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list stories")
		return
	}
	defer rows.Close()

	type item struct {
		ID            string   `json:"id"`
		CreatorID     string   `json:"creatorId"`
		CreatorHandle string   `json:"creatorHandle"`
		Caption       string   `json:"caption"`
		Journal       string   `json:"journal"`
		MediaType     string   `json:"mediaType"`
		ThumbUrl      string   `json:"thumbUrl"`
		Location      string   `json:"location"`
		Tags          []string `json:"tags"`
		Status        string   `json:"status"`
		LikeCount     int      `json:"likeCount"`
		SaveCount     int      `json:"saveCount"`
		SubmittedAt   string   `json:"submittedAt"`
	}
	var items []item
	for rows.Next() {
		var i struct {
			ID            string
			CreatorID     string
			CreatorHandle string
			Caption       string
			Journal       string
			MediaType     string
			ThumbnailKey  string
			ObjectKey     string
			Location      string
			TagsJSON      string
			Status        string
			LikeCount     int
			SaveCount     int
			CreatedAt     time.Time
		}
		err := rows.Scan(&i.ID, &i.CreatorID, &i.CreatorHandle, &i.Caption, &i.Journal,
			&i.MediaType, &i.ThumbnailKey, &i.ObjectKey, &i.Location,
			&i.TagsJSON, &i.Status, &i.LikeCount, &i.SaveCount, &i.CreatedAt)
		if err != nil {
			slog.Warn("scan moderation row", "error", err)
			continue
		}

		out := item{
			ID:            i.ID,
			CreatorID:     i.CreatorID,
			CreatorHandle: i.CreatorHandle,
			Caption:       i.Caption,
			Journal:       i.Journal,
			MediaType:     i.MediaType,
			Location:      i.Location,
			Status:        i.Status,
			LikeCount:     i.LikeCount,
			SaveCount:     i.SaveCount,
			SubmittedAt:   i.CreatedAt.Format(time.RFC3339),
		}

		thumbKey := i.ThumbnailKey
		if thumbKey == "" {
			thumbKey = i.ObjectKey
		}
		if thumbKey != "" && h.r2 != nil {
			url, err := h.r2.PresignedGetURL(r.Context(), thumbKey, 15*time.Minute)
			if err != nil {
				slog.Warn("presign thumb", "key", thumbKey, "error", err)
			} else {
				out.ThumbUrl = url
			}
		}

		var tags []string
		json.Unmarshal([]byte(i.TagsJSON), &tags)
		out.Tags = tags

		items = append(items, out)
	}
	if err := rows.Err(); err != nil {
		slog.Error("rows iteration", "error", err)
	}
	if items == nil {
		items = []item{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *StoriesHandler) Report(w http.ResponseWriter, r *http.Request) {
	storyID := r.PathValue("id")
	var req struct {
		Reason  string `json:"reason"`
		Details string `json:"details,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO story_reports (story_id, reported_by, reason, details) VALUES ($1, $2, $3, $4)`,
		storyID, middleware.UserID(r.Context()), req.Reason, req.Details)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to report story")
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "reported"})
}
