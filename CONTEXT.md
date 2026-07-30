# YTCI Explorer Backend

The Go backend server for the YTCI Explorer platform, serving the TanStack Start admin dashboard and Expo mobile app through a shared PostgreSQL database with PostGIS spatial queries.

## Language

### Core Concepts

**Destination**:
A location entry in the YTCI catalog — a natural or cultural site that travelers can discover, visit, and plan around. Has county, category, location coordinates, and extensive descriptive metadata.
_Avoid_: Place, poi, location, venue

**Mobile API**:
The set of endpoints under `/v1/mobile/*` and `/v1/public/*` that serve the Expo mobile app. Requires JWT or session-token authentication for action endpoints, but allows unauthenticated browsing of published content.
_Avoid_: Expo API, app API

**Public Route**:
An endpoint under `/v1/public/*` that requires no authentication. Returns only published/publishable data filtered by status (published, scheduled, active, open).
_Avoid_: Guest route, anonymous route, open route

**Authenticated Route**:
An endpoint under `/v1/mobile/*` that requires a valid JWT or session token via the JWTAuth or AuthGate middleware. Used for user-specific actions (bucket list, itinerary management, story creation, challenge participation).
_Avoid_: Logged-in route, user route

### User Engagement

**Bucket List**:
A user's personal collection of destinations they want to visit. Each item tracks whether the destination has been visited and when.
_Avoid_: Wishlist, saved destinations, favorites

**Itinerary**:
A user's multi-day trip plan with ordered stops. Each stop links to a destination or a custom location, assigned to a specific day and display order. Created by the itinerary generator or manually.
_Avoid_: Trip plan, route, schedule

**Stop**:
A single entry within an itinerary — a destination visit on a specific day at a specific position in the day's sequence. May include custom title, description, and estimated cost.
_Avoid_: Waypoint, checkpoint, location

**Story**:
A user-submitted narrative or photo journal about a destination visit. Can be liked, saved, and reported. Goes through moderation workflow (pending → approved → rejected).
_Avoid_: Post, review, trip report

**Interaction**:
A user action on a story — either a like or a save. Stored as a toggle (present or absent) rather than a count.
_Avoid_: Reaction, engagement, bookmark

### Activities & Learning

**Challenge**:
A gamified activity with a badge reward. May be time-bounded (with start_date/end_date) or perpetual (no dates). Users join, submit evidence, and progress through participation statuses (joined → in_progress → submitted → approved → rejected). Rejected evidence returns the participant to in_progress so they can resubmit. Challenge-level statuses are draft, active, and ended — all reversible. Challenges always start as draft and must be explicitly promoted to active. Only active Challenges accept participation. Deleting a Challenge transitions it to ended.
_Avoid_: Quest, mission, competition

**Challenge Evidence**:
A participant's submission for a Challenge, stored in challenge_progress. Contains status, evidence data, and moderation metadata. Visible to admins for review. When approved, a badge is awarded automatically.
_Avoid_: Submission, proof, verification

**Course**:
A structured learning module with lessons and quizzes. Users enroll and track completion through lesson progress and quiz attempts.
_Avoid_: Class, module, training

**Conservation Activity**:
A real-world volunteer event with a location, privacy level, and participant tracking. Users join and submit evidence of participation for moderation.
_Avoid_: Cleanup, volunteer event, eco-activity

### Media Pipeline

**Media Asset**:
A record in the media_assets table representing an uploaded file stored in Cloudflare R2. Has entity type/ID to link to destinations, stories, events, etc. Goes through status workflow (uploading → processing → ready → pending_review).
_Avoid_: File, attachment, image, upload

**Object Key**:
The R2 storage path for a media file (e.g. `media/1712345678/photo.jpg`). Used as the unique identifier in Cloudflare R2 and referenced by media_assets.object_key.
_Avoid_: File key, S3 key, path, filename

**Presigned URL**:
A time-limited, cryptographically signed URL that grants temporary access to upload to (PUT) or download from (GET) Cloudflare R2 without exposing credentials.
_Avoid_: Signed URL, temp URL, upload URL

**Pre-upload Record**:
A row in the pending_media_uploads table created when Presign is called and verified when Complete is called. Prevents a user from claiming ownership of another user's upload by guessing object keys.
_Avoid_: Pending media, upload intent, upload token

### Notifications

**Push Token**:
A device-specific Expo Push token stored for delivering notifications. Associated with a user and platform (ios/android). Can be deactivated when Expo reports DeviceNotRegistered.
_Avoid_: Device token, FCM token, APNS token

**Push Notification**:
A scheduled notification job in the push_notifications table. Processed by the push worker via pg_notify channel, sent through Expo Push API, with status tracking (draft → scheduled → sending → sent → failed).
_Avoid_: Alert, message, notification campaign

### Analytics

**App Open**:
A record of a user opening the mobile app. Deduplicated with a 5-minute cooldown to prevent double-counting from rapid app-switching. Used for DAU/WAU/MAU calculations.
_Avoid_: Session start, launch event, foreground event

### Account & Deletion

**Account**:
The better-auth authentication record stored in the `users` table (id, email, role, name). Managed by better-auth on the TanStack dashboard side. The Account ties a user to their credentials and sessions.
_Avoid_: Auth record, login, identity

**User**:
The collection of all YTCI-side data linked to a `user_id` across the platform — profile, stories, interactions, itineraries, activity records, push tokens. Distinct from the Account (the auth record); the User is what gets cleaned up when the Account is deleted.
_Avoid_: Account holder, member, person

**Account Deletion**:
The irreversible operation that deletes the Account and cleans or anonymizes the User's YTCI data. Content that other users can see (stories, comments, itineraries, challenge progress) is anonymized by setting FK columns to NULL. Personal-only data (bucket list, push tokens, app opens, user profile) is hard-deleted. Media in R2 associated with failed or abandoned pre-upload intents is cleaned up best-effort after the transaction commits.
_Avoid_: Account removal, user purge, sign-up removal

### Internal

**Admin API**:
The set of endpoints under `/v1/destinations`, `/v1/events`, etc. behind the AdminGate middleware. Used by the TanStack Start dashboard. Limited to super_admin, administrator, and moderator roles.
_Avoid_: Dashboard API, management API, CMS API

**Verification Status**:
An internal quality-assurance field on destinations indicating the confidence level of the data (e.g. verified, unverified, needs_review). Never exposed through Mobile API or Public Routes.
_Avoid_: Review status, content status, QA field

**Content Owner**:
An internal attribution field tracking who provided the destination content. Never exposed through Mobile API or Public Routes.
_Avoid_: Data source, contributor, author
