-- name: ListTopLevelComments :many
SELECT sc.id, sc.story_id, sc.author_id, sc.body, sc.status, sc.like_count, sc.created_at,
       COALESCE(up.display_name, 'Anonymous') AS author_name
FROM story_comments sc
LEFT JOIN user_profiles up ON up.user_id = sc.author_id
WHERE sc.story_id = $1 AND sc.parent_id IS NULL
ORDER BY sc.created_at DESC
LIMIT $2
OFFSET $3;

-- name: GetReplies :many
SELECT sc.id, sc.story_id, sc.author_id, sc.body, sc.status, sc.like_count, sc.created_at,
       COALESCE(up.display_name, 'Anonymous') AS author_name,
       sc.parent_id
FROM story_comments sc
LEFT JOIN user_profiles up ON up.user_id = sc.author_id
WHERE sc.parent_id = ANY($1::uuid[])
ORDER BY sc.created_at ASC;

-- name: GetRepliesForTopLevel :many
SELECT sc.id, sc.story_id, sc.author_id, sc.body, sc.status, sc.like_count, sc.created_at,
       COALESCE(up.display_name, 'Anonymous') AS author_name,
       sc.parent_id
FROM story_comments sc
LEFT JOIN user_profiles up ON up.user_id = sc.author_id
WHERE sc.parent_id = $1
ORDER BY sc.created_at ASC;

-- name: GetCommentByID :one
SELECT sc.id, sc.story_id, sc.author_id, sc.body, sc.status, sc.like_count, sc.created_at,
       COALESCE(up.display_name, 'Anonymous') AS author_name,
       sc.parent_id
FROM story_comments sc
LEFT JOIN user_profiles up ON up.user_id = sc.author_id
WHERE sc.id = $1;

-- name: CreateComment :one
INSERT INTO story_comments (story_id, author_id, body, parent_id)
VALUES ($1, $2, $3, $4)
RETURNING id, story_id, author_id, body, status, like_count, created_at;

-- name: UpdateCommentBody :one
UPDATE story_comments SET body = $2, updated_at = now()
WHERE id = $1
RETURNING id, story_id, author_id, body, status, like_count, created_at;

-- name: SoftDeleteComment :one
UPDATE story_comments SET status = 'deleted', body = '[deleted]', updated_at = now()
WHERE id = $1
RETURNING id, story_id, author_id, body, status, like_count, created_at, parent_id;

-- name: ToggleCommentInteraction :one
WITH deleted AS (
    DELETE FROM comment_interactions
    WHERE user_id = $1 AND comment_id = $2 AND interaction_type = $3
    RETURNING 1
)
INSERT INTO comment_interactions (user_id, comment_id, interaction_type)
SELECT $1, $2, $3
WHERE NOT EXISTS (SELECT 1 FROM deleted)
RETURNING id;

-- name: ModerationListComments :many
SELECT sc.id, sc.story_id, sc.author_id, sc.body, sc.status, sc.like_count, sc.created_at,
       COALESCE(up.display_name, 'Anonymous') AS author_name,
       COALESCE(s.caption, '') AS story_caption
FROM story_comments sc
LEFT JOIN user_profiles up ON up.user_id = sc.author_id
LEFT JOIN stories s ON s.id = sc.story_id
ORDER BY sc.created_at DESC
LIMIT $1
OFFSET $2;

-- name: CountCommentInteractions :one
SELECT COUNT(*) FROM comment_interactions
WHERE comment_id = $1 AND interaction_type = $2;
