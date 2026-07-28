package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
)

type AdminCommentHandler struct {
	pool *pgxpool.Pool
}

func NewAdminCommentHandler(pool *pgxpool.Pool) *AdminCommentHandler {
	return &AdminCommentHandler{pool: pool}
}

func (h *AdminCommentHandler) ModerationList(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	offsetStr := r.URL.Query().Get("offset")
	offset := 0
	if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
		offset = o
	}

	queries := gen.New(h.pool)
	comments, err := queries.ModerationListComments(r.Context(), &gen.ModerationListCommentsParams{
		Limit:  int32(limit + 1),
		Offset: int32(offset),
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list comments")
		return
	}

	hasMore := len(comments) > limit
	if hasMore {
		comments = comments[:limit]
	}

	if comments == nil {
		comments = []gen.ModerationListCommentsRow{}
	}

	type item struct {
		ID           string `json:"id"`
		StoryID      string `json:"storyId"`
		AuthorID     string `json:"authorId"`
		AuthorName   string `json:"authorName"`
		Body         string `json:"body"`
		Status       string `json:"status"`
		LikeCount    int    `json:"likeCount"`
		CreatedAt    string `json:"createdAt"`
		StoryCaption string `json:"storyCaption"`
	}

	items := make([]item, len(comments))
	for i, c := range comments {
		lc := 0
		if c.LikeCount != nil {
			lc = int(*c.LikeCount)
		}
		items[i] = item{
			ID:           uuidToStr(c.ID),
			StoryID:      uuidToStr(c.StoryID),
			AuthorID:     uuidToStr(c.AuthorID),
			AuthorName:   c.AuthorName,
			Body:         c.Body,
			Status:       c.Status,
			LikeCount:    lc,
			CreatedAt:    c.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
			StoryCaption: c.StoryCaption,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":    items,
		"nextCursor": nil,
		"hasMore":  hasMore,
	})
}

func (h *AdminCommentHandler) Moderate(w http.ResponseWriter, r *http.Request) {
	commentID := r.PathValue("cid")
	if commentID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "comment id is required")
		return
	}

	var req struct {
		Action string `json:"action"`
		Reason string `json:"reason,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	if req.Action != "delete" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ACTION", "action must be 'delete'")
		return
	}

	reason := req.Reason
	if reason == "" {
		reason = "Removed by moderator"
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE story_comments SET status = 'deleted', body = '[deleted]', updated_at = now() WHERE id = $1`,
		strToUUID(commentID))
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to moderate comment")
		return
	}

	_, err = h.pool.Exec(r.Context(),
		`UPDATE story_comments SET moderation_note = $2 WHERE id = $1`,
		strToUUID(commentID), reason)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to record moderation note")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func uuidToStr(u pgtype.UUID) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u.Bytes[0:4], u.Bytes[4:6], u.Bytes[6:8], u.Bytes[8:10], u.Bytes[10:16])
}

func strToUUID(s string) pgtype.UUID {
	var u pgtype.UUID
	u.Scan(s)
	return u
}
