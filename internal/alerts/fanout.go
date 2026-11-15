package alerts

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bradleygichuru/ytci-go/internal/push"
)

type Worker struct {
	store    *Store
	push     *push.Client
	interval time.Duration
	batch    int
	dryRun   bool
}

func NewWorker(store *Store, pushClient *push.Client) *Worker {
	return &Worker{store: store, push: pushClient, interval: 15 * time.Second, batch: 100}
}

func (w *Worker) SetDryRun(dry bool) { w.dryRun = dry }

func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	slog.Info("alerts worker started")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				slog.Error("alerts worker tick", "error", err)
			}
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	if err := w.heartbeat(ctx); err != nil {
		slog.Warn("alerts worker heartbeat", "error", err)
	}
	if err := w.expire(ctx); err != nil {
		return err
	}
	_, _ = w.store.Pool().Exec(ctx, `
		UPDATE alert_deliveries SET status='queued'
		WHERE status='sending' AND sent_at IS NULL AND created_at < now() - interval '10 minutes'`)
	if err := w.processJobs(ctx); err != nil {
		return err
	}
	if err := w.sendDue(ctx); err != nil {
		return err
	}
	if err := w.checkReceipts(ctx); err != nil {
		return err
	}
	return nil
}

func (w *Worker) heartbeat(ctx context.Context) error {
	_, err := w.store.Pool().Exec(ctx, `
		INSERT INTO alert_worker_heartbeat (worker, last_seen_at, version)
		VALUES ('alerts-worker', now(), '1')
		ON CONFLICT (worker) DO UPDATE SET last_seen_at=now(), version=EXCLUDED.version`)
	return err
}

func (w *Worker) expire(ctx context.Context) error {
	_, err := w.store.Pool().Exec(ctx, `
		UPDATE alerts SET status='expired', updated_at=now()
		WHERE status='active' AND expires_at IS NOT NULL AND expires_at < now()`)
	return err
}

func (w *Worker) processJobs(ctx context.Context) error {
	rows, err := w.store.Pool().Query(ctx, `
		UPDATE alert_fanout_jobs j
		SET status='running', started_at=now(), attempts=attempts+1
		WHERE j.id IN (
			SELECT id FROM alert_fanout_jobs
			WHERE status='pending' AND next_attempt_at <= now()
			ORDER BY created_at LIMIT 5 FOR UPDATE SKIP LOCKED
		)
		RETURNING j.id, j.alert_id, j.reason, j.content_hash`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type job struct{ id, alertID, reason, hash string }
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.alertID, &j.reason, &j.hash); err != nil {
			return err
		}
		jobs = append(jobs, j)
	}
	for _, j := range jobs {
		count, err := w.fanout(ctx, j.alertID, j.reason)
		if err != nil {
			_, _ = w.store.Pool().Exec(ctx, `
				UPDATE alert_fanout_jobs SET status=CASE WHEN attempts >= 5 THEN 'dead' ELSE 'pending' END,
				  next_attempt_at=now() + (interval '1 minute' * attempts), last_error=$2, finished_at=now()
				WHERE id=$1`, j.id, err.Error())
			slog.Error("alerts fanout failed", "job", j.id, "alert", j.alertID, "error", err)
			continue
		}
		_, _ = w.store.Pool().Exec(ctx, `
			UPDATE alert_fanout_jobs SET status='done', recipient_count=$2, finished_at=now(), last_error=NULL
			WHERE id=$1`, j.id, count)
	}
	return rows.Err()
}

type alertRow struct {
	ID          string
	SourceID    string
	Severity    string
	Category    string
	Status      string
	Event       string
	Headline    string
	Description string
	Instruction string
	SenderName  string
	SourceURL   string
	ExpiresAt   *time.Time
}

func (w *Worker) fanout(ctx context.Context, alertID, reason string) (int, error) {
	alert, err := w.loadAlert(ctx, alertID)
	if err != nil {
		return 0, err
	}
	if alert.ExpiresAt != nil && !alert.ExpiresAt.After(time.Now()) {
		return 0, nil
	}
	if alert.Status == "cancelled" {
		tag, err := w.store.Pool().Exec(ctx, `
			UPDATE alert_deliveries SET status='queued', deliver_after=now(), attempts=0
			WHERE alert_id=$1 AND status IN ('sent','failed')`, alertID)
		if err != nil {
			return 0, err
		}
		return int(tag.RowsAffected()), nil
	}
	if alert.Status != "active" {
		return 0, nil
	}
	areaRows, err := w.store.Pool().Query(ctx, `SELECT county_gid FROM alert_areas WHERE alert_id=$1`, alertID)
	if err != nil {
		return 0, err
	}
	var areas []int32
	for areaRows.Next() {
		var gid int32
		if err := areaRows.Scan(&gid); err == nil {
			areas = append(areas, gid)
		}
	}
	areaRows.Close()

	rows, err := w.store.Pool().Query(ctx, `
		SELECT p.user_id, pt.id, pt.token, p.enabled, p.min_severity, p.categories,
		       p.quiet_hours_enabled, p.quiet_hours_start::text, p.quiet_hours_end::text,
		       p.timezone, p.critical_override, p.home_county_gid, p.watch_county_gids,
		       p.follow_current_location, l.county_gid,
		       COALESCE(up.consent_notifications, false)
		FROM alert_preferences p
		JOIN user_profiles up ON up.user_id = p.user_id
		JOIN push_tokens pt ON pt.user_id::text = p.user_id AND pt.is_active = true
		LEFT JOIN user_alert_location l
		  ON l.user_id = p.user_id AND l.updated_at > now() - interval '24 hours'
		WHERE up.consent_notifications = true AND p.enabled = true AND p.os_permission_ok = true`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	now := time.Now()
	count := 0
	for rows.Next() {
		var (
			userID, tokenID, token string
			enabled                bool
			minSeverity            string
			categories             []string
			quietEnabled           bool
			quietStart, quietEnd   string
			timezone               string
			criticalOverride       bool
			followLocation         bool
			consent                bool
			home                   *int32
			watch                  []int32
			location               *int32
		)
		if err := rows.Scan(&userID, &tokenID, &token, &enabled, &minSeverity, &categories,
			&quietEnabled, &quietStart, &quietEnd, &timezone, &criticalOverride,
			&home, &watch, &followLocation, &location, &consent); err != nil {
			return count, err
		}
		if !enabled || !consent {
			continue
		}
		reasonCode, countyGID := matchAlert(alert, areas, home, watch, location, followLocation)
		if reasonCode == "" {
			continue
		}
		prefs := DeliveryPolicy{
			Enabled: enabled, MinSeverity: minSeverity, Categories: categories,
			QuietHoursEnabled: quietEnabled, QuietStart: quietStart, QuietEnd: quietEnd,
			Timezone: timezone, CriticalOverride: criticalOverride,
		}
		allowed, why, after := ShouldPush(AlertPolicyInput{
			Severity: alert.Severity, Category: alert.Category, Status: alert.Status, ExpiresAt: alert.ExpiresAt,
		}, prefs, now)
		status := "queued"
		deliverAfter := now
		if allowed && w.capExceeded(ctx, userID, alert.Severity) {
			status = "skipped_preference"
			allowed = false
			why = "cap"
		}
		switch {
		case why == "expired":
			status = "skipped_expired"
		case why == "category" || why == "severity" || why == "disabled":
			status = "skipped_preference"
		case why == "quiet_hours":
			status = "skipped_quiet_hours"
			deliverAfter = after
		case !allowed:
			status = "skipped_preference"
		}
		_, err := w.store.Pool().Exec(ctx, `
			INSERT INTO alert_deliveries (alert_id, user_id, push_token_id, county_gid, reason, status, deliver_after)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (alert_id, push_token_id) DO UPDATE SET
			  status=EXCLUDED.status, deliver_after=EXCLUDED.deliver_after, attempts=0,
			  error_code=NULL, error_message=NULL
			WHERE $8='update' AND alert_deliveries.status IN ('sent','failed','skipped_quiet_hours','skipped_preference')`,
			alert.ID, userID, tokenID, countyGID, reasonCode, status, deliverAfter, reason)
		if err != nil {
			return count, err
		}
		if status == "queued" {
			count++
		}
	}
	return count, rows.Err()
}

func matchAlert(alert alertRow, areas []int32, home *int32, watch []int32, location *int32, followLocation bool) (string, *int32) {
	if len(areas) == 0 {
		if home != nil || len(watch) > 0 || (followLocation && location != nil) {
			return "national", nil
		}
		return "", nil
	}
	if home != nil && containsGID(areas, *home) {
		return "home", home
	}
	for _, gid := range watch {
		if containsGID(areas, gid) {
			g := gid
			return "watch", &g
		}
	}
	if followLocation && location != nil && containsGID(areas, *location) {
		return "location", location
	}
	return "", nil
}

func (w *Worker) capExceeded(ctx context.Context, userID, severity string) bool {
	if SeverityRank(severity) >= 4 {
		return false
	}
	var day, hour int
	err := w.store.Pool().QueryRow(ctx, `
		SELECT count(DISTINCT d.alert_id) FILTER (WHERE d.created_at > now() - interval '24 hours'),
		       count(DISTINCT d.alert_id) FILTER (WHERE d.created_at > now() - interval '1 hour')
		FROM alert_deliveries d
		JOIN alerts a ON a.id = d.alert_id
		WHERE d.user_id=$1 AND d.status IN ('queued','sent') AND a.severity <> 'Extreme'`,
		userID).Scan(&day, &hour)
	if err != nil {
		return false
	}
	return day >= 4 || hour >= 2
}

func containsGID(areas []int32, gid int32) bool {
	for _, a := range areas {
		if a == gid {
			return true
		}
	}
	return false
}

func (w *Worker) loadAlert(ctx context.Context, id string) (alertRow, error) {
	var a alertRow
	var expires pgtype.Timestamptz
	err := w.store.Pool().QueryRow(ctx, `
		SELECT id, source_id, severity, category, status, event, COALESCE(headline,''),
		       COALESCE(description,''), COALESCE(instruction,''), COALESCE(sender_name,''),
		       COALESCE(source_url,''), expires_at
		FROM alerts WHERE id=$1`, id).Scan(&a.ID, &a.SourceID, &a.Severity, &a.Category, &a.Status, &a.Event,
		&a.Headline, &a.Description, &a.Instruction, &a.SenderName, &a.SourceURL, &expires)
	if err != nil {
		return a, err
	}
	if expires.Valid {
		t := expires.Time
		a.ExpiresAt = &t
	}
	return a, nil
}

func (w *Worker) sendDue(ctx context.Context) error {
	rows, err := w.store.Pool().Query(ctx, `
		SELECT d.id, d.alert_id, d.user_id, pt.token, a.source_id, a.severity, a.event, a.headline,
		       COALESCE(a.instruction,a.description,''), a.expires_at, a.status, d.county_gid
		FROM alert_deliveries d
		JOIN alerts a ON a.id=d.alert_id
		JOIN push_tokens pt ON pt.id=d.push_token_id
		WHERE d.status='queued' AND d.deliver_after <= now() AND a.status IN ('active','cancelled')
		ORDER BY d.created_at LIMIT $1`, w.batch)
	if err != nil {
		return err
	}
	defer rows.Close()
	type due struct {
		id                                                                int64
		alertID, userID, token, sourceID, severity, event, headline, body string
		status                                                            string
		expires                                                           *time.Time
		countyGID                                                         *int32
	}
	var items []due
	for rows.Next() {
		var d due
		var expires pgtype.Timestamptz
		if err := rows.Scan(&d.id, &d.alertID, &d.userID, &d.token, &d.sourceID, &d.severity,
			&d.event, &d.headline, &d.body, &expires, &d.status, &d.countyGID); err != nil {
			return err
		}
		if expires.Valid {
			t := expires.Time
			d.expires = &t
		}
		items = append(items, d)
	}
	if len(items) == 0 {
		return nil
	}

	// Mark sending before the HTTP call.
	ids := make([]int64, 0, len(items))
	for _, d := range items {
		ids = append(ids, d.id)
	}
	_, _ = w.store.Pool().Exec(ctx, `UPDATE alert_deliveries SET status='sending', attempts=attempts+1 WHERE id = ANY($1::bigint[])`, ids)

	if w.dryRun || w.push == nil {
		for _, d := range items {
			slog.Info("alerts dry run", "delivery", d.id, "alert", d.alertID, "user", d.userID)
		}
		return nil
	}

	messages := make([]push.ExpoPushMessage, 0, len(items))
	byToken := make(map[string][]due)
	for _, d := range items {
		title := strings.TrimSpace(d.headline)
		if title == "" {
			title = d.event
		}
		body := truncate(d.body, 180)
		ttl := 86400
		if d.expires != nil {
			sec := int(time.Until(*d.expires).Seconds())
			if sec <= 0 {
				continue
			}
			if sec < ttl {
				ttl = sec
			}
		}
		channel := "safety_alerts"
		interruption := "active"
		if SeverityRank(d.severity) >= 3 {
			interruption = "timeSensitive"
		}
		messages = append(messages, push.ExpoPushMessage{
			To: d.token, Title: title, Body: body, Sound: "default",
			Data:     map[string]any{"alertId": d.alertID, "severity": d.severity},
			Priority: priorityFor(d.severity), TTL: ttl,
			ChannelID: channel, InterruptionLevel: interruption,
			CollapseID: "alert:" + d.alertID, ThreadID: "alerts",
		})
		byToken[d.token] = append(byToken[d.token], d)
	}
	if len(messages) == 0 {
		return nil
	}
	result, err := w.push.SendMessages(ctx, messages)
	if err != nil {
		return err
	}
	for token, ticket := range result.TicketForToken {
		for _, d := range byToken[token] {
			_, _ = w.store.Pool().Exec(ctx, `UPDATE alert_deliveries SET status='sent', sent_at=now(), expo_ticket_id=$2 WHERE id=$1`, d.id, ticket)
		}
	}
	for _, d := range items {
		if _, ok := result.TicketForToken[d.token]; !ok {
			_, _ = w.store.Pool().Exec(ctx, `UPDATE alert_deliveries SET status='failed', error_code='TICKET_ERROR' WHERE id=$1`, d.id)
		}
	}
	for token := range result.TokenErrors {
		_, _ = w.store.Pool().Exec(ctx, `UPDATE push_tokens SET is_active=false, deactivated_at=now(), deactivation_reason='DeviceNotRegistered' WHERE token=$1`, token)
	}
	return nil
}

func (w *Worker) checkReceipts(ctx context.Context) error {
	if w.push == nil {
		return nil
	}
	rows, err := w.store.Pool().Query(ctx, `
		SELECT id, expo_ticket_id, push_token_id FROM alert_deliveries
		WHERE expo_ticket_id IS NOT NULL AND receipt_checked_at IS NULL
		  AND sent_at < now() - interval '15 minutes'
		ORDER BY sent_at LIMIT 1000`)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := make([]string, 0)
	byTicket := make(map[string]int64)
	for rows.Next() {
		var id int64
		var ticket, tokenID string
		if err := rows.Scan(&id, &ticket, &tokenID); err != nil {
			return err
		}
		ids = append(ids, ticket)
		byTicket[ticket] = id
	}
	if len(ids) == 0 {
		return nil
	}
	receipts, err := w.push.GetReceiptMap(ctx, ids)
	if err != nil {
		return err
	}
	for ticket, receipt := range receipts {
		id := byTicket[ticket]
		_, _ = w.store.Pool().Exec(ctx, `UPDATE alert_deliveries SET expo_receipt_status=$2, error_code=NULLIF($3,''), error_message=NULLIF($4,''), receipt_checked_at=now() WHERE id=$1`, id, receipt.Status, receipt.Code, receipt.Message)
		if receipt.Code == "DeviceNotRegistered" {
			var tokenID string
			_ = w.store.Pool().QueryRow(ctx, `SELECT push_token_id FROM alert_deliveries WHERE id=$1`, id).Scan(&tokenID)
			_, _ = w.store.Pool().Exec(ctx, `UPDATE push_tokens SET is_active=false, deactivated_at=now(), deactivation_reason='DeviceNotRegistered' WHERE id=$1`, tokenID)
		}
	}
	return nil
}

func priorityFor(severity string) string {
	if SeverityRank(severity) >= 3 {
		return "high"
	}
	return "normal"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n-1]) + "…"
}
