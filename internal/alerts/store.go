package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrCountiesEmpty = errors.New("kenya_counties is empty; seed OCHA COD-AB before enabling alerts")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

type NormalizedAlert struct {
	SourceID            string
	SourceIdentifier    string
	SourceURL           string
	MsgType             string
	Category            string
	Categories          []string
	Event               string
	Severity            string
	Urgency             string
	Certainty           string
	Language            string
	Headline            string
	Description         string
	Instruction         string
	WebURL              string
	Contact             string
	Sender              string
	SenderName          string
	SentAt              time.Time
	EffectiveAt         *time.Time
	OnsetAt             *time.Time
	ExpiresAt           *time.Time
	ContentHash         string
	RawAreaDesc         string
	MappingMethod       string
	MappingReview       bool
	CapReferences       []string
	IsTest              bool
	ReferenceIdentifier string
}

type UpsertResult struct {
	ID       string
	Created  bool
	Updated  bool
	Material bool
	Status   string
}

type existingAlert struct {
	ID          string
	Severity    string
	Instruction string
	OnsetAt     *time.Time
	ExpiresAt   *time.Time
	Status      string
	ContentHash string
	AreaGIDs    []int
}

func (s *Store) Counties(ctx context.Context) ([]County, error) {
	rows, err := s.pool.Query(ctx, `SELECT gid, adm1_name, COALESCE(adm1_pcode, '') FROM kenya_counties ORDER BY gid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []County
	for rows.Next() {
		var c County
		if err := rows.Scan(&c.GID, &c.Name, &c.PCode); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CountiesForPolygon(ctx context.Context, wkt string) ([]CountyMatch, error) {
	rows, err := s.pool.Query(ctx, `
		WITH src AS (SELECT ST_MakeValid(ST_GeomFromText($1, 4326)) AS geom)
		SELECT c.gid, c.adm1_name, COALESCE(c.adm1_pcode, ''),
		       round((ST_Area(ST_Intersection(c.geom, src.geom)::geography)
		              / NULLIF(ST_Area(c.geom::geography), 0) * 100)::numeric, 3)::float8
		FROM kenya_counties c, src
		WHERE ST_Intersects(c.geom, src.geom)
		ORDER BY c.gid`, wkt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CountyMatch
	for rows.Next() {
		var m CountyMatch
		if err := rows.Scan(&m.County.GID, &m.County.Name, &m.County.PCode, &m.Overlap); err != nil {
			return nil, err
		}
		m.Method = MethodPolygon
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) StartRun(ctx context.Context, sourceID string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO alert_source_runs (source_id, status) VALUES ($1, 'running') RETURNING id`, sourceID).Scan(&id)
	return id, err
}

func (s *Store) FinishRun(ctx context.Context, runID int64, status string, httpStatus int, seen, created, updated int, bytes int64, duration time.Duration, errMsg string) {
	_, err := s.pool.Exec(ctx, `
		UPDATE alert_source_runs
		SET finished_at = now(), status = $2, http_status = NULLIF($3, 0),
		    items_seen = $4, items_new = $5, items_updated = $6,
		    bytes_received = $7, duration_ms = $8, error = NULLIF($9, '')
		WHERE id = $1`,
		runID, status, httpStatus, seen, created, updated, bytes, duration.Milliseconds(), errMsg)
	if err != nil {
		// The run record is observability, not a reason to fail ingestion.
		_ = err
	}
}

func (s *Store) UpdateSourceHealth(ctx context.Context, sourceID string, success bool, httpStatus int, errMsg string) {
	if success {
		_, _ = s.pool.Exec(ctx, `
			UPDATE alert_sources
			SET last_checked_at = now(), last_success_at = now(), consecutive_failures = 0,
			    last_http_status = $2, last_error = NULL, updated_at = now()
			WHERE id = $1`, sourceID, httpStatus)
		return
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE alert_sources
		SET last_checked_at = now(), consecutive_failures = consecutive_failures + 1,
		    last_http_status = $2, last_error = $3, updated_at = now()
		WHERE id = $1`, sourceID, httpStatus, errMsg)
}

type SourceState struct {
	ETag                string
	LastModified        string
	LastBuildDate       string
	LastCheckedAt       time.Time
	HasChecked          bool
	LastSuccessAt       time.Time
	HasSucceeded        bool
	ConsecutiveFailures int
	PushEnabled         bool
	Enabled             bool
	PollIntervalSeconds int
}

func (s *Store) SourceState(ctx context.Context, sourceID string) (SourceState, error) {
	var state SourceState
	var etag, lastModified, lastBuild pgtype.Text
	var checked, success pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		SELECT last_etag, last_modified, last_build_date, last_checked_at, last_success_at,
		       consecutive_failures, push_enabled, enabled, poll_interval_seconds
		FROM alert_sources WHERE id=$1`, sourceID).Scan(
		&etag, &lastModified, &lastBuild, &checked, &success, &state.ConsecutiveFailures,
		&state.PushEnabled, &state.Enabled, &state.PollIntervalSeconds)
	if err != nil {
		return state, err
	}
	if etag.Valid {
		state.ETag = etag.String
	}
	if lastModified.Valid {
		state.LastModified = lastModified.String
	}
	if lastBuild.Valid {
		state.LastBuildDate = lastBuild.String
	}
	if checked.Valid {
		state.LastCheckedAt = checked.Time
		state.HasChecked = true
	}
	if success.Valid {
		state.LastSuccessAt = success.Time
		state.HasSucceeded = true
	}
	return state, nil
}

func (s *Store) SaveSourceValidators(ctx context.Context, sourceID, etag, lastModified, lastBuildDate string) {
	_, _ = s.pool.Exec(ctx, `
		UPDATE alert_sources SET last_etag=NULLIF($2,''), last_modified=NULLIF($3,''),
		    last_build_date=NULLIF($4,''), updated_at=now()
		WHERE id=$1`, sourceID, etag, lastModified, lastBuildDate)
}

// RecentDeliveryAt returns the most recent delivery attempt for an alert. It is
// used to hold material-update pushes to one per six hours.
func (s *Store) RecentDeliveryAt(ctx context.Context, tx pgx.Tx, alertID string) (*time.Time, error) {
	var last pgtype.Timestamptz
	err := tx.QueryRow(ctx, `
		SELECT GREATEST(COALESCE(max(sent_at), 'epoch'::timestamptz),
		                COALESCE(max(created_at), 'epoch'::timestamptz))
		FROM alert_deliveries
		WHERE alert_id=$1 AND status IN ('queued','sending','sent')`, alertID).Scan(&last)
	if err != nil || !last.Valid {
		return nil, err
	}
	t := last.Time
	if t.Equal(time.Unix(0, 0).UTC()) || t.Year() <= 1 {
		return nil, nil
	}
	return &t, nil
}

func (s *Store) UpsertAlert(ctx context.Context, n NormalizedAlert, areas []CountyMatch, pushEnabled bool) (UpsertResult, error) {
	if n.SourceID == "" || n.SourceIdentifier == "" {
		return UpsertResult{}, fmt.Errorf("source and source identifier are required")
	}
	if n.MsgType == "" {
		n.MsgType = "Alert"
	}
	if n.Categories == nil {
		n.Categories = []string{}
	}
	if n.CapReferences == nil {
		n.CapReferences = []string{}
	}
	existing, err := s.findExisting(ctx, n.SourceID, n.SourceIdentifier, n.ReferenceIdentifier)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return UpsertResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UpsertResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if existing != nil {
		if existing.Status == "cancelled" {
			_, _ = tx.Exec(ctx, `INSERT INTO alert_source_messages (source_id, source_identifier, alert_id, content_hash)
				VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, n.SourceID, n.SourceIdentifier, existing.ID, n.ContentHash)
			if err := tx.Commit(ctx); err != nil {
				return UpsertResult{}, err
			}
			return UpsertResult{ID: existing.ID, Status: "cancelled"}, nil
		}
		sameHash := existing.ContentHash == n.ContentHash
		if sameHash && n.MsgType != "Cancel" {
			_, _ = tx.Exec(ctx, `INSERT INTO alert_source_messages (source_id, source_identifier, alert_id, content_hash)
				VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, n.SourceID, n.SourceIdentifier, existing.ID, n.ContentHash)
			if err := tx.Commit(ctx); err != nil {
				return UpsertResult{}, err
			}
			return UpsertResult{ID: existing.ID, Status: existing.Status}, nil
		}

		if n.MsgType == "Cancel" {
			// A cancellation withdraws the existing record. Keep its content and
			// areas so the tombstone remains readable.
			_, err = tx.Exec(ctx, `
				UPDATE alerts SET status='cancelled', cancelled_at=now(), msg_type='Cancel',
				  sent_at=$2, content_hash=$3, updated_at=now()
				WHERE id=$1`, existing.ID, n.SentAt, n.ContentHash)
			if err != nil {
				return UpsertResult{}, err
			}
			version, err := nextVersion(ctx, tx, existing.ID)
			if err != nil {
				return UpsertResult{}, err
			}
			if err := insertUpdate(ctx, tx, existing.ID, version, n); err != nil {
				return UpsertResult{}, err
			}
			_, _ = tx.Exec(ctx, `INSERT INTO alert_source_messages (source_id, source_identifier, alert_id, content_hash)
				VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, n.SourceID, n.SourceIdentifier, existing.ID, n.ContentHash)
			if pushEnabled {
				_, err = tx.Exec(ctx, `INSERT INTO alert_fanout_jobs (alert_id, reason, content_hash)
					VALUES ($1,'cancel',$2) ON CONFLICT DO NOTHING`, existing.ID, n.ContentHash)
				if err != nil {
					return UpsertResult{}, err
				}
			}
			if err := tx.Commit(ctx); err != nil {
				return UpsertResult{}, err
			}
			return UpsertResult{ID: existing.ID, Updated: true, Material: true, Status: "cancelled"}, nil
		}

		_, err = tx.Exec(ctx, `
			UPDATE alerts SET
			  source_url=$2, msg_type=$3, status='active', category=$4, categories=$5, event=$6,
			  severity=$7, urgency=$8, certainty=$9, language=$10, headline=$11,
			  description=$12, instruction=$13, web_url=$14, contact=$15, sender=$16,
			  sender_name=$17, sent_at=$18, effective_at=$19, onset_at=$20, expires_at=$21,
			  content_hash=$22, raw_area_desc=$23, mapping_method=$24, mapping_review=$25,
			  cap_references=$26, is_test=$27, updated_at=now()
			WHERE id=$1`,
			existing.ID, n.SourceURL, n.MsgType, n.Category, n.Categories, n.Event,
			n.Severity, n.Urgency, n.Certainty, n.Language, n.Headline, n.Description,
			n.Instruction, n.WebURL, n.Contact, n.Sender, n.SenderName, n.SentAt,
			n.EffectiveAt, n.OnsetAt, n.ExpiresAt, n.ContentHash, n.RawAreaDesc,
			n.MappingMethod, n.MappingReview, n.CapReferences, n.IsTest)
		if err != nil {
			return UpsertResult{}, err
		}

		if err := replaceAreas(ctx, tx, existing.ID, areas); err != nil {
			return UpsertResult{}, err
		}
		version, err := nextVersion(ctx, tx, existing.ID)
		if err != nil {
			return UpsertResult{}, err
		}
		if err := insertUpdate(ctx, tx, existing.ID, version, n); err != nil {
			return UpsertResult{}, err
		}
		_, _ = tx.Exec(ctx, `INSERT INTO alert_source_messages (source_id, source_identifier, alert_id, content_hash)
			VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, n.SourceID, n.SourceIdentifier, existing.ID, n.ContentHash)

		material := isMaterial(existing, n, areas)
		fanout := material && pushEnabled
		if fanout && SeverityRank(n.Severity) <= SeverityRank(existing.Severity) {
			// Material updates push at most once per six hours. Severity
			// escalations bypass this gate.
			if last, err := s.RecentDeliveryAt(ctx, tx, existing.ID); err == nil && last != nil && time.Since(*last) < 6*time.Hour {
				fanout = false
			}
		}
		if fanout {
			_, err = tx.Exec(ctx, `INSERT INTO alert_fanout_jobs (alert_id, reason, content_hash)
				VALUES ($1,'update',$2) ON CONFLICT DO NOTHING`, existing.ID, n.ContentHash)
			if err != nil {
				return UpsertResult{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return UpsertResult{}, err
		}
		return UpsertResult{ID: existing.ID, Updated: true, Material: material, Status: "active"}, nil
	}

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO alerts (
		  source_id, source_identifier, source_url, msg_type, status, category, categories,
		  event, severity, urgency, certainty, language, headline, description, instruction,
		  web_url, contact, sender, sender_name, sent_at, effective_at, onset_at, expires_at,
		  content_hash, raw_area_desc, mapping_method, mapping_review, cap_references, is_test,
		  cancelled_at
		) VALUES ($1,$2,$3,$4,
		  CASE WHEN $4='Cancel' THEN 'cancelled' ELSE 'active' END,
		  $5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,
		  CASE WHEN $4='Cancel' THEN now() ELSE NULL END)
		RETURNING id`,
		n.SourceID, n.SourceIdentifier, n.SourceURL, n.MsgType, n.Category, n.Categories,
		n.Event, n.Severity, n.Urgency, n.Certainty, n.Language, n.Headline, n.Description,
		n.Instruction, n.WebURL, n.Contact, n.Sender, n.SenderName, n.SentAt,
		n.EffectiveAt, n.OnsetAt, n.ExpiresAt, n.ContentHash, n.RawAreaDesc,
		n.MappingMethod, n.MappingReview, n.CapReferences, n.IsTest).Scan(&id)
	if err != nil {
		return UpsertResult{}, err
	}

	if err := replaceAreas(ctx, tx, id, areas); err != nil {
		return UpsertResult{}, err
	}
	if err := insertUpdate(ctx, tx, id, 1, n); err != nil {
		return UpsertResult{}, err
	}
	_, _ = tx.Exec(ctx, `INSERT INTO alert_source_messages (source_id, source_identifier, alert_id, content_hash)
		VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, n.SourceID, n.SourceIdentifier, id, n.ContentHash)
	if pushEnabled && !n.IsTest && n.MsgType != "Cancel" {
		_, err = tx.Exec(ctx, `INSERT INTO alert_fanout_jobs (alert_id, reason, content_hash)
			VALUES ($1,'new',$2) ON CONFLICT DO NOTHING`, id, n.ContentHash)
		if err != nil {
			return UpsertResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return UpsertResult{}, err
	}
	status := "active"
	if n.MsgType == "Cancel" {
		status = "cancelled"
	}
	return UpsertResult{ID: id, Created: true, Material: true, Status: status}, nil
}

func (s *Store) findExisting(ctx context.Context, sourceID, sourceIdentifier, reference string) (*existingAlert, error) {
	existing, err := s.findBySourceIdentifier(ctx, sourceID, sourceIdentifier)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// A message may use a new identifier while already being in the ledger,
	// or reference an earlier identifier in a chain of CAP updates.
	if existing, err = s.findByLedger(ctx, sourceID, sourceIdentifier); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if reference == "" {
		return nil, pgx.ErrNoRows
	}
	if existing, err = s.findBySourceIdentifier(ctx, sourceID, reference); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return s.findByLedger(ctx, sourceID, reference)
}

func (s *Store) findByLedger(ctx context.Context, sourceID, identifier string) (*existingAlert, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.severity, COALESCE(a.instruction,''), a.onset_at, a.expires_at,
		       a.status, a.content_hash,
		       COALESCE(array_agg(ar.county_gid) FILTER (WHERE ar.county_gid IS NOT NULL), '{}')
		FROM alert_source_messages m
		JOIN alerts a ON a.id = m.alert_id
		LEFT JOIN alert_areas ar ON ar.alert_id = a.id
		WHERE m.source_id=$1 AND m.source_identifier=$2
		GROUP BY a.id
		LIMIT 1`, sourceID, identifier)
	if err != nil {
		return nil, err
	}
	return scanExisting(rows)
}

func (s *Store) findBySourceIdentifier(ctx context.Context, sourceID, identifier string) (*existingAlert, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, severity, COALESCE(instruction,''), onset_at, expires_at, status, content_hash,
		       COALESCE(array_agg(a.county_gid) FILTER (WHERE a.county_gid IS NOT NULL), '{}')
		FROM alerts
		LEFT JOIN alert_areas a ON a.alert_id = alerts.id
		WHERE source_id=$1 AND source_identifier = $2
		GROUP BY alerts.id`, sourceID, identifier)
	if err != nil {
		return nil, err
	}
	return scanExisting(rows)
}

func scanExisting(rows pgx.Rows) (*existingAlert, error) {
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	var e existingAlert
	if err := rows.Scan(&e.ID, &e.Severity, &e.Instruction, &e.OnsetAt, &e.ExpiresAt, &e.Status, &e.ContentHash, &e.AreaGIDs); err != nil {
		return nil, err
	}
	return &e, rows.Err()
}

func replaceAreas(ctx context.Context, tx pgx.Tx, alertID string, areas []CountyMatch) error {
	if _, err := tx.Exec(ctx, `DELETE FROM alert_areas WHERE alert_id=$1`, alertID); err != nil {
		return err
	}
	for i, a := range areas {
		method := a.Method
		if method == "" {
			method = MethodManual
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO alert_areas (alert_id, county_gid, method, overlap_pct, is_primary)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (alert_id, county_gid) DO UPDATE SET method=EXCLUDED.method,
			  overlap_pct=EXCLUDED.overlap_pct, is_primary=EXCLUDED.is_primary`,
			alertID, a.County.GID, method, a.Overlap, i == 0)
		if err != nil {
			return err
		}
	}
	return nil
}

func nextVersion(ctx context.Context, tx pgx.Tx, alertID string) (int, error) {
	var v int
	err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM alert_updates WHERE alert_id=$1`, alertID).Scan(&v)
	return v, err
}

func insertUpdate(ctx context.Context, tx pgx.Tx, alertID string, version int, n NormalizedAlert) error {
	snapshot, _ := json.Marshal(n)
	_, err := tx.Exec(ctx, `
		INSERT INTO alert_updates (alert_id, version, source_identifier, msg_type, content_hash, changed_fields, snapshot, source_sent_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		alertID, version, n.SourceIdentifier, n.MsgType, n.ContentHash,
		changedFields(n), snapshot, n.SentAt)
	return err
}

func changedFields(n NormalizedAlert) []string {
	fields := []string{"content"}
	if n.Severity != "" {
		fields = append(fields, "severity")
	}
	if n.ExpiresAt != nil {
		fields = append(fields, "expires")
	}
	if n.Instruction != "" {
		fields = append(fields, "instruction")
	}
	if n.MsgType == "Cancel" {
		fields = append(fields, "status")
	}
	return fields
}

func isMaterial(existing *existingAlert, n NormalizedAlert, areas []CountyMatch) bool {
	if n.MsgType == "Cancel" {
		return true
	}
	if existing.Severity != n.Severity {
		return true
	}
	if existing.Instruction != n.Instruction {
		return true
	}
	if (existing.OnsetAt == nil) != (n.OnsetAt == nil) {
		return true
	}
	if existing.OnsetAt != nil && n.OnsetAt != nil && !existing.OnsetAt.Equal(*n.OnsetAt) {
		return true
	}
	if (existing.ExpiresAt == nil) != (n.ExpiresAt == nil) {
		return true
	}
	if existing.ExpiresAt != nil && n.ExpiresAt != nil && !existing.ExpiresAt.Equal(*n.ExpiresAt) {
		return true
	}
	if !sameIntSet(existing.AreaGIDs, areaGIDs(areas)) {
		return true
	}
	return false
}

func areaGIDs(areas []CountyMatch) []int {
	out := make([]int, 0, len(areas))
	for _, a := range areas {
		out = append(out, a.County.GID)
	}
	return out
}

func sameIntSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[int]bool, len(a))
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
	}
	return true
}

func NormalizeSeverity(value string) string {
	switch strings.Title(strings.ToLower(strings.TrimSpace(value))) {
	case "Extreme", "Severe", "Moderate", "Minor":
		return strings.Title(strings.ToLower(strings.TrimSpace(value)))
	default:
		return "Unknown"
	}
}
