package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
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

func (h *StoriesHandler) List(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	pg := &pagination.CursorPaginator[gen.Story]{}

	pg.WritePage(w, r,
		func(limit int32) ([]gen.Story, error) {
			return queries.ListStories(r.Context(), limit)
		},
		func(limit int32, sortValue, id string) ([]gen.Story, error) {
			var ts pgtype.Timestamp
			var uid pgtype.UUID
			ts.Scan(sortValue)
			uid.Scan(id)
			return queries.ListStoriesAfter(r.Context(), &gen.ListStoriesAfterParams{
				CreatedAt: ts,
				ID:        uid,
				Limit:     limit,
			})
		},
		func(s gen.Story) (string, bool) {
			ts := s.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
			return pagination.EncodeCursor(ts, pagination.UUIDString(s.ID.Bytes)), true
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

	query := `SELECT s.id, s.creator_id, u.name AS creator_handle, s.caption, COALESCE(s.journal, '') AS journal,
		COALESCE(ma.media_type, '') AS media_type,
		COALESCE(ma.thumbnail_key, '') AS thumbnail_key,
		COALESCE(ma.object_key, '') AS object_key,
		COALESCE(d.name, '') AS location,
		COALESCE(s.tags, '[]') AS tags,
		s.status, s.like_count, s.save_count, s.created_at
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
