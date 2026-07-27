package push

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	pool   *pgxpool.Pool
	client *Client
	done   chan struct{}
}

func NewWorker(pool *pgxpool.Pool, client *Client) *Worker {
	return &Worker{
		pool:   pool,
		client: client,
		done:   make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	go w.listen(ctx)
	slog.Info("push notification worker started")
}

func (w *Worker) Stop() {
	close(w.done)
}

func (w *Worker) listen(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.processScheduled(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (w *Worker) processScheduled(ctx context.Context) {
	rows, err := w.pool.Query(ctx,
		`SELECT id, title, body, image_url, data, target_audience
		 FROM push_notifications
		 WHERE scheduled_at <= now() AND status = 'scheduled'
		 ORDER BY scheduled_at
		 LIMIT 5
		 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		slog.Error("push worker: failed to query scheduled", "error", err)
		return
	}
	defer rows.Close()

	type scheduled struct {
		id              string
		title           string
		body            string
		imageURL        *string
		data            *string
		targetAudience  *string
	}
	var jobs []scheduled
	for rows.Next() {
		var s scheduled
		if err := rows.Scan(&s.id, &s.title, &s.body, &s.imageURL, &s.data, &s.targetAudience); err != nil {
			slog.Error("push worker: failed to scan row", "error", err)
			continue
		}
		jobs = append(jobs, s)
	}

	for _, job := range jobs {
		_, err = w.pool.Exec(ctx,
			`UPDATE push_notifications SET status = 'sending' WHERE id = $1`, job.id)
		if err != nil {
			slog.Error("push worker: failed to mark sending", "id", job.id, "error", err)
			continue
		}

		tokens := w.resolveTokens(ctx, job.targetAudience)
		if len(tokens) == 0 {
			w.pool.Exec(ctx,
				`UPDATE push_notifications SET status = 'sent', sent_at = now(), recipient_count = 0 WHERE id = $1`, job.id)
			continue
		}

		messages := make([]ExpoPushMessage, len(tokens))
		for i, tok := range tokens {
			messages[i] = ExpoPushMessage{
				To:    tok,
				Title: job.title,
				Body:  job.body,
				Sound: "default",
			}
			if job.data != nil {
				var dataMap map[string]any
				json.Unmarshal([]byte(*job.data), &dataMap)
				messages[i].Data = dataMap
			}
		}

		result, err := w.client.SendMessages(ctx, messages)
		if err != nil {
			slog.Error("push worker: send failed", "id", job.id, "error", err)
			w.pool.Exec(ctx,
				`UPDATE push_notifications SET status = 'failed' WHERE id = $1`, job.id)
			continue
		}

		_, err = w.pool.Exec(ctx,
			`UPDATE push_notifications SET status = 'sent', sent_at = now(), recipient_count = $2 WHERE id = $1`,
			job.id, result.Sent)
		if err != nil {
			slog.Error("push worker: failed to mark sent", "id", job.id, "error", err)
		}

		slog.Info("push notification sent", "id", job.id, "sent", result.Sent, "failed", result.Failed)

		go w.recordCleanup(ctx, job.id, result)
	}
}

func (w *Worker) resolveTokens(ctx context.Context, audience *string) []string {
	query := `SELECT token FROM push_tokens WHERE is_active = true`
	rows, err := w.pool.Query(ctx, query)
	if err != nil {
		slog.Error("push worker: failed to resolve tokens", "error", err)
		return nil
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		tokens = append(tokens, t)
	}
	return tokens
}

func (w *Worker) recordCleanup(ctx context.Context, jobID string, result *SendResult) {
	for _, errMsg := range result.Errors {
		if errMsg == "DeviceNotRegistered" {
			slog.Info("push worker: cleaning up stale token")
		}
	}
}
