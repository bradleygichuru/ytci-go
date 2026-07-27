-- name: ListStories :many
SELECT * FROM stories
WHERE status = 'approved'
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: GetStoryByID :one
SELECT * FROM stories WHERE id = $1;

-- name: CreateStory :one
INSERT INTO stories (creator_id, destination_id, caption, journal, tags, status)
VALUES ($1, $2, $3, $4, $5, 'pending')
RETURNING *;

-- name: UpdateStoryStatus :one
UPDATE stories SET
    status = $2,
    moderated_by = $3,
    moderation_note = $4,
    moderated_at = now()
WHERE id = $1
RETURNING *;
