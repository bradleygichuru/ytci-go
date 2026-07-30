package expo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type CommentHandler struct {
	pool *pgxpool.Pool
}

func NewCommentHandler(pool *pgxpool.Pool) *CommentHandler {
	return &CommentHandler{pool: pool}
}

type commentResponse struct {
	ID         string            `json:"id"`
	StoryID    string            `json:"storyId"`
	AuthorID   string            `json:"authorId"`
	AuthorName string            `json:"authorName"`
	Body       string            `json:"body"`
	Status     string            `json:"status"`
	LikeCount  int               `json:"likeCount"`
	IsLiked    bool              `json:"isLiked"`
	CreatedAt  string            `json:"createdAt"`
	Replies    []replyResponse   `json:"replies"`
}

type replyResponse struct {
	ID         string `json:"id"`
	AuthorID   string `json:"authorId"`
	AuthorName string `json:"authorName"`
	Body       string `json:"body"`
	Status     string `json:"status"`
	LikeCount  int    `json:"likeCount"`
	IsLiked    bool   `json:"isLiked"`
	CreatedAt  string `json:"createdAt"`
	ParentID   string `json:"parentId"`
}

func (h *CommentHandler) ListComments(w http.ResponseWriter, r *http.Request) {
	storyID := r.PathValue("id")
	if storyID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "story id is required")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 50 {
		limit = l
	}

	offsetStr := r.URL.Query().Get("offset")
	offset := 0
	if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
		offset = o
	}

	queries := gen.New(h.pool)

	topComments, err := queries.ListTopLevelComments(r.Context(), &gen.ListTopLevelCommentsParams{
		StoryID: toUUID(storyID),
		Limit:   int32(limit + 1),
		Offset:  int32(offset),
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list comments")
		return
	}

	hasMore := len(topComments) > limit
	if hasMore {
		topComments = topComments[:limit]
	}

	if topComments == nil {
		topComments = []gen.ListTopLevelCommentsRow{}
	}

	var topIDs []pgtype.UUID
	for _, tc := range topComments {
		topIDs = append(topIDs, tc.ID)
	}

	var repliesMap map[pgtype.UUID][]gen.GetRepliesRow
	if len(topIDs) > 0 {
		replies, err := queries.GetReplies(r.Context(), topIDs)
		if err != nil {
			handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load replies")
			return
		}

		repliesMap = make(map[pgtype.UUID][]gen.GetRepliesRow, len(topIDs))
		for _, rep := range replies {
			repliesMap[rep.ParentID] = append(repliesMap[rep.ParentID], rep)
		}
	}

	userID := middleware.UserID(r.Context())
	hasAuth := userID != ""

	var commentIDs []pgtype.UUID
	for _, tc := range topComments {
		commentIDs = append(commentIDs, tc.ID)
		for _, rep := range repliesMap[tc.ID] {
			commentIDs = append(commentIDs, rep.ID)
		}
	}

	likedSet := make(map[string]bool)
	if hasAuth && len(commentIDs) > 0 {
		likedSet = h.resolveLiked(r.Context(), userID, commentIDs)
	}

	items := make([]commentResponse, 0, len(topComments))
	for _, tc := range topComments {
		authorName := tc.AuthorName
		if tc.Status == "deleted" {
			authorName = "[deleted]"
		}
		c := commentResponse{
			ID:         uuidString(tc.ID),
			StoryID:    uuidString(tc.StoryID),
			AuthorID:   ptrVal(tc.AuthorID),
			AuthorName: authorName,
			Body:       tc.Body,
			Status:     tc.Status,
			LikeCount:  int(ptrToInt32(tc.LikeCount)),
			IsLiked:    likedSet[uuidString(tc.ID)],
			CreatedAt:  tc.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
			Replies:    make([]replyResponse, 0, len(repliesMap[tc.ID])),
		}

		for _, rep := range repliesMap[tc.ID] {
			repAuthorName := rep.AuthorName
			if rep.Status == "deleted" {
				repAuthorName = "[deleted]"
			}
			r := replyResponse{
				ID:         uuidString(rep.ID),
				AuthorID:   ptrVal(rep.AuthorID),
				AuthorName: repAuthorName,
				Body:       rep.Body,
				Status:     rep.Status,
				LikeCount:  int(ptrToInt32(rep.LikeCount)),
				IsLiked:    likedSet[uuidString(rep.ID)],
				CreatedAt:  rep.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
				ParentID:   uuidString(rep.ParentID),
			}
			c.Replies = append(c.Replies, r)
		}

		items = append(items, c)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":    items,
		"nextCursor": nil,
		"hasMore":  hasMore,
	})
}

func (h *CommentHandler) resolveLiked(ctx context.Context, userID string, commentIDs []pgtype.UUID) map[string]bool {
	result := make(map[string]bool, len(commentIDs))

	rows, err := h.pool.Query(ctx,
		`SELECT comment_id FROM comment_interactions
		 WHERE user_id = $1 AND comment_id = ANY($2::uuid[]) AND interaction_type = 'like'`,
		userID, commentIDs)
	if err != nil {
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var cid pgtype.UUID
		rows.Scan(&cid)
		result[uuidString(cid)] = true
	}
	return result
}

func (h *CommentHandler) CreateComment(w http.ResponseWriter, r *http.Request) {
	storyID := r.PathValue("id")
	if storyID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "story id is required")
		return
	}

	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Body == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "body is required")
		return
	}

	queries := gen.New(h.pool)
	userID := middleware.UserID(r.Context())
	comment, err := queries.CreateComment(r.Context(), &gen.CreateCommentParams{
		StoryID:  toUUID(storyID),
		AuthorID: &userID,
		Body:     req.Body,
		ParentID: pgtype.UUID{},
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create comment")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":     uuidString(comment.ID),
		"status": comment.Status,
	})
}

func (h *CommentHandler) CreateReply(w http.ResponseWriter, r *http.Request) {
	storyID := r.PathValue("id")
	parentID := r.PathValue("cid")
	if storyID == "" || parentID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "story id and comment id are required")
		return
	}

	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Body == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "body is required")
		return
	}

	queries := gen.New(h.pool)

	parent, err := queries.GetCommentByID(r.Context(), toUUID(parentID))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "parent comment not found")
		return
	}

	if parent.ParentID.Valid {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_PARENT", "cannot reply to a reply (max depth is 2)")
		return
	}

	userID := middleware.UserID(r.Context())
	comment, err := queries.CreateComment(r.Context(), &gen.CreateCommentParams{
		StoryID:  toUUID(storyID),
		AuthorID: &userID,
		Body:     req.Body,
		ParentID: toUUID(parentID),
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create reply")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":     uuidString(comment.ID),
		"status": comment.Status,
	})
}

func (h *CommentHandler) UpdateComment(w http.ResponseWriter, r *http.Request) {
	commentID := r.PathValue("cid")
	if commentID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "comment id is required")
		return
	}

	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Body == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "body is required")
		return
	}

	userID := middleware.UserID(r.Context())

	queries := gen.New(h.pool)
	existing, err := queries.GetCommentByID(r.Context(), toUUID(commentID))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "comment not found")
		return
	}

	if ptrVal(existing.AuthorID) != userID {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you can only edit your own comments")
		return
	}

	_, err = queries.UpdateCommentBody(r.Context(), &gen.UpdateCommentBodyParams{
		ID:   toUUID(commentID),
		Body: req.Body,
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update comment")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *CommentHandler) DeleteComment(w http.ResponseWriter, r *http.Request) {
	commentID := r.PathValue("cid")
	if commentID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "comment id is required")
		return
	}

	userID := middleware.UserID(r.Context())

	queries := gen.New(h.pool)

	existing, err := queries.GetCommentByID(r.Context(), toUUID(commentID))
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "comment not found")
		} else {
			handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get comment")
		}
		return
	}

	isAuthor := ptrVal(existing.AuthorID) == userID
	if isAuthor {
		_, err = queries.SoftDeleteComment(r.Context(), toUUID(commentID))
		if err != nil {
			handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete comment")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
		return
	}

	var storyCreatorID string
	err = h.pool.QueryRow(r.Context(),
		`SELECT creator_id FROM stories WHERE id = $1`, existing.StoryID).Scan(&storyCreatorID)
	if err == nil && storyCreatorID == userID {
		_, err = queries.SoftDeleteComment(r.Context(), toUUID(commentID))
		if err != nil {
			handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete comment")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
		return
	}

	handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you can only delete your own comments")
}

func (h *CommentHandler) ToggleLike(w http.ResponseWriter, r *http.Request) {
	commentID := r.PathValue("cid")
	if commentID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "comment id is required")
		return
	}

	queries := gen.New(h.pool)

	_, err := queries.ToggleCommentInteraction(r.Context(), &gen.ToggleCommentInteractionParams{
		UserID:          middleware.UserID(r.Context()),
		CommentID:       toUUID(commentID),
		InteractionType: "like",
	})
	if err != nil && err != pgx.ErrNoRows {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to toggle like")
		return
	}

	cid := toUUID(commentID)
	var cnt int64
	h.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM comment_interactions WHERE comment_id = $1 AND interaction_type = 'like'`,
		cid).Scan(&cnt)
	h.pool.Exec(r.Context(),
		`UPDATE story_comments SET like_count = $2 WHERE id = $1`,
		cid, cnt)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "toggled"})
}

func (h *CommentHandler) ReportComment(w http.ResponseWriter, r *http.Request) {
	commentID := r.PathValue("cid")
	if commentID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "comment id is required")
		return
	}

	var req struct {
		Reason  string  `json:"reason"`
		Details *string `json:"details,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	queries := gen.New(h.pool)

	existing, err := queries.GetCommentByID(r.Context(), toUUID(commentID))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "comment not found")
		return
	}

	var details string
	if req.Details != nil {
		details = *req.Details
	}
	details = "comment_id: " + uuidString(existing.ID) + " | " + details

	_, err = h.pool.Exec(r.Context(),
		`INSERT INTO story_reports (story_id, reported_by, reason, details) VALUES ($1, $2, $3, $4)`,
		existing.StoryID, middleware.UserID(r.Context()), req.Reason, details)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to report comment")
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "reported"})
}

func toUUID(s string) pgtype.UUID {
	var u pgtype.UUID
	u.Scan(s)
	return u
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", u.Bytes[0:4], u.Bytes[4:6], u.Bytes[6:8], u.Bytes[8:10], u.Bytes[10:16])
}

func ptrToInt32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func ptrVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string {
	return &s
}
