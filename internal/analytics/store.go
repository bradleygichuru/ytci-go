package analytics

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the analytics persistence layer. It never reads users from event
// rows; the only user→analytics_id lookup is analytics_id_map.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ConsentVersion returns the consent version the server records when a client
// has not yet written one. Override with ANALYTICS_CONSENT_VERSION.
func ConsentVersion() string {
	if v := strings.TrimSpace(os.Getenv("ANALYTICS_CONSENT_VERSION")); v != "" {
		return v
	}
	return defaultConsentVer
}

// Under18IdentifiedEnabled reports whether identified analytics for a
// self-declared under-18 may be collected. It stays false until ticket #346
// (consent/notice/under-18 sign-off) is closed.
func Under18IdentifiedEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ANALYTICS_UNDER_18_IDENTIFIED_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// LatestConsent returns the newest consent row for the analytics_id. A missing
// row is represented as an `unknown` consent at the current version.
func (s *Store) LatestConsent(ctx context.Context, analyticsID string) (Consent, error) {
	c := Consent{
		AnalyticsID: analyticsID,
		Scope:       ConsentScope,
		Version:     ConsentVersion(),
		State:       ConsentUnknown,
	}
	var recorded time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT version, state, recorded_at
		FROM analytics_consents
		WHERE analytics_id = $1 AND scope = 'analytics'
		ORDER BY recorded_at DESC
		LIMIT 1`, analyticsID).Scan(&c.Version, &c.State, &recorded)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	c.RecordedAt = &recorded
	return c, nil
}

// SetConsent records a consent decision. Consent is versioned: a new version
// creates a new row rather than rewriting history. The write must be bound to
// the caller: an authenticated account may only write consent for its own
// analytics_id, and a guest must prove the install that minted the id. Without
// this a caller who knows an id could forge a grant.
func (s *Store) SetConsent(ctx context.Context, w ConsentWrite, userID string) (Consent, error) {
	if rej := s.checkIdentityBinding(ctx, s.pool, w.AnalyticsID, w.InstallID, userID); rej != nil {
		return Consent{}, rej
	}
	switch w.State {
	case ConsentGranted, ConsentDenied:
	default:
		return Consent{}, reject(http.StatusBadRequest, "INVALID_CONSENT_STATE", "state must be granted or denied")
	}
	switch w.Source {
	case "guest_device", "account":
	default:
		return Consent{}, reject(http.StatusBadRequest, "INVALID_CONSENT_SOURCE", "source must be guest_device or account")
	}
	if w.Version == "" || len(w.Version) > 64 {
		return Consent{}, reject(http.StatusBadRequest, "INVALID_CONSENT_VERSION", "version is required")
	}

	var recorded time.Time
	var withdrawn any
	if w.State == ConsentDenied {
		withdrawn = time.Now().UTC()
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO analytics_consents (analytics_id, scope, version, state, source, recorded_at, withdrawn_at)
		VALUES ($1, 'analytics', $2, $3, $4, now(), $5)
		ON CONFLICT (analytics_id, scope, version) DO UPDATE SET
			state = EXCLUDED.state,
			source = EXCLUDED.source,
			recorded_at = now(),
			withdrawn_at = EXCLUDED.withdrawn_at
		RETURNING recorded_at`,
		w.AnalyticsID, w.Version, w.State, w.Source, withdrawn).Scan(&recorded)
	if err != nil {
		return Consent{}, err
	}
	return Consent{
		AnalyticsID: w.AnalyticsID,
		Scope:       ConsentScope,
		Version:     w.Version,
		State:       w.State,
		RecordedAt:  &recorded,
	}, nil
}

// WithdrawConsent records a denial at the supplied version (or the current
// default). Withdrawal is the client's signal to clear its offline queue. Like
// SetConsent it is bound to the caller's account or install.
func (s *Store) WithdrawConsent(ctx context.Context, analyticsID, installID, version, userID string) (Consent, error) {
	if version == "" {
		version = ConsentVersion()
	}
	source := "guest_device"
	if userID != "" {
		source = "account"
	}
	return s.SetConsent(ctx, ConsentWrite{
		AnalyticsID: analyticsID,
		InstallID:   installID,
		Version:     version,
		State:       ConsentDenied,
		Source:      source,
	}, userID)
}

// UpsertIdentity mints or reuses an analytics_id for an install. install_id is
// idempotent: the same install always resolves to the same analytics_id.
func (s *Store) UpsertIdentity(ctx context.Context, req InstallRequest, actorType, userID string) (Identity, error) {
	if _, err := uuid.Parse(req.InstallID); err != nil {
		return Identity{}, reject(http.StatusBadRequest, "INVALID_INSTALL_ID", "installId must be a uuid")
	}
	if req.Platform != "ios" && req.Platform != "android" {
		return Identity{}, reject(http.StatusBadRequest, "INVALID_PLATFORM", "platform must be ios or android")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Identity{}, err
	}
	defer tx.Rollback(ctx)

	var current string
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(analytics_id::text, '') FROM analytics_installs WHERE install_id = $1 FOR UPDATE`,
		req.InstallID).Scan(&current)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		current = ""
	case err != nil:
		return Identity{}, err
	}

	analyticsID := current
	if analyticsID == "" {
		analyticsID = uuid.NewString()
	}

	if err := upsertInstall(ctx, tx, req, analyticsID); err != nil {
		return Identity{}, err
	}

	if actorType == ActorAccount && userID != "" {
		var mapped string
		err := tx.QueryRow(ctx, `
			INSERT INTO analytics_id_map (user_id, analytics_id, install_id, actor_type)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id) DO UPDATE SET
				install_id = EXCLUDED.install_id,
				actor_type = EXCLUDED.actor_type
			RETURNING analytics_id::text`,
			userID, analyticsID, req.InstallID, actorType).Scan(&mapped)
		if err != nil {
			return Identity{}, err
		}
		analyticsID = mapped
		// Keep the install pointing at the account's stable analytics_id.
		if _, err := tx.Exec(ctx,
			`UPDATE analytics_installs SET analytics_id = $2 WHERE install_id = $1`,
			req.InstallID, analyticsID); err != nil {
			return Identity{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Identity{}, err
	}

	consent, err := s.LatestConsent(ctx, analyticsID)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		AnalyticsID: analyticsID,
		InstallID:   req.InstallID,
		ActorType:   actorType,
		Consent:     consent,
	}, nil
}

func upsertInstall(ctx context.Context, tx pgx.Tx, req InstallRequest, analyticsID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO analytics_installs
			(install_id, platform, os_version, app_version, build, locale, analytics_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (install_id) DO UPDATE SET
			last_seen_at = now(),
			platform = COALESCE(EXCLUDED.platform, analytics_installs.platform),
			os_version = COALESCE(EXCLUDED.os_version, analytics_installs.os_version),
			app_version = COALESCE(EXCLUDED.app_version, analytics_installs.app_version),
			build = COALESCE(EXCLUDED.build, analytics_installs.build),
			locale = COALESCE(EXCLUDED.locale, analytics_installs.locale)`,
		req.InstallID, nullIfEmpty(req.Platform), nullIfEmpty(req.OSVersion),
		nullIfEmpty(req.AppVersion), nullIfEmpty(req.Build), nullIfEmpty(req.Locale),
		analyticsID)
	return err
}

// batchID is deterministic so a retried offline flush returns the same ack
// without writing duplicate batches or events.
func batchID(b *Batch) string {
	ids := make([]string, 0, len(b.Events))
	for _, ev := range b.Events {
		ids = append(ids, ev.EventID)
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(b.AnalyticsID + "|" + b.InstallID + "|" + strings.Join(ids, ",")))
	return uuid.NewSHA1(uuid.NameSpaceOID, sum[:]).String()
}

func (s *Store) checkBatchIdentity(ctx context.Context, tx pgx.Tx, b *Batch, userID string) *RejectError {
	return s.checkIdentityBinding(ctx, tx, b.AnalyticsID, b.InstallID, userID)
}

// rowQuerier is satisfied by *pgxpool.Pool and pgx.Tx.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// checkIdentityBinding proves the caller may act on the analytics_id. An
// authenticated account must own the id through analytics_id_map; a guest must
// present the install that minted it. Both reads are the only paths from an
// actor to an analytics_id.
func (s *Store) checkIdentityBinding(ctx context.Context, q rowQuerier, analyticsID, installID, userID string) *RejectError {
	if _, err := uuid.Parse(analyticsID); err != nil {
		return reject(http.StatusBadRequest, "INVALID_ANALYTICS_ID", "analyticsId must be a uuid")
	}
	if userID != "" {
		var mapped string
		err := q.QueryRow(ctx, `
			SELECT analytics_id::text FROM analytics_id_map
			WHERE user_id = $1 AND erased_at IS NULL`, userID).Scan(&mapped)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && mapped != analyticsID) {
			return reject(http.StatusForbidden, "ANALYTICS_ID_UNKNOWN", "analytics_id is not bound to this account")
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return reject(http.StatusInternalServerError, "INTERNAL_ERROR", "identity lookup failed")
		}
		return nil
	}
	if installID == "" {
		return reject(http.StatusBadRequest, "INSTALL_ID_REQUIRED", "installId is required for guest requests")
	}
	var installAnalytics string
	err := q.QueryRow(ctx,
		`SELECT COALESCE(analytics_id::text, '') FROM analytics_installs WHERE install_id = $1`,
		installID).Scan(&installAnalytics)
	if err != nil || installAnalytics != analyticsID {
		return reject(http.StatusForbidden, "ANALYTICS_ID_UNKNOWN", "analytics_id is not bound to this install")
	}
	return nil
}

func (s *Store) checkConsent(ctx context.Context, tx pgx.Tx, analyticsID, version string) *RejectError {
	var state, storedVersion string
	err := tx.QueryRow(ctx, `
		SELECT state, version FROM analytics_consents
		WHERE analytics_id = $1 AND scope = 'analytics'
		ORDER BY recorded_at DESC LIMIT 1`, analyticsID).Scan(&state, &storedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return reject(http.StatusForbidden, "CONSENT_REQUIRED", "analytics consent has not been granted")
	}
	if err != nil {
		return reject(http.StatusInternalServerError, "INTERNAL_ERROR", "consent lookup failed")
	}
	switch state {
	case ConsentDenied:
		return reject(http.StatusForbidden, "CONSENT_WITHDRAWN", "analytics consent has been withdrawn")
	case ConsentGranted:
	default:
		return reject(http.StatusForbidden, "CONSENT_REQUIRED", "analytics consent has not been granted")
	}
	if storedVersion != version {
		return reject(http.StatusForbidden, "CONSENT_STALE", "consent version does not match the accepted notice")
	}
	return nil
}

type loadedSchema struct {
	schema eventSchema
	banned []string
}

func (s *Store) loadSchemas(ctx context.Context, tx pgx.Tx, names []string) (map[string]loadedSchema, error) {
	out := make(map[string]loadedSchema, len(names))
	if len(names) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT event_name, property_schema, banned_keys
		FROM analytics_event_schemas
		WHERE event_name = ANY($1) AND status = 'active'`, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var raw []byte
		var banned []string
		if err := rows.Scan(&name, &raw, &banned); err != nil {
			return nil, err
		}
		var schema eventSchema
		if err := json.Unmarshal(raw, &schema); err != nil {
			return nil, fmt.Errorf("parse schema for %s: %w", name, err)
		}
		out[name] = loadedSchema{schema: schema, banned: banned}
	}
	return out, rows.Err()
}

type cleanEvent struct {
	event   Event
	ts      time.Time
	props   map[string]any
	dropped []string
	family  string
	name    string
	route   *string
}

// IngestBatch validates, filters and stores an offline flush. Consent is
// checked at flush even when the client already checked it at enqueue.
func (s *Store) IngestBatch(ctx context.Context, userID string, b *Batch, now time.Time) (BatchAck, error) {
	id := batchID(b)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BatchAck{}, err
	}
	defer tx.Rollback(ctx)

	// Idempotency first: a retried flush returns its original acknowledgement.
	var accepted, dropped, unknownKeys int
	err = tx.QueryRow(ctx, `
		SELECT accepted_count, dropped_count, unknown_key_count
		FROM analytics_ingest_batches WHERE id = $1`, id).Scan(&accepted, &dropped, &unknownKeys)
	if err == nil {
		return BatchAck{BatchID: id, Accepted: accepted, Dropped: dropped, UnknownKeys: unknownKeys}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BatchAck{}, err
	}

	if rej := s.checkBatchIdentity(ctx, tx, b, userID); rej != nil {
		return BatchAck{}, rej
	}
	if rej := s.checkConsent(ctx, tx, b.AnalyticsID, b.ConsentVersion); rej != nil {
		return BatchAck{}, rej
	}

	schemaNames := make([]string, 0, len(b.Events))
	seen := make(map[string]bool)
	for _, ev := range b.Events {
		key := schemaForEvent(ev.Name, ev.Family)
		if !seen[key] {
			seen[key] = true
			schemaNames = append(schemaNames, key)
		}
	}
	schemas, err := s.loadSchemas(ctx, tx, schemaNames)
	if err != nil {
		return BatchAck{}, err
	}

	clean := make([]cleanEvent, 0, len(b.Events))
	rejected := make([]string, 0)
	unknownKeyCount := 0

	var sessionStart time.Time
	var haveStart bool
	if prev, err := sessionStartTime(ctx, tx, b.SessionID); err == nil {
		sessionStart, haveStart = prev, true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return BatchAck{}, err
	}

	for i := range b.Events {
		ev := b.Events[i]
		ts, reason := validateEvent(&ev, b, now)
		if reason != "" {
			rejected = append(rejected, ev.EventID)
			dropped++
			continue
		}
		if !haveStart || ts.Before(sessionStart) {
			sessionStart = ts
			haveStart = true
		}
		// A session is hard-capped at 24 hours (ADR 0016 §3).
		if ts.After(sessionStart.Add(24 * time.Hour)) {
			rejected = append(rejected, ev.EventID)
			dropped++
			continue
		}

		key := schemaForEvent(ev.Name, ev.Family)
		loaded, ok := schemas[key]
		if !ok {
			loaded = loadedSchema{schema: eventSchema{Properties: map[string]schemaProperty{}}}
		}
		props, droppedKeys := filterProperties(ev.Properties, loaded.schema, loaded.banned)
		unknownKeyCount += len(droppedKeys)
		clean = append(clean, cleanEvent{
			event:   ev,
			ts:      ts,
			props:   props,
			dropped: droppedKeys,
			family:  ev.Family,
			name:    ev.Name,
			route:   ev.Route,
		})
	}

	// Re-run the cap check against the earliest event because a later event
	// can extend the session start backwards. Events past the cap are dropped.
	final := make([]cleanEvent, 0, len(clean))
	for _, ev := range clean {
		if ev.ts.After(sessionStart.Add(24 * time.Hour)) {
			rejected = append(rejected, ev.event.EventID)
			dropped++
			continue
		}
		final = append(final, ev)
	}
	clean = final

	var sessionEnd *time.Time
	if haveStart {
		end := sessionStart
		for _, ev := range clean {
			if ev.ts.After(end) {
				end = ev.ts
			}
		}
		sessionEnd = &end
	}

	tz := 180
	if b.TZOffsetMin != nil {
		tz = *b.TZOffsetMin
	}
	actorType := b.ActorType
	if actorType == "" {
		actorType = ActorGuest
		if userID != "" {
			actorType = ActorAccount
		}
	}

	for _, ev := range clean {
		propsJSON, _ := json.Marshal(ev.props)
		if ev.props == nil {
			propsJSON = []byte(`{}`)
		}
		droppedKeys := ev.dropped
		if droppedKeys == nil {
			droppedKeys = []string{}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO analytics_events
				(client_event_id, analytics_id, session_id, session_seq, event_family,
				 event, route, client_ts, tz_offset_min, app_version, build, platform,
				 os_version, locale, network, actor_type, properties, dropped_properties)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			ON CONFLICT DO NOTHING`,
			ev.event.EventID, b.AnalyticsID, b.SessionID, ev.event.SessionSeq, ev.family,
			ev.name, ev.route, ev.ts, tz, nullIfEmpty(b.AppVersion), nullIfEmpty(b.Build),
			nullIfEmpty(b.Platform), nullIfEmpty(b.OSVersion), nullIfEmpty(b.Locale),
			nullIfEmpty(b.Network), actorType, propsJSON, droppedKeys)
		if err != nil {
			return BatchAck{}, err
		}
	}
	accepted = len(clean)

	if haveStart {
		if err := upsertSession(ctx, tx, b, actorType, tz, sessionStart, sessionEnd, len(clean)); err != nil {
			return BatchAck{}, err
		}
	}

	status := "accepted"
	if dropped > 0 && accepted > 0 {
		status = "partial"
	} else if accepted == 0 {
		status = "rejected"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO analytics_ingest_batches
			(id, analytics_id, install_id, consent_version, event_count, accepted_count,
			 dropped_count, unknown_key_count, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, b.AnalyticsID, nullIfEmpty(b.InstallID), b.ConsentVersion, len(b.Events),
		accepted, dropped, unknownKeyCount, status)
	if err != nil {
		return BatchAck{}, err
	}

	_, err = tx.Exec(ctx,
		`UPDATE analytics_installs SET last_seen_at = now() WHERE install_id = $1`, b.InstallID)
	if err != nil {
		return BatchAck{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return BatchAck{}, err
	}
	return BatchAck{
		BatchID:          id,
		Accepted:         accepted,
		Dropped:          dropped,
		UnknownKeys:      unknownKeyCount,
		RejectedEventIDs: rejected,
	}, nil
}

func sessionStartTime(ctx context.Context, tx pgx.Tx, sessionID string) (time.Time, error) {
	var start time.Time
	err := tx.QueryRow(ctx,
		`SELECT started_at FROM analytics_sessions WHERE session_id = $1`, sessionID).Scan(&start)
	return start, err
}

func upsertSession(ctx context.Context, tx pgx.Tx, b *Batch, actorType string, tz int, start time.Time, end *time.Time, count int) error {
	capAt := start.Add(24 * time.Hour)
	capped := false
	if end != nil && end.After(capAt) {
		end = &capAt
		capped = true
	}
	var cappedAt any
	if capped {
		cappedAt = time.Now().UTC()
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO analytics_sessions
			(session_id, analytics_id, install_id, started_at, ended_at, client_ts,
			 tz_offset_min, app_version, build, platform, os_version, locale, network,
			 actor_type, event_count, capped_at)
		VALUES ($1,$2,$3,$4,$5,$4,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (session_id) DO UPDATE SET
			started_at = LEAST(analytics_sessions.started_at, EXCLUDED.started_at),
			ended_at = LEAST(
				GREATEST(COALESCE(analytics_sessions.ended_at, EXCLUDED.ended_at), EXCLUDED.ended_at),
				LEAST(analytics_sessions.started_at, EXCLUDED.started_at) + interval '24 hours'),
			event_count = analytics_sessions.event_count + EXCLUDED.event_count,
			capped_at = COALESCE(analytics_sessions.capped_at, EXCLUDED.capped_at)`,
		b.SessionID, b.AnalyticsID, nullIfEmpty(b.InstallID), start, end, tz,
		nullIfEmpty(b.AppVersion), nullIfEmpty(b.Build), nullIfEmpty(b.Platform),
		nullIfEmpty(b.OSVersion), nullIfEmpty(b.Locale), nullIfEmpty(b.Network),
		actorType, count, cappedAt)
	return err
}

// ExportByUser returns the reader's own analytics events and sessions. The
// account mapping is created lazily so a fresh account gets a valid empty
// export rather than a 404.
func (s *Store) ExportByUser(ctx context.Context, userID string) (Export, error) {
	var analyticsID string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO analytics_id_map (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING analytics_id::text`, userID).Scan(&analyticsID)
	if err != nil {
		return Export{}, err
	}

	out := Export{
		AnalyticsID: analyticsID,
		GeneratedAt: time.Now().UTC(),
		Events:      []ExportEvent{},
		Sessions:    []ExportSession{},
	}

	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(client_event_id::text, ''), COALESCE(session_id::text, ''),
		       COALESCE(session_seq, 0), COALESCE(event_family, 'action'), event,
		       route, COALESCE(to_char(client_ts AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), ''),
		       COALESCE(properties, '{}'::jsonb)
		FROM analytics_events
		WHERE analytics_id = $1
		ORDER BY client_ts NULLS LAST, server_seq`, analyticsID)
	if err != nil {
		return Export{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var ev ExportEvent
		var props []byte
		if err := rows.Scan(&ev.EventID, &ev.SessionID, &ev.SessionSeq, &ev.Family,
			&ev.Name, &ev.Route, &ev.ClientTS, &props); err != nil {
			return Export{}, err
		}
		if len(props) > 0 {
			_ = json.Unmarshal(props, &ev.Properties)
		}
		out.Events = append(out.Events, ev)
	}
	if err := rows.Err(); err != nil {
		return Export{}, err
	}

	srows, err := s.pool.Query(ctx, `
		SELECT session_id::text, started_at, ended_at, event_count
		FROM analytics_sessions WHERE analytics_id = $1
		ORDER BY started_at`, analyticsID)
	if err != nil {
		return Export{}, err
	}
	defer srows.Close()
	for srows.Next() {
		var sess ExportSession
		if err := srows.Scan(&sess.SessionID, &sess.StartedAt, &sess.EndedAt, &sess.EventCount); err != nil {
			return Export{}, err
		}
		out.Sessions = append(out.Sessions, sess)
	}
	return out, srows.Err()
}

// RequestErasure creates an erasure request and tombstones the subject's raw
// analytics rows. Aggregates are retained because they no longer identify a
// reader; consent records are kept as proof of the decision.
func (s *Store) RequestErasure(ctx context.Context, subjectType, subjectRef string) (ErasureRequest, error) {
	return s.RequestErasureAs(ctx, subjectType, subjectRef, "")
}

// RequestErasureAs is RequestErasure with the acting admin recorded in the
// restricted identity access log.
func (s *Store) RequestErasureAs(ctx context.Context, subjectType, subjectRef, actorID string) (ErasureRequest, error) {
	switch subjectType {
	case "user", "analytics_id":
	default:
		return ErasureRequest{}, reject(http.StatusBadRequest, "INVALID_SUBJECT_TYPE", "subjectType must be user or analytics_id")
	}
	if strings.TrimSpace(subjectRef) == "" {
		return ErasureRequest{}, reject(http.StatusBadRequest, "INVALID_SUBJECT_REF", "subjectRef is required")
	}
	if subjectType == "analytics_id" {
		if _, err := uuid.Parse(subjectRef); err != nil {
			return ErasureRequest{}, reject(http.StatusBadRequest, "INVALID_SUBJECT_REF", "subjectRef must be a uuid")
		}
	}

	now := time.Now().UTC()
	req := ErasureRequest{
		ID:               uuid.NewString(),
		SubjectType:      subjectType,
		Status:           "pending",
		RequestedAt:      now,
		TargetBy:         now.AddDate(0, 0, 14),
		BackupCycleUntil: ptrTime(now.AddDate(0, 0, 30)),
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO analytics_erasure_requests
			(id, subject_type, subject_ref, requested_at, status, target_by, backup_cycle_until)
		VALUES ($1,$2,$3,$4,'pending',$5,$6)`,
		req.ID, subjectType, subjectRef, now, req.TargetBy, req.BackupCycleUntil)
	if err != nil {
		return req, err
	}

	completed, err := s.processErasure(ctx, req.ID, actorID)
	if err != nil {
		return req, err
	}
	return completed, nil
}

// processErasure runs the tombstone steps for one request.
func (s *Store) processErasure(ctx context.Context, requestID, actorID string) (ErasureRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ErasureRequest{}, err
	}
	defer tx.Rollback(ctx)

	var req ErasureRequest
	err = tx.QueryRow(ctx, `
		UPDATE analytics_erasure_requests
		SET status = 'in_progress'
		WHERE id = $1 AND status IN ('pending','failed')
		RETURNING id::text, subject_type, status, requested_at, target_by, backup_cycle_until`,
		requestID).Scan(&req.ID, &req.SubjectType, &req.Status, &req.RequestedAt,
		&req.TargetBy, &req.BackupCycleUntil)
	if err != nil {
		return ErasureRequest{}, err
	}

	var subjectRef string
	if err := tx.QueryRow(ctx,
		`SELECT subject_ref FROM analytics_erasure_requests WHERE id = $1`, requestID).Scan(&subjectRef); err != nil {
		return ErasureRequest{}, err
	}

	ids := make([]string, 0, 1)
	if req.SubjectType == "analytics_id" {
		ids = append(ids, subjectRef)
	} else {
		rows, err := tx.Query(ctx,
			`SELECT analytics_id::text FROM analytics_id_map WHERE user_id = $1`, subjectRef)
		if err != nil {
			return ErasureRequest{}, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return ErasureRequest{}, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return ErasureRequest{}, err
		}
	}

	for _, id := range ids {
		if _, err := tx.Exec(ctx, `SELECT analytics_tombstone($1)`, id); err != nil {
			return ErasureRequest{}, err
		}
		if actorID != "" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO analytics_identity_access_log (analytics_id, actor_id, reason)
				VALUES ($1, $2, 'admin analytics erasure')`, id, actorID); err != nil {
				return ErasureRequest{}, err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO analytics_erasure_events (request_id, step, status, detail)
			VALUES ($1, 'tombstone', 'ok', $2)`, requestID, id); err != nil {
			return ErasureRequest{}, err
		}
	}
	if req.SubjectType == "user" {
		if _, err := tx.Exec(ctx,
			`DELETE FROM analytics_id_map WHERE user_id = $1`, subjectRef); err != nil {
			return ErasureRequest{}, err
		}
	}

	if err := tx.QueryRow(ctx, `
		UPDATE analytics_erasure_requests
		SET status = 'completed', completed_at = now(), tombstone_at = now()
		WHERE id = $1
		RETURNING status`, requestID).Scan(&req.Status); err != nil {
		return ErasureRequest{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO analytics_erasure_events (request_id, step, status, detail)
		VALUES ($1, 'completed', 'ok', $2)`, requestID, fmt.Sprintf("%d identities", len(ids))); err != nil {
		return ErasureRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ErasureRequest{}, err
	}
	return req, nil
}

func ptrTime(t time.Time) *time.Time { return &t }
