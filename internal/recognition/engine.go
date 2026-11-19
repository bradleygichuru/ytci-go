package recognition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/contracts"
)

// ErrOccurrenceConflict means the same subject and occurrence key arrived with
// different semantics. That is a bug in the emitter, not a duplicate delivery.
var ErrOccurrenceConflict = errors.New("recognition: occurrence key reused with different event")

// ErrKilled means a kill switch suppressed the effect (never the event fact).
var ErrKilled = errors.New("recognition: disabled by kill switch")

// Engine owns the ledger, rules evaluation and outbox.
type Engine struct {
	pool *pgxpool.Pool
}

// New returns an engine bound to the shared pool.
func New(pool *pgxpool.Pool) *Engine {
	return &Engine{pool: pool}
}

// Pool exposes the underlying pool for tests and admin projections.
func (e *Engine) Pool() *pgxpool.Pool { return e.pool }

type querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Record appends one event and its outbox row in the caller's transaction.
// The caller supplies its own transaction so the source change and the
// recognition fact commit together. When tx is nil the engine opens one.
func (e *Engine) Record(ctx context.Context, ev Event) (RecordResult, error) {
	if e.pool == nil {
		return RecordResult{}, errors.New("recognition: nil pool")
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return RecordResult{}, err
	}
	defer tx.Rollback(ctx)

	res, err := e.RecordTx(ctx, tx, ev)
	if err != nil {
		return RecordResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RecordResult{}, err
	}
	return res, nil
}

// RecordTx is Record inside a caller-owned transaction.
func (e *Engine) RecordTx(ctx context.Context, tx pgx.Tx, ev Event) (RecordResult, error) {
	normalised, payload, err := normaliseEvent(ev)
	if err != nil {
		return RecordResult{}, err
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return RecordResult{}, err
	}

	// Occurrence identity. A repeat of the same fact is a no-op even when a
	// retry carries a fresh transport id.
	var existingID, existingType, existingSourceType, existingSourceID string
	err = tx.QueryRow(ctx, `
		SELECT id::text, type, data->>'source_type', data->>'source_id'
		FROM recognition.events
		WHERE subject = $1 AND occurrence_key = $2`,
		normalised.Subject, payload.OccurrenceKey,
	).Scan(&existingID, &existingType, &existingSourceType, &existingSourceID)
	if err == nil {
		if existingType != normalised.Type ||
			existingSourceType != payload.SourceType ||
			existingSourceID != payload.SourceID {
			return RecordResult{}, ErrOccurrenceConflict
		}
		return RecordResult{EventID: existingID, Duplicate: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RecordResult{}, err
	}

	var eventID string
	err = tx.QueryRow(ctx, `
		INSERT INTO recognition.events
			(id, specversion, source, type, subject, time, data, idempotency_key,
			 occurrence_key, sensitivity, privacy_class, age_band, program_scope,
			 consent_version, verification_method, verification_result, rule_key_hint)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::jsonb, $1, $8, $9, $10,
		        NULLIF($11,''), NULLIF($12,''), NULLIF($13,''), $14, $15, NULLIF($16,''))
		ON CONFLICT (source, id) DO NOTHING
		RETURNING id::text`,
		normalised.ID, normalised.SpecVersion, normalised.Source, normalised.Type,
		normalised.Subject, normalised.Time, raw, payload.OccurrenceKey,
		payload.Sensitivity, payload.PrivacyClass, payload.AgeBand,
		payload.ProgramScope, payload.ConsentVersion, payload.VerificationMethod,
		payload.VerificationResult, normalised.RuleKeyHint,
	).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Transport duplicate: the event exists, so its outbox row does too.
		if err := tx.QueryRow(ctx,
			`SELECT id::text FROM recognition.events WHERE source=$1 AND id=$2::uuid`,
			normalised.Source, normalised.ID,
		).Scan(&eventID); err != nil {
			return RecordResult{}, err
		}
		return RecordResult{EventID: eventID, Duplicate: true}, nil
	}
	if err != nil {
		if isUniqueViolation(err) {
			return RecordResult{}, ErrOccurrenceConflict
		}
		return RecordResult{}, err
	}

	var outboxID string
	err = tx.QueryRow(ctx, `
		INSERT INTO recognition.outbox (event_id, topic)
		VALUES ($1::uuid, 'rules.evaluate')
		ON CONFLICT (event_id, topic) DO NOTHING
		RETURNING id::text`, eventID).Scan(&outboxID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx,
			`SELECT id::text FROM recognition.outbox WHERE event_id=$1::uuid AND topic='rules.evaluate'`,
			eventID).Scan(&outboxID); err != nil {
			return RecordResult{}, err
		}
	}
	if err != nil {
		return RecordResult{}, err
	}
	return RecordResult{EventID: eventID, OutboxID: outboxID}, nil
}

func normaliseEvent(ev Event) (Event, Payload, error) {
	if ev.SpecVersion == "" {
		ev.SpecVersion = "1.0"
	}
	if ev.SpecVersion != "1.0" {
		return Event{}, Payload{}, fmt.Errorf("recognition: unsupported specversion %q", ev.SpecVersion)
	}
	if ev.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return Event{}, Payload{}, err
		}
		ev.ID = id.String()
	}
	if _, err := uuid.Parse(ev.ID); err != nil {
		return Event{}, Payload{}, fmt.Errorf("recognition: id must be a UUID: %w", err)
	}
	if ev.Source == "" || !strings.HasPrefix(ev.Source, "ke.") {
		return Event{}, Payload{}, fmt.Errorf("recognition: source %q must look like ke.<domain>.<module>", ev.Source)
	}
	if ev.Subject == "" {
		return Event{}, Payload{}, errors.New("recognition: subject is required")
	}
	if !knownEventType(ev.Type) {
		return Event{}, Payload{}, fmt.Errorf("recognition: unknown event type %q", ev.Type)
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	ev.Time = ev.Time.UTC()

	p := ev.Data
	if p.SourceType == "" || p.SourceID == "" {
		return Event{}, Payload{}, errors.New("recognition: source_type and source_id are required")
	}
	if p.OccurredAt == nil {
		t := ev.Time
		p.OccurredAt = &t
	}
	p.OccurredAt = timePtr(p.OccurredAt.UTC())
	if p.SourceRevision != nil && *p.SourceRevision < 0 {
		return Event{}, Payload{}, errors.New("recognition: source_revision must be zero or greater")
	}
	if len(p.SourceID) > 128 || len(p.OccurrenceKey) > 256 {
		return Event{}, Payload{}, errors.New("recognition: source_id or occurrence_key is too long")
	}
	if p.Sensitivity == "" {
		p.Sensitivity = "standard"
	}
	if p.PrivacyClass == "" {
		if p.Sensitivity == "standard" {
			p.PrivacyClass = "private"
		} else {
			p.PrivacyClass = "sensitive"
		}
	}
	if p.ProgramScope == "" {
		p.ProgramScope = "ytci"
	}

	ce, err := contracts.LoadCloudEvents()
	if err != nil {
		return Event{}, Payload{}, err
	}
	if err := validateOccurrence(ce, ev.Type, p.OccurrenceKey); err != nil {
		return Event{}, Payload{}, err
	}

	data := map[string]any{
		"source_type":         p.SourceType,
		"source_id":           p.SourceID,
		"occurrence_key":      p.OccurrenceKey,
		"verification_method": p.VerificationMethod,
		"verification_result": p.VerificationResult,
		"sensitivity":         p.Sensitivity,
		"privacy_class":       p.PrivacyClass,
		"program_scope":       p.ProgramScope,
		"occurred_at":         p.OccurredAt.Format(time.RFC3339Nano),
	}
	if p.AgeBand != "" {
		data["age_band"] = p.AgeBand
	}
	if p.ConsentVersion != "" {
		data["consent_version"] = p.ConsentVersion
	}
	if p.SourceRevision != nil {
		data["source_revision"] = *p.SourceRevision
	}
	if err := ce.ValidatePayload(data); err != nil {
		return Event{}, Payload{}, err
	}

	ev.Data = p
	return ev, p, nil
}

func validateOccurrence(ce *contracts.CloudEvents, eventType, key string) error {
	tmpl, ok := ce.OccurrenceKeys[eventType]
	if !ok {
		return fmt.Errorf("recognition: no occurrence template for %q", eventType)
	}
	if key == "" {
		return errors.New("recognition: occurrence_key is required")
	}
	if len(key) > 256 {
		return errors.New("recognition: occurrence_key is too long")
	}
	if i := strings.Index(tmpl, "{"); i >= 0 {
		prefix := tmpl[:i]
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			return fmt.Errorf("recognition: occurrence_key %q must start with %q", key, prefix)
		}
	}
	if strings.ContainsAny(key, "{}") {
		return fmt.Errorf("recognition: occurrence_key %q has unfilled placeholders", key)
	}
	return nil
}

func knownEventType(t string) bool {
	for _, v := range contracts.CloudEventTypes {
		if v == t {
			return true
		}
	}
	return false
}

// ProcessDue drains up to limit due outbox rows. Each row is claimed with
// FOR UPDATE SKIP LOCKED so several workers can run concurrently.
func (e *Engine) ProcessDue(ctx context.Context, workerID string, limit int) (ProcessStats, error) {
	if limit <= 0 {
		limit = 25
	}
	if workerID == "" {
		workerID = "recognition-worker"
	}
	stats := ProcessStats{}
	for i := 0; i < limit; i++ {
		ok, awards, err := e.processOne(ctx, workerID)
		if !ok {
			break
		}
		stats.Claimed++
		if err != nil {
			stats.Failed++
			continue
		}
		stats.Done++
		stats.Awards += awards
	}
	return stats, nil
}

func (e *Engine) processOne(ctx context.Context, workerID string) (bool, int, error) {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)

	var outboxID, eventID string
	err = tx.QueryRow(ctx, `
		SELECT id::text, event_id::text
		FROM recognition.outbox
		WHERE (status = 'pending' AND available_at <= now())
		   OR (status = 'processing' AND locked_at < now() - interval '5 minutes')
		ORDER BY available_at, created_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1`).Scan(&outboxID, &eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE recognition.outbox
		SET status='processing', attempts=attempts+1, locked_at=now(),
		    worker_id=$2, last_error=NULL
		WHERE id=$1::uuid`, outboxID, workerID); err != nil {
		return false, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, err
	}

	work, err := e.pool.Begin(ctx)
	if err != nil {
		return true, 0, err
	}
	defer work.Rollback(ctx)

	awards, evalErr := e.evaluateEvent(ctx, work, eventID)
	if evalErr != nil {
		if err := work.Rollback(ctx); err != nil {
			return true, 0, err
		}
		e.failOutbox(ctx, outboxID, evalErr)
		return true, 0, evalErr
	}
	if _, err := work.Exec(ctx, `
		UPDATE recognition.outbox
		SET status='done', processed_at=now(), locked_at=NULL, last_error=NULL
		WHERE id=$1::uuid`, outboxID); err != nil {
		return true, 0, err
	}
	if err := work.Commit(ctx); err != nil {
		return true, 0, err
	}
	return true, awards, nil
}

// failOutbox keeps retries bounded and leaves a dead row for operators.
func (e *Engine) failOutbox(ctx context.Context, outboxID string, cause error) {
	var attempts int
	if err := e.pool.QueryRow(ctx,
		`SELECT attempts FROM recognition.outbox WHERE id=$1::uuid`, outboxID).Scan(&attempts); err != nil {
		return
	}
	status := "pending"
	if attempts >= 5 {
		status = "dead"
	}
	backoff := Backoff(attempts)
	_, _ = e.pool.Exec(ctx, `
		UPDATE recognition.outbox
		SET status=$2, locked_at=NULL, last_error=$3,
		    available_at = CASE WHEN $2='dead' THEN available_at ELSE now() + $4::interval END
		WHERE id=$1::uuid`, outboxID, status, truncate(cause.Error(), 500), fmt.Sprintf("%d seconds", int(backoff.Seconds())))
}

// Backoff is exponential with a five-minute ceiling.
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := time.Duration(1<<uint(attempts)) * 5 * time.Second
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

func (e *Engine) evaluateEvent(ctx context.Context, tx pgx.Tx, eventID string) (int, error) {
	var ev Event
	var data []byte
	var ageBand, consentVersion, hint *string
	err := tx.QueryRow(ctx, `
		SELECT id::text, source, type, subject, time, data, age_band, consent_version,
		       rule_key_hint
		FROM recognition.events WHERE id=$1::uuid`, eventID,
	).Scan(&ev.ID, &ev.Source, &ev.Type, &ev.Subject, &ev.Time, &data, &ageBand, &consentVersion, &hint)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(data, &ev.Data); err != nil {
		return 0, fmt.Errorf("recognition: decode event payload: %w", err)
	}
	ev.Data.AgeBand = deref(ageBand)
	ev.Data.ConsentVersion = deref(consentVersion)
	ev.RuleKeyHint = deref(hint)

	if killed, err := e.killedForEvent(ctx, tx, ev.Type); err != nil {
		return 0, err
	} else if killed {
		return 0, nil
	}

	occurredAt := ev.Time
	if ev.Data.OccurredAt != nil {
		occurredAt = *ev.Data.OccurredAt
	}

	rules, err := e.matchRules(ctx, tx, ev, occurredAt)
	if err != nil {
		return 0, fmt.Errorf("matchRules: %w", err)
	}
	awards := 0
	for _, rule := range rules {
		awarded, err := e.applyRule(ctx, tx, ev, occurredAt, rule)
		if err != nil {
			return awards, fmt.Errorf("applyRule %s: %w", rule.Key, err)
		}
		if awarded {
			awards++
		}
	}
	// Deterministic mission progress stays current even when no rule awards
	// (self-declared destination progress) or an optional reward is selected.
	if err := updateProgress(ctx, tx, ev.Subject, ev.Data.SourceType); err != nil {
		return awards, fmt.Errorf("updateProgress: %w", err)
	}
	if err := updateLeaderboard(ctx, tx, ev); err != nil {
		return awards, fmt.Errorf("updateLeaderboard: %w", err)
	}
	return awards, nil
}

type ruleRow struct {
	Key      string
	Version  int
	XPAmount int
	BadgeKey *string
	Criteria map[string]any
}

func (e *Engine) matchRules(ctx context.Context, tx pgx.Tx, ev Event, occurredAt time.Time) ([]ruleRow, error) {
	if ev.Data.VerificationResult == "rejected" ||
		!contracts.Awardable(ev.Data.VerificationResult) {
		return nil, nil
	}
	if ev.RuleKeyHint != "" {
		r, ok, err := e.MatchRuleByKey(ctx, tx, ev.RuleKeyHint, occurredAt)
		if err != nil || !ok {
			return nil, err
		}
		return []ruleRow{r}, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT rule_key, version, xp_amount, badge_key, criteria
		FROM recognition.rules
		WHERE status='published'
		  AND source_type=$1
		  AND effective_from <= $2
		  AND (effective_to IS NULL OR effective_to > $2)
		  AND (criteria->>'event_type' IS NULL OR criteria->>'event_type'=$3)
		  AND COALESCE(criteria->>'variant','default')='default'
		ORDER BY version DESC
		LIMIT 1`,
		ev.Data.SourceType, occurredAt, ev.Type)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ruleRow, 0, 1)
	for rows.Next() {
		var r ruleRow
		var criteria []byte
		if err := rows.Scan(&r.Key, &r.Version, &r.XPAmount, &r.BadgeKey, &criteria); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(criteria, &r.Criteria)
		out = append(out, r)
	}
	return out, rows.Err()
}

// MatchRuleByKey is used by source adapters that know the rule variant (for
// example challenge difficulty or an optional heritage reward).
func (e *Engine) MatchRuleByKey(ctx context.Context, tx pgx.Tx, ruleKey string, occurredAt time.Time) (ruleRow, bool, error) {
	var r ruleRow
	var criteria []byte
	err := tx.QueryRow(ctx, `
		SELECT rule_key, version, xp_amount, badge_key, criteria
		FROM recognition.rules
		WHERE rule_key=$1 AND status='published'
		  AND effective_from <= $2 AND (effective_to IS NULL OR effective_to > $2)
		ORDER BY version DESC LIMIT 1`, ruleKey, occurredAt,
	).Scan(&r.Key, &r.Version, &r.XPAmount, &r.BadgeKey, &criteria)
	if errors.Is(err, pgx.ErrNoRows) {
		return ruleRow{}, false, nil
	}
	if err != nil {
		return ruleRow{}, false, err
	}
	_ = json.Unmarshal(criteria, &r.Criteria)
	return r, true, nil
}

func (e *Engine) applyRule(ctx context.Context, tx pgx.Tx, ev Event, occurredAt time.Time, rule ruleRow) (bool, error) {
	inserted := false
	if rule.XPAmount != 0 {
		capped, err := e.weeklyCapReached(ctx, tx, ev.Subject, ev.Data.SourceType, occurredAt, rule.Criteria)
		if err != nil {
			return false, err
		}
		if !capped {
			var entryID string
			err := tx.QueryRow(ctx, `
				INSERT INTO recognition.xp_entries
					(subject_id, amount, reason, rule_key, rule_version, source_type,
					 source_id, event_id, occurred_at)
				VALUES ($1,$2,'award',$3,$4,$5,$6,$7::uuid,$8)
				ON CONFLICT (subject_id, rule_key, source_type, source_id)
					WHERE reason='award'
				DO NOTHING
				RETURNING id::text`,
				ev.Subject, rule.XPAmount, rule.Key, rule.Version, ev.Data.SourceType,
				ev.Data.SourceID, ev.ID, occurredAt,
			).Scan(&entryID)
			if err == nil {
				inserted = true
				if err := updateXPTotal(ctx, tx, ev.Subject, rule.XPAmount); err != nil {
					return false, err
				}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return false, err
			}
		}
	}
	if rule.BadgeKey != nil && *rule.BadgeKey != "" {
		var awardID string
		err := tx.QueryRow(ctx, `
			INSERT INTO recognition.badge_awards
				(subject_id, badge_key, rule_key, rule_version, source_type,
				 source_id, event_id, privacy_class, awarded_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7::uuid,$8,$9)
			ON CONFLICT (subject_id, badge_key, source_type, source_id)
			DO NOTHING
			RETURNING id::text`,
			ev.Subject, *rule.BadgeKey, rule.Key, rule.Version, ev.Data.SourceType,
			ev.Data.SourceID, ev.ID, ev.Data.PrivacyClass, occurredAt,
		).Scan(&awardID)
		if err == nil {
			inserted = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}
	return inserted, nil
}

func (e *Engine) weeklyCapReached(ctx context.Context, tx pgx.Tx, subject, sourceType string, occurredAt time.Time, criteria map[string]any) (bool, error) {
	cap := intFromAny(criteria["weekly_cap"])
	if cap <= 0 {
		return false, nil
	}
	var count int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM recognition.xp_entries
		WHERE subject_id=$1 AND source_type=$2 AND reason='award'
		  AND occurred_at >= date_trunc('week', $3::timestamptz)
		  AND occurred_at < date_trunc('week', $3::timestamptz) + interval '7 days'`,
		subject, sourceType, occurredAt).Scan(&count)
	if err != nil {
		return false, err
	}
	return count >= cap, nil
}

func updateXPTotal(ctx context.Context, tx pgx.Tx, subject string, delta int) error {
	var total int64
	err := tx.QueryRow(ctx, `
		INSERT INTO recognition.xp_totals (subject_id, total_xp, level, highest_level, updated_at)
		VALUES ($1, $2, 0, 0, now())
		ON CONFLICT (subject_id) DO UPDATE
		SET total_xp = recognition.xp_totals.total_xp + EXCLUDED.total_xp,
		    updated_at = now()
		RETURNING total_xp`, subject, delta).Scan(&total)
	if err != nil {
		return err
	}
	var level int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(level),0) FROM recognition.levels WHERE threshold <= $1`,
		total).Scan(&level); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE recognition.xp_totals
		SET level = GREATEST(level, $2), highest_level = GREATEST(highest_level, $2),
		    updated_at = now()
		WHERE subject_id=$1`, subject, level)
	return err
}

func updateProgress(ctx context.Context, tx pgx.Tx, subject, sourceType string) error {
	rows, err := tx.Query(ctx, `
		SELECT track_key, target_value, criteria
		FROM recognition.progress_tracks
		WHERE status='published' AND criteria->>'source_type'=$1`, sourceType)
	if err != nil {
		return err
	}
	defer rows.Close()
	type track struct {
		key    string
		target int
	}
	var tracks []track
	for rows.Next() {
		var t track
		var criteria []byte
		if err := rows.Scan(&t.key, &t.target, &criteria); err != nil {
			return err
		}
		tracks = append(tracks, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range tracks {
		var current int
		if err := tx.QueryRow(ctx, `
			SELECT count(DISTINCT data->>'source_id')
			FROM recognition.events
			WHERE subject=$1 AND data->>'source_type'=$2
			  AND COALESCE(data->>'verification_result','') <> 'rejected'`,
			subject, sourceType).Scan(&current); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO recognition.progress_snapshots
				(subject_id, track_key, current_value, target_value, completed_at, updated_at)
			VALUES ($1::text,$2::text,$3::int,$4::int,
			        CASE WHEN $3::int >= $4::int THEN now() ELSE NULL END, now())
			ON CONFLICT (subject_id, track_key) DO UPDATE
			SET current_value=EXCLUDED.current_value,
			    target_value=EXCLUDED.target_value,
			    completed_at=COALESCE(recognition.progress_snapshots.completed_at, EXCLUDED.completed_at),
			    updated_at=now()`,
			subject, t.key, current, t.target); err != nil {
			return err
		}
	}
	return nil
}

func updateLeaderboard(ctx context.Context, tx pgx.Tx, ev Event) error {
	privacy := ev.Data.PrivacyClass
	if privacy == "" {
		privacy = "private"
	}
	_, err := tx.Exec(ctx, `
		UPDATE recognition.leaderboard_entries le
		SET level = COALESCE((SELECT t.level FROM recognition.xp_totals t WHERE t.subject_id=le.subject_id), le.level),
		    public_badge_count = (
		        SELECT count(*) FROM recognition.badge_awards ba
		        JOIN recognition.badge_definitions bd ON bd.badge_key=ba.badge_key
		        WHERE ba.subject_id=le.subject_id AND ba.revoked_at IS NULL
		          AND bd.public_eligible AND bd.privacy_class='public'
		    ),
		    verified_sites = (
		        SELECT count(DISTINCT e.data->>'source_id') FROM recognition.events e
		        WHERE e.subject=le.subject_id
		          AND e.type='ke.heritage.visit.verified.v1'
		          AND COALESCE(e.data->>'privacy_class','private') <> 'sensitive'
		          AND COALESCE(e.data->>'sensitivity','standard') = 'standard'
		    ),
		    trail_completions = (
		        SELECT count(DISTINCT e.data->>'source_id') FROM recognition.events e
		        WHERE e.subject=le.subject_id
		          AND e.type='ke.heritage.trail.completed.v1'
		          AND COALESCE(e.data->>'privacy_class','private') <> 'sensitive'
		    ),
		    last_completion_at = GREATEST(
		        COALESCE(le.last_completion_at, 'epoch'::timestamptz), $2::timestamptz),
		    updated_at = now()
		WHERE le.subject_id=$1
		  AND le.season_id IN (
		      SELECT id FROM recognition.seasons
		      WHERE $2::timestamptz >= starts_at AND $2::timestamptz <= ends_at
		        AND status IN ('active','frozen')
		  )`, ev.Subject, ev.Time)
	return err
}

// Reconcile compares the projected totals with SUM(amount). It alerts by
// returning drift; it never corrects automatically.
func (e *Engine) Reconcile(ctx context.Context) ([]Drift, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT t.subject_id, t.total_xp, COALESCE(SUM(x.amount),0)
		FROM recognition.xp_totals t
		LEFT JOIN recognition.xp_entries x ON x.subject_id=t.subject_id
		GROUP BY t.subject_id, t.total_xp
		HAVING t.total_xp <> COALESCE(SUM(x.amount),0)
		ORDER BY t.subject_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Drift, 0)
	for rows.Next() {
		var d Drift
		if err := rows.Scan(&d.SubjectID, &d.Projected, &d.Ledger); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE recognition.xp_totals SET reconciled_at=now()`); err != nil {
		return nil, err
	}
	return out, nil
}

// CapForTest exposes the weekly cap rule to tests without a database.
func CapForTest(criteria map[string]any) int { return intFromAny(criteria["weekly_cap"]) }

func timePtr(t time.Time) *time.Time { return &t }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func isUniqueViolation(err error) bool {
	type sqlState interface{ SQLState() string }
	var s sqlState
	return errors.As(err, &s) && s.SQLState() == "23505"
}

// killedForEvent applies global and per-source kill switches. The event fact
// is always stored; only the effect is suppressed.
func (e *Engine) killedForEvent(ctx context.Context, tx pgx.Tx, eventType string) (bool, error) {
	keys := []string{"global"}
	if strings.HasPrefix(eventType, "ke.event.") {
		keys = append(keys, "source:event")
	}
	if strings.HasPrefix(eventType, "ke.audio.") {
		keys = append(keys, "source:audio")
	}
	var enabled bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM recognition.kill_switches
		              WHERE key = ANY($1) AND enabled)`, keys).Scan(&enabled)
	return enabled, err
}

// SourceSwitchFor is the per-source switch key for an event type.
func SourceSwitchFor(eventType string) string {
	switch {
	case strings.HasPrefix(eventType, "ke.event."):
		return "source:event"
	case strings.HasPrefix(eventType, "ke.audio."):
		return "source:audio"
	}
	return ""
}
