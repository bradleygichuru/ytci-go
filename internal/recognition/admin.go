package recognition

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Admin errors map to frozen responses.
var (
	ErrSelfApproval   = errors.New("recognition: actor may not approve their own action")
	ErrNotSimulated   = errors.New("recognition: rule must be simulated before publish")
	ErrMissingNameSW  = errors.New("recognition: Swahili name is required before publish")
	ErrKillSwitchOn   = errors.New("recognition: global kill switch is enabled")
	ErrInvalidInput   = errors.New("recognition: invalid input")
	ErrAlreadyDecided = errors.New("recognition: case is already decided")
)

func writeAudit(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, actor, action, entityType, entityID string, before, after any, reason, requestID string) error {
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	_, err := q.Exec(ctx, `
		INSERT INTO recognition.audit
			(actor_id, action, entity_type, entity_id, before, after, reason, request_id)
		VALUES (NULLIF($1,''), $2, $3, NULLIF($4,''), NULLIF($5,'{}')::jsonb,
		        NULLIF($6,'{}')::jsonb, NULLIF($7,''), NULLIF($8,''))`,
		actor, action, entityType, entityID, string(b), string(a), reason, requestID)
	return err
}

func parseOffset(cursor string) int {
	n, err := strconv.Atoi(cursor)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// ---------------------------------------------------------------------------
// Rules
// ---------------------------------------------------------------------------

func (e *Engine) ListRules(ctx context.Context, status, cursor string, limit int) ([]RecognitionRule, bool, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := parseOffset(cursor)
	rows, err := e.pool.Query(ctx, `
		SELECT id::text, rule_key, version, source_type, status, effective_from,
		       effective_to, criteria, xp_amount, badge_key, created_by, published_by
		FROM recognition.rules
		WHERE ($1='' OR status=$1)
		ORDER BY created_at DESC, id
		LIMIT $2 OFFSET $3`, status, limit+1, offset)
	if err != nil {
		return nil, false, "", err
	}
	defer rows.Close()
	out := make([]RecognitionRule, 0, limit)
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, false, "", err
		}
		out = append(out, r)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	next := ""
	if hasMore {
		next = strconv.Itoa(offset + limit)
	}
	return out, hasMore, next, rows.Err()
}

func scanRule(rows pgx.Rows) (RecognitionRule, error) {
	var r RecognitionRule
	var criteria []byte
	var effectiveTo pgtype.Timestamptz
	var badgeKey, createdBy, publishedBy pgtype.Text
	err := rows.Scan(&r.ID, &r.RuleKey, &r.Version, &r.SourceType, &r.Status,
		&r.EffectiveFrom, &effectiveTo, &criteria, &r.XPAmount, &badgeKey,
		&createdBy, &publishedBy)
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal(criteria, &r.Criteria)
	if effectiveTo.Valid {
		t := effectiveTo.Time
		r.EffectiveTo = &t
	}
	if badgeKey.Valid {
		r.BadgeKey = &badgeKey.String
	}
	if createdBy.Valid {
		r.CreatedBy = &createdBy.String
	}
	if publishedBy.Valid {
		r.PublishedBy = &publishedBy.String
	}
	return r, nil
}

func (e *Engine) CreateRule(ctx context.Context, actor, ruleKey, sourceType string, effectiveFrom *time.Time, criteria map[string]any, xpAmount int, badgeKey *string) (RecognitionRule, error) {
	if ruleKey == "" || sourceType == "" || xpAmount < 0 {
		return RecognitionRule{}, ErrInvalidInput
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return RecognitionRule{}, err
	}
	defer tx.Rollback(ctx)
	var version int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version),0)+1 FROM recognition.rules WHERE rule_key=$1`,
		ruleKey).Scan(&version); err != nil {
		return RecognitionRule{}, err
	}
	if effectiveFrom == nil {
		t := time.Now().UTC()
		effectiveFrom = &t
	}
	if criteria == nil {
		criteria = map[string]any{}
	}
	raw, _ := json.Marshal(criteria)
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO recognition.rules
			(rule_key, version, source_type, status, effective_from, criteria, xp_amount,
			 badge_key, created_by)
		VALUES ($1,$2,$3,'draft',$4,$5::jsonb,$6,$7,NULLIF($8,''))
		RETURNING id::text`,
		ruleKey, version, sourceType, *effectiveFrom, raw, xpAmount, badgeKey, actor).Scan(&id)
	if err != nil {
		return RecognitionRule{}, err
	}
	if err := writeAudit(ctx, tx, actor, "rule.create", "rule", id, nil, criteria, "", ""); err != nil {
		return RecognitionRule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RecognitionRule{}, err
	}
	return e.getRule(ctx, id)
}

func (e *Engine) getRule(ctx context.Context, id string) (RecognitionRule, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT id::text, rule_key, version, source_type, status, effective_from,
		       effective_to, criteria, xp_amount, badge_key, created_by, published_by
		FROM recognition.rules WHERE id=$1::uuid`, id)
	if err != nil {
		return RecognitionRule{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return RecognitionRule{}, pgx.ErrNoRows
	}
	return scanRule(rows)
}

func (e *Engine) PublishRule(ctx context.Context, actor, id, requestID string) (RecognitionRule, error) {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return RecognitionRule{}, err
	}
	defer tx.Rollback(ctx)
	var createdBy *string
	var simulatedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx, `
		SELECT created_by, simulated_at FROM recognition.rules
		WHERE id=$1::uuid FOR UPDATE`, id).Scan(&createdBy, &simulatedAt)
	if err != nil {
		return RecognitionRule{}, err
	}
	if !simulatedAt.Valid {
		return RecognitionRule{}, ErrNotSimulated
	}
	if createdBy != nil && *createdBy == actor {
		return RecognitionRule{}, ErrSelfApproval
	}
	if _, err := tx.Exec(ctx, `
		UPDATE recognition.rules
		SET status='published', published_by=NULLIF($2,''), published_at=now()
		WHERE id=$1::uuid`, id, actor); err != nil {
		return RecognitionRule{}, err
	}
	if err := writeAudit(ctx, tx, actor, "rule.publish", "rule", id, nil, nil, "", requestID); err != nil {
		return RecognitionRule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RecognitionRule{}, err
	}
	return e.getRule(ctx, id)
}

func (e *Engine) SimulateRule(ctx context.Context, actor, id string, from, to *time.Time) (RuleSimulation, error) {
	var ruleKey, sourceType string
	var version, xp int
	var criteria []byte
	err := e.pool.QueryRow(ctx, `
		SELECT rule_key, version, source_type, xp_amount, criteria
		FROM recognition.rules WHERE id=$1::uuid`, id,
	).Scan(&ruleKey, &version, &sourceType, &xp, &criteria)
	if err != nil {
		return RuleSimulation{}, err
	}
	if from == nil {
		t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		from = &t
	}
	if to == nil {
		t := time.Now().UTC()
		to = &t
	}
	var eval, awardable int
	var sample []string
	err = e.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE x.id IS NULL),
		       COALESCE(array_agg(DISTINCT e.subject) FILTER (WHERE x.id IS NULL), '{}')
		FROM recognition.events e
		LEFT JOIN recognition.xp_entries x
		  ON x.subject_id=e.subject AND x.rule_key=$1 AND x.source_type=e.data->>'source_type'
		 AND x.source_id=e.data->>'source_id' AND x.reason='award'
		WHERE e.data->>'source_type'=$2
		  AND e.time BETWEEN $3 AND $4
		  AND (e.data->>'verification_result') IN ('verified','attested','moderated','server_recorded')`,
		ruleKey, sourceType, *from, *to).Scan(&eval, &awardable, &sample)
	if err != nil {
		return RuleSimulation{}, err
	}
	if len(sample) > 10 {
		sample = sample[:10]
	}
	if _, err := e.pool.Exec(ctx, `
		UPDATE recognition.rules SET simulated_at=now(), simulated_by=NULLIF($2,'')
		WHERE id=$1::uuid`, id, actor); err != nil {
		return RuleSimulation{}, err
	}
	note := "Simulation only. Rules are immutable; publishing cannot mint a second award for an existing occurrence."
	return RuleSimulation{
		RuleKey: ruleKey, Version: version, Evaluated: eval, WouldAward: awardable,
		WouldDeduct: 0, SampleSubjects: sample, Notes: &note,
	}, nil
}

// ---------------------------------------------------------------------------
// Badges
// ---------------------------------------------------------------------------

func (e *Engine) AdminBadges(ctx context.Context) ([]BadgeDefinition, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT badge_key, name_en, name_sw, family, criteria, icon_key, colour_token,
		       privacy_class, hidden, public_eligible, status, version
		FROM recognition.badge_definitions ORDER BY family, badge_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BadgeDefinition, 0)
	for rows.Next() {
		var b BadgeDefinition
		var nameSw, icon, colour pgtype.Text
		var criteria []byte
		if err := rows.Scan(&b.BadgeKey, &b.NameEn, &nameSw, &b.Family, &criteria,
			&icon, &colour, &b.PrivacyClass, &b.Hidden, &b.PublicEligible,
			&b.Status, &b.Version); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(criteria, &b.Criteria)
		if nameSw.Valid {
			b.NameSw = &nameSw.String
		}
		if icon.Valid {
			b.IconKey = &icon.String
		}
		if colour.Valid {
			b.ColourToken = &colour.String
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (e *Engine) CreateBadge(ctx context.Context, actor string, in BadgeDefinition) (BadgeDefinition, error) {
	if in.BadgeKey == "" || in.NameEn == "" || in.Family == "" {
		return BadgeDefinition{}, ErrInvalidInput
	}
	if in.PrivacyClass == "" {
		in.PrivacyClass = "private"
	}
	if in.Criteria == nil {
		in.Criteria = map[string]any{}
	}
	raw, _ := json.Marshal(in.Criteria)
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return BadgeDefinition{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO recognition.badge_definitions
			(badge_key, name_en, name_sw, family, criteria, icon_key, colour_token,
			 privacy_class, hidden, public_eligible, status, version, created_by)
		VALUES ($1,$2,NULLIF($3,''),$4,$5::jsonb,NULLIF($6,''),NULLIF($7,''),
		        $8,$9,$10,'draft',1,NULLIF($11,''))`,
		in.BadgeKey, in.NameEn, deref(in.NameSw), in.Family, raw, deref(in.IconKey),
		deref(in.ColourToken), in.PrivacyClass, in.Hidden, in.PublicEligible, actor)
	if err != nil {
		return BadgeDefinition{}, err
	}
	if err := writeAudit(ctx, tx, actor, "badge.create", "badge", in.BadgeKey, nil, in, "", ""); err != nil {
		return BadgeDefinition{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BadgeDefinition{}, err
	}
	return e.getBadgeDefinition(ctx, in.BadgeKey)
}

func (e *Engine) getBadgeDefinition(ctx context.Context, key string) (BadgeDefinition, error) {
	var b BadgeDefinition
	var nameSw, icon, colour pgtype.Text
	var criteria []byte
	err := e.pool.QueryRow(ctx, `
		SELECT badge_key, name_en, name_sw, family, criteria, icon_key, colour_token,
		       privacy_class, hidden, public_eligible, status, version
		FROM recognition.badge_definitions WHERE badge_key=$1`, key,
	).Scan(&b.BadgeKey, &b.NameEn, &nameSw, &b.Family, &criteria, &icon, &colour,
		&b.PrivacyClass, &b.Hidden, &b.PublicEligible, &b.Status, &b.Version)
	if err != nil {
		return b, err
	}
	_ = json.Unmarshal(criteria, &b.Criteria)
	if nameSw.Valid {
		b.NameSw = &nameSw.String
	}
	if icon.Valid {
		b.IconKey = &icon.String
	}
	if colour.Valid {
		b.ColourToken = &colour.String
	}
	return b, nil
}

func (e *Engine) PublishBadge(ctx context.Context, actor, key, requestID string) (BadgeDefinition, error) {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return BadgeDefinition{}, err
	}
	defer tx.Rollback(ctx)
	var createdBy *string
	var nameSW pgtype.Text
	err = tx.QueryRow(ctx, `
		SELECT created_by, name_sw FROM recognition.badge_definitions
		WHERE badge_key=$1 FOR UPDATE`, key).Scan(&createdBy, &nameSW)
	if err != nil {
		return BadgeDefinition{}, err
	}
	if !nameSW.Valid || nameSW.String == "" {
		return BadgeDefinition{}, ErrMissingNameSW
	}
	if createdBy != nil && *createdBy == actor {
		return BadgeDefinition{}, ErrSelfApproval
	}
	if _, err := tx.Exec(ctx, `
		UPDATE recognition.badge_definitions
		SET status='published', published_by=NULLIF($2,''), updated_at=now()
		WHERE badge_key=$1`, key, actor); err != nil {
		return BadgeDefinition{}, err
	}
	if err := writeAudit(ctx, tx, actor, "badge.publish", "badge", key, nil, nil, "", requestID); err != nil {
		return BadgeDefinition{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BadgeDefinition{}, err
	}
	return e.getBadgeDefinition(ctx, key)
}

// ---------------------------------------------------------------------------
// Seasons
// ---------------------------------------------------------------------------

func (e *Engine) AdminSeasons(ctx context.Context) ([]Season, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT season_key, name_en, name_sw, starts_at, ends_at, status, badge_key,
		       xp_award, mission_families
		FROM recognition.seasons ORDER BY starts_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Season, 0)
	for rows.Next() {
		var s Season
		var nameSw, badge pgtype.Text
		var families []string
		if err := rows.Scan(&s.Key, &s.NameEn, &nameSw, &s.StartsAt, &s.EndsAt,
			&s.Status, &badge, &s.XPAward, &families); err != nil {
			return nil, err
		}
		if nameSw.Valid {
			s.NameSw = &nameSw.String
		}
		if badge.Valid {
			s.BadgeKey = &badge.String
		}
		if families == nil {
			families = []string{}
		}
		s.MissionFamilies = families
		out = append(out, s)
	}
	return out, rows.Err()
}

func (e *Engine) CreateSeason(ctx context.Context, actor, key, nameEn, nameSw string, startsAt, endsAt time.Time, badgeKey *string, xpAward int, families []string) (Season, error) {
	if key == "" || nameEn == "" || !endsAt.After(startsAt) {
		return Season{}, ErrInvalidInput
	}
	if xpAward < 0 {
		return Season{}, ErrInvalidInput
	}
	if families == nil {
		families = []string{}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Season{}, err
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return Season{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO recognition.seasons
			(id, season_key, name_en, name_sw, starts_at, ends_at, status, badge_key,
			 xp_award, mission_families, created_by)
		VALUES ($1::uuid,$2,$3,NULLIF($4,''),$5,$6,'upcoming',$7,$8,$9,NULLIF($10,''))`,
		id.String(), key, nameEn, nameSw, startsAt, endsAt, badgeKey, xpAward, families, actor)
	if err != nil {
		return Season{}, err
	}
	if err := writeAudit(ctx, tx, actor, "season.create", "season", id.String(), nil, nameEn, "", ""); err != nil {
		return Season{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Season{}, err
	}
	return e.Season(ctx, key)
}

func (e *Engine) SeasonByID(ctx context.Context, id string) (Season, error) {
	var key string
	err := e.pool.QueryRow(ctx,
		`SELECT season_key FROM recognition.seasons WHERE id=$1::uuid`, id).Scan(&key)
	if err != nil {
		return Season{}, err
	}
	return e.Season(ctx, key)
}

func (e *Engine) SetSeasonStatus(ctx context.Context, actor, id, status, action, requestID string) (Season, error) {
	allowed := map[string]bool{"published": true, "frozen": true, "closed": true}
	if !allowed[status] {
		return Season{}, ErrInvalidInput
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return Season{}, err
	}
	defer tx.Rollback(ctx)
	var key string
	var startsAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT season_key, starts_at FROM recognition.seasons WHERE id=$1::uuid FOR UPDATE`, id,
	).Scan(&key, &startsAt)
	if err != nil {
		return Season{}, err
	}
	newStatus := status
	if status == "published" {
		newStatus = "upcoming"
		if !startsAt.After(time.Now()) {
			newStatus = "active"
		}
		if _, err := tx.Exec(ctx,
			`UPDATE recognition.seasons SET status=$2, published_at=now() WHERE id=$1::uuid`,
			id, newStatus); err != nil {
			return Season{}, err
		}
	} else if status == "frozen" {
		if _, err := tx.Exec(ctx,
			`UPDATE recognition.seasons SET status='frozen', frozen_at=now() WHERE id=$1::uuid`,
			id); err != nil {
			return Season{}, err
		}
	} else {
		if _, err := tx.Exec(ctx,
			`UPDATE recognition.seasons SET status='closed', closed_at=now() WHERE id=$1::uuid`,
			id); err != nil {
			return Season{}, err
		}
	}
	if err := writeAudit(ctx, tx, actor, action, "season", id, nil, map[string]string{"status": newStatus}, "", requestID); err != nil {
		return Season{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Season{}, err
	}
	if status == "closed" {
		if err := e.snapshotSeason(ctx, key, id); err != nil {
			return Season{}, err
		}
	}
	return e.Season(ctx, key)
}

// snapshotSeason writes the retained 12-month board payload.
func (e *Engine) snapshotSeason(ctx context.Context, seasonKey, seasonID string) error {
	board, err := e.boardQuery(ctx, seasonKey, seasonID, nil, "")
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(board)
	_, err = e.pool.Exec(ctx, `
		INSERT INTO recognition.leaderboard_snapshots
			(season_id, county_gid, entry_count, payload)
		VALUES ($1::uuid, NULL, $2, $3::jsonb)`, seasonID, len(board.Entries), payload)
	return err
}

// ---------------------------------------------------------------------------
// Moderation
// ---------------------------------------------------------------------------

func (e *Engine) ModerationCases(ctx context.Context, status, cursor string, limit int) ([]ModerationCase, bool, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := parseOffset(cursor)
	rows, err := e.pool.Query(ctx, `
		SELECT id::text, kind, subject_id, reason, status, decision, decided_at,
		       appeal_deadline, created_at
		FROM recognition.moderation_cases
		WHERE ($1='' OR status=$1)
		ORDER BY created_at DESC, id
		LIMIT $2 OFFSET $3`, status, limit+1, offset)
	if err != nil {
		return nil, false, "", err
	}
	defer rows.Close()
	out := make([]ModerationCase, 0, limit)
	for rows.Next() {
		var c ModerationCase
		var subject, decision pgtype.Text
		var decidedAt, appeal pgtype.Timestamptz
		if err := rows.Scan(&c.ID, &c.Kind, &subject, &c.Reason, &c.Status,
			&decision, &decidedAt, &appeal, &c.CreatedAt); err != nil {
			return nil, false, "", err
		}
		if subject.Valid {
			c.SubjectRef = &subject.String
		}
		if decision.Valid {
			c.Decision = &decision.String
		}
		if decidedAt.Valid {
			t := decidedAt.Time
			c.DecidedAt = &t
		}
		if appeal.Valid {
			t := appeal.Time
			c.AppealDeadline = &t
		}
		out = append(out, c)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	next := ""
	if hasMore {
		next = strconv.Itoa(offset + limit)
	}
	return out, hasMore, next, rows.Err()
}

func (e *Engine) DecideModerationCase(ctx context.Context, actor, id, decision, reason, requestID string) (ModerationCase, error) {
	valid := map[string]bool{"warn": true, "disqualify": true, "rename": true, "dismiss": true, "revoke": true}
	if !valid[decision] || len(reason) < 10 {
		return ModerationCase{}, ErrInvalidInput
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return ModerationCase{}, err
	}
	defer tx.Rollback(ctx)
	var kind, status string
	var subject pgtype.Text
	err = tx.QueryRow(ctx, `
		SELECT kind, status, subject_id FROM recognition.moderation_cases
		WHERE id=$1::uuid FOR UPDATE`, id).Scan(&kind, &status, &subject)
	if err != nil {
		return ModerationCase{}, err
	}
	if status == "decided" || status == "closed" {
		return ModerationCase{}, ErrAlreadyDecided
	}
	var appeal pgtype.Timestamptz
	if decision == "disqualify" || decision == "revoke" {
		appeal = pgtype.Timestamptz{Time: time.Now().Add(14 * 24 * time.Hour), Valid: true}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE recognition.moderation_cases
		SET status='decided', decision=$2, decided_by=NULLIF($3,''), decided_at=now(),
		    appeal_deadline=$4, updated_at=now()
		WHERE id=$1::uuid`, id, decision, actor, appeal); err != nil {
		return ModerationCase{}, err
	}

	if subject.Valid {
		switch decision {
		case "disqualify":
			if _, err := tx.Exec(ctx, `
				INSERT INTO recognition.disqualifications
					(season_id, subject_id, reason, evidence, case_id, decided_by, appeal_deadline)
				VALUES (NULL,$1,$2,jsonb_build_object('case', $3::text),$3::uuid,NULLIF($4,''),$5)`,
				subject.String, reason, id, actor, appeal); err != nil {
				return ModerationCase{}, err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE recognition.leaderboard_entries
				SET disqualified=true, suppressed=true, updated_at=now()
				WHERE subject_id=$1`, subject.String); err != nil {
				return ModerationCase{}, err
			}
		case "revoke":
			if _, err := tx.Exec(ctx, `
				INSERT INTO recognition.retractions
					(subject_id, entity_type, entity_id, reason, decided_by)
				VALUES ($1,'badge_award',$2,$3,NULLIF($4,''))`,
				subject.String, id, reason, actor); err != nil {
				return ModerationCase{}, err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE recognition.badge_awards SET revoked_at=now(), revoke_reason=$2
				WHERE subject_id=$1 AND revoked_at IS NULL`, subject.String, reason); err != nil {
				return ModerationCase{}, err
			}
		case "rename":
			if _, err := tx.Exec(ctx, `
				UPDATE recognition.display_names
				SET status='rejected', moderation_reason=$2, moderated_by=NULLIF($3,''),
				    display_name=COALESCE(previous_approved, 'Reader ' || left(md5(subject_id),6)),
				    changed_at=now()
				WHERE subject_id=$1`, subject.String, reason, actor); err != nil {
				return ModerationCase{}, err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE recognition.leaderboard_entries le
				SET display_name=COALESCE(dn.previous_approved, 'Reader ' || left(md5(le.subject_id),6)),
				    updated_at=now()
				FROM recognition.display_names dn
				WHERE le.subject_id=$1 AND dn.subject_id=$1`, subject.String); err != nil {
				return ModerationCase{}, err
			}
		}
	}
	if err := writeAudit(ctx, tx, actor, "moderation.decision", "moderation_case", id, nil,
		map[string]string{"decision": decision, "reason": reason}, reason, requestID); err != nil {
		return ModerationCase{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModerationCase{}, err
	}
	return e.getModerationCase(ctx, id)
}

func (e *Engine) getModerationCase(ctx context.Context, id string) (ModerationCase, error) {
	var c ModerationCase
	var subject, decision pgtype.Text
	var decidedAt, appeal pgtype.Timestamptz
	err := e.pool.QueryRow(ctx, `
		SELECT id::text, kind, subject_id, reason, status, decision, decided_at,
		       appeal_deadline, created_at
		FROM recognition.moderation_cases WHERE id=$1::uuid`, id,
	).Scan(&c.ID, &c.Kind, &subject, &c.Reason, &c.Status, &decision, &decidedAt,
		&appeal, &c.CreatedAt)
	if err != nil {
		return c, err
	}
	if subject.Valid {
		c.SubjectRef = &subject.String
	}
	if decision.Valid {
		c.Decision = &decision.String
	}
	if decidedAt.Valid {
		t := decidedAt.Time
		c.DecidedAt = &t
	}
	if appeal.Valid {
		t := appeal.Time
		c.AppealDeadline = &t
	}
	return c, nil
}

// ApproveDisplayName is used by the name moderation decision; it keeps the
// previous approved value for a future reject.
func (e *Engine) ApproveDisplayName(ctx context.Context, actor, subject, name string) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE recognition.display_names
		SET status='approved', moderated_by=NULLIF($2,''), changed_at=now(),
		    moderation_reason=NULL
		WHERE subject_id=$1`, subject, actor); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE recognition.leaderboard_entries SET display_name=$2, updated_at=now()
		WHERE subject_id=$1`, subject, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Retract removes a public signal immediately and keeps private progress.
func (e *Engine) Retract(ctx context.Context, actor, subject, entityType, entityID, reason, requestID string) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO recognition.retractions
			(subject_id, entity_type, entity_id, reason, decided_by)
		VALUES ($1,$2,$3,$4,NULLIF($5,''))`,
		subject, entityType, entityID, reason, actor); err != nil {
		return err
	}
	if entityType == "leaderboard_entry" {
		if _, err := tx.Exec(ctx, `
			UPDATE recognition.leaderboard_entries
			SET suppressed=true, updated_at=now()
			WHERE subject_id=$1`, subject); err != nil {
			return err
		}
	}
	if entityType == "badge_award" {
		if _, err := tx.Exec(ctx, `
			UPDATE recognition.badge_awards SET revoked_at=now(), revoke_reason=$2
			WHERE id=$1::uuid`, entityID, reason); err != nil {
			return err
		}
	}
	if err := writeAudit(ctx, tx, actor, "retraction.create", entityType, entityID, nil,
		map[string]string{"reason": reason}, reason, requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Backfill
// ---------------------------------------------------------------------------

func (e *Engine) ListBackfill(ctx context.Context) ([]BackfillBatch, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT id::text, rule_key, rule_version, status, processed, awarded,
		       dry_run_diff, created_by, approved_by, created_at
		FROM recognition.backfill_batches ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BackfillBatch, 0)
	for rows.Next() {
		b, err := scanBackfill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanBackfill(rows pgx.Rows) (BackfillBatch, error) {
	var b BackfillBatch
	var diff []byte
	var approvedBy pgtype.Text
	if err := rows.Scan(&b.ID, &b.RuleKey, &b.RuleVersion, &b.Status, &b.Processed,
		&b.Awarded, &diff, &b.CreatedBy, &approvedBy, &b.CreatedAt); err != nil {
		return b, err
	}
	if len(diff) > 0 && string(diff) != "null" {
		var d BackfillDiff
		if json.Unmarshal(diff, &d) == nil {
			b.Diff = &d
		}
	}
	if approvedBy.Valid {
		b.ApprovedBy = &approvedBy.String
	}
	return b, nil
}

func (e *Engine) CreateBackfill(ctx context.Context, actor, ruleKey string, ruleVersion int, from, to time.Time) (BackfillBatch, error) {
	if ruleKey == "" || ruleVersion < 1 || !to.After(from) {
		return BackfillBatch{}, ErrInvalidInput
	}
	var sourceType string
	var effectiveFrom time.Time
	var effectiveTo pgtype.Timestamptz
	err := e.pool.QueryRow(ctx, `
		SELECT source_type, effective_from, effective_to FROM recognition.rules
		WHERE rule_key=$1 AND version=$2`, ruleKey, ruleVersion,
	).Scan(&sourceType, &effectiveFrom, &effectiveTo)
	if err != nil {
		return BackfillBatch{}, err
	}
	// Never grant XP for a policy that did not exist when the reader acted.
	windowStart := from
	if effectiveFrom.After(windowStart) {
		windowStart = effectiveFrom
	}
	windowEnd := to
	if effectiveTo.Valid && effectiveTo.Time.Before(windowEnd) {
		windowEnd = effectiveTo.Time
	}
	var evaluated, wouldAward, wouldSkip, conflicts int
	if windowEnd.After(windowStart) {
		err = e.pool.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE x.id IS NULL),
			       count(*) FILTER (WHERE x.id IS NOT NULL)
			FROM recognition.events e
			LEFT JOIN recognition.xp_entries x
			  ON x.subject_id=e.subject AND x.rule_key=$1
			 AND x.source_type=e.data->>'source_type' AND x.source_id=e.data->>'source_id'
			 AND x.reason='award'
			WHERE e.data->>'source_type'=$2 AND e.time BETWEEN $3 AND $4
			  AND (e.data->>'verification_result') IN ('verified','attested','moderated','server_recorded')`,
			ruleKey, sourceType, windowStart, windowEnd,
		).Scan(&evaluated, &wouldAward, &wouldSkip)
		if err != nil {
			return BackfillBatch{}, err
		}
		err = e.pool.QueryRow(ctx, `
			SELECT count(*) FROM (
			  SELECT subject, occurrence_key, count(*)
			  FROM recognition.events
			  WHERE data->>'source_type'=$1 AND time BETWEEN $2 AND $3
			  GROUP BY subject, occurrence_key HAVING count(*) > 1
			) dup`, sourceType, windowStart, windowEnd).Scan(&conflicts)
		if err != nil {
			return BackfillBatch{}, err
		}
	}
	diff, _ := json.Marshal(BackfillDiff{WouldAward: wouldAward, WouldSkip: wouldSkip, Conflicts: conflicts})
	filter, _ := json.Marshal(map[string]string{"from": from.UTC().Format(time.RFC3339), "to": to.UTC().Format(time.RFC3339)})
	var id string
	err = e.pool.QueryRow(ctx, `
		INSERT INTO recognition.backfill_batches
			(rule_key, rule_version, filter, status, dry_run_diff, created_by)
		VALUES ($1,$2,$3::jsonb,'dry_run',$4::jsonb,NULLIF($5,''))
		RETURNING id::text`, ruleKey, ruleVersion, filter, diff, actor).Scan(&id)
	if err != nil {
		return BackfillBatch{}, err
	}
	_ = evaluated
	return e.getBackfill(ctx, id)
}

func (e *Engine) getBackfill(ctx context.Context, id string) (BackfillBatch, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT id::text, rule_key, rule_version, status, processed, awarded,
		       dry_run_diff, created_by, approved_by, created_at
		FROM recognition.backfill_batches WHERE id=$1::uuid`, id)
	if err != nil {
		return BackfillBatch{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return BackfillBatch{}, pgx.ErrNoRows
	}
	return scanBackfill(rows)
}

// ApproveBackfill requires a second actor, then runs in bounded batches.
func (e *Engine) ApproveBackfill(ctx context.Context, actor, id, requestID string) (BackfillBatch, error) {
	killed, err := e.switchEnabled(ctx, "global")
	if err != nil {
		return BackfillBatch{}, err
	}
	if killed {
		return BackfillBatch{}, ErrKillSwitchOn
	}
	var createdBy string
	var status, ruleKey string
	var ruleVersion int
	var filter []byte
	err = e.pool.QueryRow(ctx, `
		SELECT created_by, status, rule_key, rule_version, filter
		FROM recognition.backfill_batches WHERE id=$1::uuid`, id,
	).Scan(&createdBy, &status, &ruleKey, &ruleVersion, &filter)
	if err != nil {
		return BackfillBatch{}, err
	}
	if status != "dry_run" {
		return BackfillBatch{}, ErrAlreadyDecided
	}
	if createdBy == actor {
		return BackfillBatch{}, ErrSelfApproval
	}
	var window map[string]string
	_ = json.Unmarshal(filter, &window)
	from, _ := time.Parse(time.RFC3339, window["from"])
	to, _ := time.Parse(time.RFC3339, window["to"])

	if _, err := e.pool.Exec(ctx, `
		UPDATE recognition.backfill_batches
		SET status='running', approved_by=NULLIF($2,''), started_at=now()
		WHERE id=$1::uuid`, id, actor); err != nil {
		return BackfillBatch{}, err
	}
	logAudit(ctx, e.pool, actor, "backfill.approve", "backfill", id, nil, nil, "", requestID)

	processed, awarded, runErr := e.runBackfill(ctx, ruleKey, ruleVersion, from, to)
	finalStatus := "completed"
	if runErr != nil {
		finalStatus = "failed"
	}
	if _, err := e.pool.Exec(ctx, `
		UPDATE recognition.backfill_batches
		SET status=$2, processed=$3, awarded=$4, finished_at=now()
		WHERE id=$1::uuid`, id, finalStatus, processed, awarded); err != nil {
		return BackfillBatch{}, err
	}
	return e.getBackfill(ctx, id)
}

func (e *Engine) runBackfill(ctx context.Context, ruleKey string, ruleVersion int, from, to time.Time) (int, int, error) {
	var sourceType string
	var xpAmount int
	var badgeKey *string
	var effectiveFrom time.Time
	var effectiveTo pgtype.Timestamptz
	err := e.pool.QueryRow(ctx, `
		SELECT source_type, xp_amount, badge_key, effective_from, effective_to
		FROM recognition.rules WHERE rule_key=$1 AND version=$2`,
		ruleKey, ruleVersion).Scan(&sourceType, &xpAmount, &badgeKey, &effectiveFrom, &effectiveTo)
	if err != nil {
		return 0, 0, err
	}
	if effectiveFrom.After(from) {
		from = effectiveFrom
	}
	if effectiveTo.Valid && effectiveTo.Time.Before(to) {
		to = effectiveTo.Time
	}

	processed, awarded := 0, 0
	lastID := "00000000-0000-0000-0000-000000000000"
	for {
		rows, err := e.pool.Query(ctx, `
			SELECT id::text, subject, type, time, data
			FROM recognition.events
			WHERE id > $1::uuid AND data->>'source_type'=$2 AND time BETWEEN $3 AND $4
			  AND (data->>'verification_result') IN ('verified','attested','moderated','server_recorded')
			ORDER BY id LIMIT 100`, lastID, sourceType, from, to)
		if err != nil {
			return processed, awarded, err
		}
		type evRow struct {
			id, subject, typ string
			at               time.Time
			data             Payload
		}
		batch := make([]evRow, 0, 100)
		for rows.Next() {
			var r evRow
			var raw []byte
			if err := rows.Scan(&r.id, &r.subject, &r.typ, &r.at, &raw); err != nil {
				rows.Close()
				return processed, awarded, err
			}
			_ = json.Unmarshal(raw, &r.data)
			batch = append(batch, r)
		}
		rows.Close()
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			processed++
			inserted, err := e.awardHistorical(ctx, r.subject, r.id, r.at, sourceType,
				r.data.SourceID, ruleKey, ruleVersion, xpAmount, badgeKey, r.data.PrivacyClass)
			if err != nil {
				return processed, awarded, err
			}
			if inserted {
				awarded++
			}
			lastID = r.id
		}
	}
	return processed, awarded, nil
}

func (e *Engine) awardHistorical(ctx context.Context, subject, eventID string, occurredAt time.Time, sourceType, sourceID, ruleKey string, ruleVersion, xpAmount int, badgeKey *string, privacy string) (bool, error) {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	inserted := false
	if xpAmount != 0 {
		var id string
		err := tx.QueryRow(ctx, `
			INSERT INTO recognition.xp_entries
				(subject_id, amount, reason, rule_key, rule_version, source_type,
				 source_id, event_id, occurred_at)
			VALUES ($1,$2,'award',$3,$4,$5,$6,$7::uuid,$8)
			ON CONFLICT (subject_id, rule_key, source_type, source_id) WHERE reason='award'
			DO NOTHING RETURNING id::text`,
			subject, xpAmount, ruleKey, ruleVersion, sourceType, sourceID, eventID, occurredAt,
		).Scan(&id)
		if err == nil {
			inserted = true
			if err := updateXPTotal(ctx, tx, subject, xpAmount); err != nil {
				return false, err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}
	if badgeKey != nil && *badgeKey != "" {
		var id string
		err := tx.QueryRow(ctx, `
			INSERT INTO recognition.badge_awards
				(subject_id, badge_key, rule_key, rule_version, source_type, source_id,
				 event_id, privacy_class, awarded_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7::uuid,$8,$9)
			ON CONFLICT (subject_id, badge_key, source_type, source_id) DO NOTHING
			RETURNING id::text`,
			subject, *badgeKey, ruleKey, ruleVersion, sourceType, sourceID, eventID,
			privacy, occurredAt).Scan(&id)
		if err == nil {
			inserted = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}
	return inserted, tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Adjustments, switches, audit
// ---------------------------------------------------------------------------

func (e *Engine) CreateAdjustment(ctx context.Context, actor, approver, subject string, amount int, reason, note string, compensatingFor *string, requestID string) (XpEntry, error) {
	if amount == 0 || subject == "" || len(note) < 10 || (reason != "adjustment" && reason != "reversal") {
		return XpEntry{}, ErrInvalidInput
	}
	if approver == "" || approver == actor {
		return XpEntry{}, ErrSelfApproval
	}
	sourceID := ""
	if compensatingFor != nil && *compensatingFor != "" {
		sourceID = *compensatingFor
	} else {
		id, err := uuid.NewV7()
		if err != nil {
			return XpEntry{}, err
		}
		sourceID = id.String()
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return XpEntry{}, err
	}
	defer tx.Rollback(ctx)
	var id string
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO recognition.xp_entries
			(subject_id, amount, reason, rule_key, rule_version, source_type, source_id,
			 compensating_for, approved_by, note)
		VALUES ($1,$2,$3,'manual.adjustment',1,'adjustment',$4,
		        NULLIF($5,'')::uuid, NULLIF($6,''), $7)
		RETURNING id::text, created_at`,
		subject, amount, reason, sourceID, deref(compensatingFor), approver, note,
	).Scan(&id, &createdAt)
	if err != nil {
		return XpEntry{}, err
	}
	if err := updateXPTotal(ctx, tx, subject, amount); err != nil {
		return XpEntry{}, err
	}
	if err := writeAudit(ctx, tx, actor, "xp.adjust", "xp_entry", id,
		nil, map[string]any{"amount": amount, "reason": reason, "note": note},
		note, requestID); err != nil {
		return XpEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return XpEntry{}, err
	}
	return e.getXpEntry(ctx, id)
}

func (e *Engine) getXpEntry(ctx context.Context, id string) (XpEntry, error) {
	var x XpEntry
	err := e.pool.QueryRow(ctx, `
		SELECT id::text, amount, reason, source_type, source_id, note, created_at
		FROM recognition.xp_entries WHERE id=$1::uuid`, id,
	).Scan(&x.ID, &x.Amount, &x.Reason, &x.SourceType, &x.SourceID, &x.Note, &x.CreatedAt)
	return x, err
}

func (e *Engine) KillSwitches(ctx context.Context) ([]KillSwitch, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT key, enabled, reason, updated_by, updated_at
		FROM recognition.kill_switches ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KillSwitch, 0)
	for rows.Next() {
		var k KillSwitch
		var reason, updatedBy pgtype.Text
		var updatedAt pgtype.Timestamptz
		if err := rows.Scan(&k.Key, &k.Enabled, &reason, &updatedBy, &updatedAt); err != nil {
			return nil, err
		}
		if reason.Valid {
			k.Reason = &reason.String
		}
		if updatedBy.Valid {
			k.UpdatedBy = &updatedBy.String
		}
		if updatedAt.Valid {
			t := updatedAt.Time
			k.UpdatedAt = &t
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (e *Engine) SetKillSwitch(ctx context.Context, actor, key string, enabled bool, reason, requestID string) (KillSwitch, error) {
	valid := map[string]bool{"global": true, "public_leaderboard": true, "source:event": true, "source:audio": true}
	if !valid[key] || len(reason) < 5 {
		return KillSwitch{}, ErrInvalidInput
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return KillSwitch{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		UPDATE recognition.kill_switches
		SET enabled=$2, reason=$3, updated_by=NULLIF($4,''), updated_at=now()
		WHERE key=$1`, key, enabled, reason, actor)
	if err != nil {
		return KillSwitch{}, err
	}
	if err := writeAudit(ctx, tx, actor, "kill_switch.set", "kill_switch", key, nil,
		map[string]any{"enabled": enabled, "reason": reason}, reason, requestID); err != nil {
		return KillSwitch{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return KillSwitch{}, err
	}
	return e.getKillSwitch(ctx, key)
}

func (e *Engine) getKillSwitch(ctx context.Context, key string) (KillSwitch, error) {
	var k KillSwitch
	var reason, updatedBy pgtype.Text
	var updatedAt pgtype.Timestamptz
	err := e.pool.QueryRow(ctx, `
		SELECT key, enabled, reason, updated_by, updated_at
		FROM recognition.kill_switches WHERE key=$1`, key,
	).Scan(&k.Key, &k.Enabled, &reason, &updatedBy, &updatedAt)
	if err != nil {
		return k, err
	}
	if reason.Valid {
		k.Reason = &reason.String
	}
	if updatedBy.Valid {
		k.UpdatedBy = &updatedBy.String
	}
	if updatedAt.Valid {
		t := updatedAt.Time
		k.UpdatedAt = &t
	}
	return k, nil
}

func (e *Engine) Audit(ctx context.Context, entityType, cursor string, limit int) ([]AuditEntry, bool, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := parseOffset(cursor)
	rows, err := e.pool.Query(ctx, `
		SELECT id::text, actor_id, action, entity_type, entity_id, reason, created_at
		FROM recognition.audit
		WHERE ($1='' OR entity_type=$1)
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3`, entityType, limit+1, offset)
	if err != nil {
		return nil, false, "", err
	}
	defer rows.Close()
	out := make([]AuditEntry, 0, limit)
	for rows.Next() {
		var a AuditEntry
		var actorID, entityID, reason pgtype.Text
		if err := rows.Scan(&a.ID, &actorID, &a.Action, &a.EntityType, &entityID,
			&reason, &a.CreatedAt); err != nil {
			return nil, false, "", err
		}
		if actorID.Valid {
			a.ActorRef = &actorID.String
		}
		if entityID.Valid {
			a.EntityID = &entityID.String
		}
		if reason.Valid {
			a.Reason = &reason.String
		}
		out = append(out, a)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	next := ""
	if hasMore {
		next = strconv.Itoa(offset + limit)
	}
	return out, hasMore, next, rows.Err()
}

func logAudit(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, actor, action, entityType, entityID string, before, after any, reason, requestID string) {
	_ = writeAudit(ctx, q, actor, action, entityType, entityID, before, after, reason, requestID)
}

// RecordConsent stores a scoped consent decision. The engine only reads the
// latest granted row; consent_records remains the account-level audit.
func (e *Engine) RecordConsent(ctx context.Context, subject, scope, version, state string) error {
	if scope == "" || version == "" || (state != "granted" && state != "denied" && state != "unknown") {
		return ErrInvalidInput
	}
	_, err := e.pool.Exec(ctx, `
		INSERT INTO recognition.consents (subject_id, scope, version, state, source)
		VALUES ($1,$2,$3,$4,'app')`, subject, scope, version, state)
	return err
}
