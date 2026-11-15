package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
	if n.Status() == "" {
		return UpsertResult{}, fmt.Errorf("invalid status")
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
		sameHash := existing.ContentHash == n.ContentHash
		if sameHash && n.MsgType != "Cancel" {
			_, _ = tx.Exec(ctx, `INSERT INTO alert_source_messages (source_id, source_identifier, alert_id, content_hash)
				VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, n.SourceID, n.SourceIdentifier, existing.ID, n.ContentHash)
			if err := tx.Commit(ctx); err != nil {
				return UpsertResult{}, err
			}
			return UpsertResult{ID: existing.ID, Status: existing.Status}, nil
		}

		status := "active"
		if n.MsgType == "Cancel" {
			status = "cancelled"
		}
		_, err = tx.Exec(ctx, `
			UPDATE alerts SET
			  source_url=$2, msg_type=$3, status=$4, category=$5, categories=$6, event=$7,
			  severity=$8, urgency=$9, certainty=$10, language=$11, headline=$12,
			  description=$13, instruction=$14, web_url=$15, contact=$16, sender=$17,
			  sender_name=$18, sent_at=$19, effective_at=$20, onset_at=$21, expires_at=$22,
			  content_hash=$23, raw_area_desc=$24, mapping_method=$25, mapping_review=$26,
			  cap_references=$27, is_test=$28, updated_at=now(),
			  cancelled_at = CASE WHEN $4 = 'cancelled' THEN now() ELSE cancelled_at END
			WHERE id=$1`,
			existing.ID, n.SourceURL, n.MsgType, status, n.Category, n.Categories, n.Event,
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
		reason := "update"
		if n.MsgType == "Cancel" {
			reason = "cancel"
		}
		if material && pushEnabled {
			_, err = tx.Exec(ctx, `INSERT INTO alert_fanout_jobs (alert_id, reason, content_hash)
				VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, existing.ID, reason, n.ContentHash)
			if err != nil {
				return UpsertResult{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return UpsertResult{}, err
		}
		return UpsertResult{ID: existing.ID, Updated: true, Material: material, Status: status}, nil
	}

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO alerts (
		  source_id, source_identifier, source_url, msg_type, status, category, categories,
		  event, severity, urgency, certainty, language, headline, description, instruction,
		  web_url, contact, sender, sender_name, sent_at, effective_at, onset_at, expires_at,
		  content_hash, raw_area_desc, mapping_method, mapping_review, cap_references, is_test
		) VALUES ($1,$2,$3,$4,'active',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
		  $19,$20,$21,$22,$23,$24,$25,$26,$27,$28)
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
	if pushEnabled && !n.IsTest {
		_, err = tx.Exec(ctx, `INSERT INTO alert_fanout_jobs (alert_id, reason, content_hash)
			VALUES ($1,'new',$2) ON CONFLICT DO NOTHING`, id, n.ContentHash)
		if err != nil {
			return UpsertResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return UpsertResult{}, err
	}
	return UpsertResult{ID: id, Created: true, Material: true, Status: "active"}, nil
}

func (n NormalizedAlert) Status() string {
	switch n.MsgType {
	case "Cancel":
		return "cancelled"
	case "Alert", "Update", "":
		return "active"
	default:
		return "active"
	}
}

func (s *Store) findExisting(ctx context.Context, sourceID, sourceIdentifier, reference string) (*existingAlert, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, severity, COALESCE(instruction,''), expires_at, status, content_hash,
		       COALESCE(array_agg(a.county_gid) FILTER (WHERE a.county_gid IS NOT NULL), '{}')
		FROM alerts
		LEFT JOIN alert_areas a ON a.alert_id = alerts.id
		WHERE source_id=$1 AND source_identifier = $2
		GROUP BY alerts.id`, sourceID, sourceIdentifier)
	if err != nil {
		return nil, err
	}
	existing, err := scanExisting(rows)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if reference == "" {
		return nil, pgx.ErrNoRows
	}

	rows, err = s.pool.Query(ctx, `
		SELECT id, severity, COALESCE(instruction,''), expires_at, status, content_hash,
		       COALESCE(array_agg(a.county_gid) FILTER (WHERE a.county_gid IS NOT NULL), '{}')
		FROM alerts
		LEFT JOIN alert_areas a ON a.alert_id = alerts.id
		WHERE source_id=$1 AND source_identifier = $2
		GROUP BY alerts.id`, sourceID, reference)
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
	if err := rows.Scan(&e.ID, &e.Severity, &e.Instruction, &e.ExpiresAt, &e.Status, &e.ContentHash, &e.AreaGIDs); err != nil {
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
