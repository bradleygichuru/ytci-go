package push

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

const ProgramChannelID = "program_updates"

type ProgramPush struct {
	ID         string
	Title      string
	Body       string
	Priority   string
	ImageURL   string
	Route      string
	CountyGIDs []int
	ExpiresAt  *time.Time
}

// UpsertCampaignProgramUpdate turns a campaign notification into an inbox
// Program Update. The source pair makes retries idempotent.
func UpsertCampaignProgramUpdate(
	ctx context.Context,
	pool *pgxpool.Pool,
	notificationID, title, body, imageURL, dataRaw, targetRaw, sentBy string,
) (ProgramPush, error) {
	audience := ParseTargetAudience(targetRaw)
	priority := "Normal"
	if audience.Important {
		priority = "Important"
	}
	route := routeFromCampaignData(dataRaw)
	if sentBy == "" {
		sentBy = "system"
	}
	counties := audience.CountyGIDs
	if counties == nil {
		counties = []int{}
	}

	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO program_updates
		  (title, body, priority, status, county_gids, categories, image_url, link_url,
		   published_at, created_by, source, source_id)
		VALUES ($1,$2,$3,'published',$4,ARRAY['Program'],NULLIF($5,''),NULLIF($6,''),
		        now(),$7,'campaign',$8)
		ON CONFLICT (source, source_id) WHERE source = 'campaign' AND source_id IS NOT NULL
		DO UPDATE SET title=EXCLUDED.title, body=EXCLUDED.body, priority=EXCLUDED.priority,
		  county_gids=EXCLUDED.county_gids, image_url=EXCLUDED.image_url,
		  link_url=EXCLUDED.link_url, updated_at=now()
		RETURNING id`,
		title, body, priority, counties, imageURL, route, sentBy, notificationID,
	).Scan(&id)
	if err != nil {
		return ProgramPush{}, err
	}

	return ProgramPush{
		ID: id, Title: title, Body: body, Priority: priority, ImageURL: imageURL,
		Route: route, CountyGIDs: counties,
	}, nil
}

func routeFromCampaignData(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return ""
	}
	if route, ok := data["route"].(string); ok && route != "" {
		return route
	}
	if campaignID, ok := data["campaignId"].(string); ok && campaignID != "" {
		return "/campaigns/" + campaignID
	}
	return ""
}

// SendProgramUpdate sends an already-persisted Program Update to the Program
// audience. Quiet hours and caps suppress the push; the inbox item remains.
func SendProgramUpdate(ctx context.Context, pool *pgxpool.Pool, client *Client, update ProgramPush) (int, error) {
	if update.ID == "" {
		return 0, fmt.Errorf("program update id is required")
	}
	if update.ExpiresAt != nil && !update.ExpiresAt.After(time.Now()) {
		return 0, nil
	}
	if client == nil {
		return 0, nil
	}

	audience := TargetAudience{CountyGIDs: update.CountyGIDs, Important: update.Priority == "Important"}
	targets, err := ResolveProgramTargets(ctx, pool, audience)
	if err != nil {
		return 0, err
	}

	now := time.Now()
	route := update.Route
	if route == "" {
		route = "/alerts/program/" + update.ID
	}
	ttl := 86400
	if update.ExpiresAt != nil {
		seconds := int(time.Until(*update.ExpiresAt).Seconds())
		if seconds <= 0 {
			return 0, nil
		}
		if seconds < ttl {
			ttl = seconds
		}
	}

	messages := make([]ExpoPushMessage, 0, len(targets))
	deliveries := make([]int64, 0, len(targets))
	for _, target := range targets {
		if !quietHoursAllow(target, now) || programCapExceeded(ctx, pool, target.UserID) {
			insertProgramDelivery(ctx, pool, update.ID, target, "skipped")
			continue
		}
		var deliveryID int64
		err := pool.QueryRow(ctx, `
			INSERT INTO program_update_deliveries (program_update_id, user_id, push_token_id, status)
			VALUES ($1,$2,$3,'queued')
			ON CONFLICT (program_update_id, push_token_id) DO NOTHING
			RETURNING id`, update.ID, target.UserID, target.PushTokenID).Scan(&deliveryID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // already attempted for this token
		}
		if err != nil {
			return 0, err
		}
		messages = append(messages, BuildMessage(MessageSpec{
			To: target.Token, Title: update.Title, Body: update.Body,
			Data:     map[string]any{"programUpdateId": update.ID, "route": route},
			ImageURL: update.ImageURL, Priority: "default", TTL: ttl,
			ChannelID: ProgramChannelID, InterruptionLevel: "active",
			CollapseID: "program:" + update.ID, ThreadID: "program",
		}))
		deliveries = append(deliveries, deliveryID)
	}

	if len(messages) == 0 {
		return 0, nil
	}

	result, err := client.SendMessages(ctx, messages)
	if err != nil {
		for _, deliveryID := range deliveries {
			_, _ = pool.Exec(ctx, `
				UPDATE program_update_deliveries SET status='failed', error_code='SEND_FAILED'
				WHERE id=$1 AND status='queued'`, deliveryID)
		}
		return 0, err
	}

	for index, deliveryID := range deliveries {
		if ticket, ok := result.TicketForIndex[index]; ok {
			_, _ = pool.Exec(ctx, `
				UPDATE program_update_deliveries
				SET status='sent', sent_at=now(), expo_ticket_id=$2
				WHERE id=$1`, deliveryID, ticket)
		} else {
			_, _ = pool.Exec(ctx, `
				UPDATE program_update_deliveries SET status='failed', error_code='TICKET_ERROR'
				WHERE id=$1`, deliveryID)
		}
	}
	for token := range result.TokenErrors {
		_, _ = pool.Exec(ctx, `
			UPDATE push_tokens SET is_active=false, deactivated_at=now(),
			  deactivation_reason='DeviceNotRegistered' WHERE token=$1`, token)
	}
	return result.Sent, nil
}

func insertProgramDelivery(ctx context.Context, pool *pgxpool.Pool, programUpdateID string, target ProgramTarget, status string) {
	_, _ = pool.Exec(ctx, `
		INSERT INTO program_update_deliveries (program_update_id, user_id, push_token_id, status)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (program_update_id, push_token_id) DO NOTHING`,
		programUpdateID, target.UserID, target.PushTokenID, status)
}

func programCapExceeded(ctx context.Context, pool *pgxpool.Pool, userID string) bool {
	if userID == "" {
		return false
	}
	var day, hour int
	err := pool.QueryRow(ctx, `
		SELECT
		  COALESCE((SELECT count(DISTINCT d.alert_id) FROM alert_deliveries d
		            JOIN alerts a ON a.id=d.alert_id
		            WHERE d.user_id=$1 AND d.status IN ('queued','sent')
		              AND a.severity <> 'Extreme'
		              AND d.created_at > now() - interval '24 hours'), 0)
		  + COALESCE((SELECT count(DISTINCT pd.program_update_id) FROM program_update_deliveries pd
		              WHERE pd.user_id=$1 AND pd.status IN ('queued','sent')
		                AND pd.created_at > now() - interval '24 hours'), 0),
		  COALESCE((SELECT count(DISTINCT d.alert_id) FROM alert_deliveries d
		            JOIN alerts a ON a.id=d.alert_id
		            WHERE d.user_id=$1 AND d.status IN ('queued','sent')
		              AND a.severity <> 'Extreme'
		              AND d.created_at > now() - interval '1 hour'), 0)
		  + COALESCE((SELECT count(DISTINCT pd.program_update_id) FROM program_update_deliveries pd
		              WHERE pd.user_id=$1 AND pd.status IN ('queued','sent')
		                AND pd.created_at > now() - interval '1 hour'), 0)`,
		userID).Scan(&day, &hour)
	if err != nil {
		return false
	}
	return day >= 4 || hour >= 2
}
