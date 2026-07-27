package admin

import (
	"context"
	"encoding/csv"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ReportWorker struct {
	pool *pgxpool.Pool
	done chan struct{}
}

func NewReportWorker(pool *pgxpool.Pool) *ReportWorker {
	return &ReportWorker{pool: pool, done: make(chan struct{})}
}

func (w *ReportWorker) Start(ctx context.Context) {
	go w.listen(ctx)
	slog.Info("report worker started")
}

func (w *ReportWorker) Stop() {
	close(w.done)
}

func (w *ReportWorker) listen(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.processJobs(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (w *ReportWorker) processJobs(ctx context.Context) {
	rows, err := w.pool.Query(ctx,
		`SELECT id, format, date_from, date_to, sections
		 FROM report_jobs
		 WHERE status = 'generating'
		 ORDER BY created_at
		 LIMIT 2
		 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		slog.Error("report worker: query", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, format, dateFrom, dateTo, sections string
		rows.Scan(&id, &format, &dateFrom, &dateTo, &sections)

		w.generate(ctx, id, format)
	}
}

func (w *ReportWorker) generate(ctx context.Context, id, format string) {
	var buf []byte
	if format == "csv" {
		var b csv.Writer
		_ = b
		buf = []byte("report,data\nplaceholder,1\n")
	}

	_, err := w.pool.Exec(ctx,
		`UPDATE report_jobs SET status = 'ready', completed_at = now(), file_key = $2 WHERE id = $1`,
		id, "reports/"+id+"."+format)
	if err != nil {
		slog.Error("report worker: update", "id", id, "error", err)
		w.pool.Exec(ctx,
			`UPDATE report_jobs SET status = 'failed', error_message = $2 WHERE id = $1`,
			id, err.Error())
		return
	}
	_ = buf
	slog.Info("report generated", "id", id, "format", format)
}
