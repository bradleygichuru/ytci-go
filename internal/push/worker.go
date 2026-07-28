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
	go w.listenPGNotify(ctx)
	go w.cleanupTicker(ctx)
	slog.Info("push worker started with pg_notify listener")
}

func (w *Worker) Stop() {
	close(w.done)
}

func (w *Worker) listenPGNotify(ctx context.Context) {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		slog.Error("push worker: acquire connection", "error", err)
		return
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, "LISTEN push_scheduled")
	if err != nil {
		slog.Error("push worker: listen push_scheduled", "error", err)
		return
	}
	slog.Info("push worker: listening on push_scheduled")

	for {
		select {
		case <-w.done:
			return
		case <-ctx.Done():
			return
		default:
			notification, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				time.Sleep(5 * time.Second)
				continue
			}
			slog.Info("push worker: received notification", "channel", notification.Channel, "payload", notification.Payload)
			w.processScheduled(ctx)
		}
	}
}

func (w *Worker) cleanupTicker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
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
		`SELECT id, title, body, COALESCE(image_url, ''), COALESCE(data, ''), COALESCE(target_audience, '')
		 FROM push_notifications
		 WHERE scheduled_at <= now() AND status = 'scheduled'
		 ORDER BY scheduled_at
		 LIMIT 5
		 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		slog.Error("push worker: query scheduled", "error", err)
		return
	}
	defer rows.Close()

	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.title, &j.body, &j.imageURL, &j.data, &j.targetAudience); err != nil {
			slog.Error("push worker: scan", "error", err)
			continue
		}
		jobs = append(jobs, j)
	}

	for _, j := range jobs {
		w.sendOne(ctx, j)
	}
}

func (w *Worker) sendOne(ctx context.Context, j job) {
	_, err := w.pool.Exec(ctx,
		`UPDATE push_notifications SET status = 'sending' WHERE id = $1`, j.id)
	if err != nil {
		slog.Error("push worker: mark sending", "id", j.id, "error", err)
		return
	}

	tokens := w.resolveTokens(ctx)
	if len(tokens) == 0 {
		w.pool.Exec(ctx,
			`UPDATE push_notifications SET status = 'sent', sent_at = now(), recipient_count = 0 WHERE id = $1`, j.id)
		return
	}

	messages := make([]ExpoPushMessage, len(tokens))
	for i, tok := range tokens {
		msg := ExpoPushMessage{To: tok, Title: j.title, Body: j.body, Sound: "default"}
		if j.data != "" {
			var dataMap map[string]any
			if err := json.Unmarshal([]byte(j.data), &dataMap); err == nil {
				msg.Data = dataMap
			}
		}
		messages[i] = msg
	}

	result, err := w.client.SendMessages(ctx, messages)
	if err != nil {
		slog.Error("push worker: send", "id", j.id, "error", err)
		w.pool.Exec(ctx, `UPDATE push_notifications SET status = 'failed' WHERE id = $1`, j.id)
		return
	}

	summaryErrMsgs := make([]string, len(result.Errors))
	copy(summaryErrMsgs, result.Errors)

	w.pool.Exec(ctx,
		`UPDATE push_notifications SET status = 'sent', sent_at = now(), recipient_count = $2 WHERE id = $1`,
		j.id, result.Sent)
	slog.Info("push sent", "id", j.id, "sent", result.Sent, "failed", result.Failed, "errors", summaryErrMsgs)

	for token := range result.TokenErrors {
		if _, err := w.pool.Exec(ctx,
			`UPDATE push_tokens SET is_active = false WHERE token = $1`, token); err != nil {
			slog.Warn("push worker: failed to deactivate token", "token", token, "error", err)
		}
	}
}

func (w *Worker) resolveTokens(ctx context.Context) []string {
	tokens, err := ResolveActiveTokens(ctx, w.pool)
	if err != nil {
		slog.Error("push worker: resolve tokens", "error", err)
		return nil
	}
	return tokens
}

type job struct {
	id             string
	title          string
	body           string
	imageURL       string
	data           string
	targetAudience string
}
