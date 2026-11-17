package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type audioProgressRequest struct {
	PositionSeconds int    `json:"positionSeconds"`
	DurationSeconds int    `json:"durationSeconds"`
	Language        string `json:"language"`
	UpdatedAt       string `json:"updatedAt"`
}

func (h *AudioStoryHandler) ListSavedStories(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT s.slug,s.title,COALESCE(m.object_key,''),sv.created_at
		 FROM audio_story_saves sv
		 JOIN audio_stories s ON s.id=sv.story_id
		 LEFT JOIN media_assets m ON m.id=s.cover_media_id
		 WHERE sv.user_id=$1
		 ORDER BY sv.created_at DESC`, userID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load saved stories")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var slug, title, coverKey string
		var created pgtype.Timestamp
		if err := rows.Scan(&slug, &title, &coverKey, &created); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read saved story")
			return
		}
		items = append(items, map[string]any{
			"slug": slug, "title": title,
			"coverUrl": h.presignAudio(r.Context(), coverKey, audioPlaybackTTL),
			"savedAt":  tsPtr(created),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AudioStoryHandler) SaveStory(w http.ResponseWriter, r *http.Request) {
	h.toggleSave(w, r, true)
}

func (h *AudioStoryHandler) UnsaveStory(w http.ResponseWriter, r *http.Request) {
	h.toggleSave(w, r, false)
}

func (h *AudioStoryHandler) toggleSave(w http.ResponseWriter, r *http.Request, save bool) {
	userID := middleware.UserID(r.Context())
	slug := chi.URLParam(r, "slug")
	var storyID string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT id FROM audio_stories WHERE slug=$1`, slug).Scan(&storyID); err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}
	if save {
		_, err := h.pool.Exec(r.Context(),
			`INSERT INTO audio_story_saves (user_id,story_id) VALUES ($1,$2)
			 ON CONFLICT (user_id,story_id) DO NOTHING`, userID, storyID)
		if err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to save story")
			return
		}
	} else {
		if _, err := h.pool.Exec(r.Context(),
			`DELETE FROM audio_story_saves WHERE user_id=$1 AND story_id=$2`, userID, storyID); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to remove saved story")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": save})
}

func (h *AudioStoryHandler) ListProgress(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT s.slug,s.title,p.position_seconds,p.duration_seconds,COALESCE(p.language,''),p.updated_at,
		        (s.status='published' AND c.status='published')
		 FROM audio_story_progress p
		 JOIN audio_stories s ON s.id=p.story_id
		 LEFT JOIN audio_story_collections c ON c.id=s.collection_id
		 WHERE p.user_id=$1
		 ORDER BY p.updated_at DESC LIMIT 50`, userID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load progress")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var slug, title, language string
		var position, duration int
		var updated pgtype.Timestamp
		var available bool
		if err := rows.Scan(&slug, &title, &position, &duration, &language, &updated, &available); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read progress")
			return
		}
		items = append(items, map[string]any{
			"slug": slug, "title": title, "positionSeconds": position,
			"durationSeconds": duration, "language": language,
			"updatedAt": tsPtr(updated), "available": available,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AudioStoryHandler) UpsertProgress(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	slug := chi.URLParam(r, "slug")
	var req audioProgressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		audioFail(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.PositionSeconds < 0 || req.DurationSeconds < 0 {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "progress values cannot be negative")
		return
	}
	var storyID string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT id FROM audio_stories WHERE slug=$1`, slug).Scan(&storyID); err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}
	clientTime := ""
	if strings.TrimSpace(req.UpdatedAt) != "" {
		if parsed, err := time.Parse(time.RFC3339, req.UpdatedAt); err == nil {
			clientTime = parsed.UTC().Format(time.RFC3339Nano)
		}
	}
	var updated pgtype.Timestamp
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO audio_story_progress (user_id,story_id,position_seconds,duration_seconds,language,updated_at)
		 VALUES ($1,$2,$3,$4,NULLIF($5,''),COALESCE(NULLIF($6,'')::timestamp,now()))
		 ON CONFLICT (user_id,story_id) DO UPDATE SET
		 position_seconds=EXCLUDED.position_seconds,
		 duration_seconds=EXCLUDED.duration_seconds,
		 language=EXCLUDED.language,
		 updated_at=GREATEST(audio_story_progress.updated_at, EXCLUDED.updated_at)
		 WHERE EXCLUDED.updated_at >= audio_story_progress.updated_at
		 RETURNING updated_at`,
		userID, storyID, req.PositionSeconds, req.DurationSeconds, req.Language, clientTime).Scan(&updated)
	if err != nil {
		// A losing LWW write returns no row; that is not an error.
		writeJSON(w, http.StatusOK, map[string]any{"status": "ignored"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "updatedAt": tsPtr(updated)})
}
