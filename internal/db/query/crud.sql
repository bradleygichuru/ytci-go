-- name: CreateEvent :one
INSERT INTO events (title, organizer, county, venue, event_date, end_date, type, description, contact_email, contact_phone, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: UpdateEvent :one
UPDATE events SET
    title = COALESCE(NULLIF($2, ''), title),
    organizer = COALESCE(NULLIF($3, ''), organizer),
    description = COALESCE($4, description),
    status = COALESCE(NULLIF($5, ''), status),
    updated_at = now()
WHERE id = $1 RETURNING *;

-- name: DeleteEvent :one
UPDATE events SET status = 'cancelled', updated_at = now() WHERE id = $1 RETURNING *;

-- name: CreateCampaign :one
INSERT INTO campaigns (title, banner_url, type, status, start_date, end_date, target_url, created_by)
VALUES ($1, $2, $3, 'draft', $4, $5, $6, $7)
RETURNING *;

-- name: UpdateCampaignStatus :one
UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: JoinChallenge :one
INSERT INTO challenge_progress (user_id, challenge_id, status)
VALUES ($1, $2, 'joined')
ON CONFLICT (user_id, challenge_id) DO NOTHING
RETURNING *;

-- name: CreateCourse :one
INSERT INTO courses (title, description, difficulty, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: AddBucketItem :one
INSERT INTO bucket_list_items (user_id, destination_id, visited)
VALUES ($1, $2, false)
ON CONFLICT (user_id, destination_id) DO NOTHING
RETURNING *;

-- name: RemoveBucketItem :exec
DELETE FROM bucket_list_items WHERE user_id = $1 AND destination_id = $2;

-- name: MarkDestinationVisited :one
UPDATE bucket_list_items SET visited = true, visited_at = now() WHERE user_id = $1 AND destination_id = $2 RETURNING *;

-- name: RecordAppOpen :one
INSERT INTO app_opens (user_id, platform, app_version)
VALUES ($1, $2, $3) RETURNING id;

-- name: GetQuizWithQuestions :one
SELECT id, course_id, title, questions, pass_threshold
FROM quizzes WHERE id = $1;

-- name: CreateCourseWithFields :one
INSERT INTO courses (title, description, difficulty, created_by)
VALUES ($1, $2, $3, $4) RETURNING id, title, difficulty, status, created_at;

-- name: CreateChallenge :one
INSERT INTO challenges (title, description, badge_name, status, start_date, end_date, created_by)
VALUES ($1, $2, $3, 'draft', $4, $5, $6) RETURNING *;

-- name: CreateItineraryWithStops :one
INSERT INTO itineraries (user_id, title, inputs, status)
VALUES ($1, $2, $3, 'draft') RETURNING id, title, status, created_at;
