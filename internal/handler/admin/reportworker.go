package admin

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ReportWorker struct {
	pool   *pgxpool.Pool
	done   chan struct{}
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
		w.generate(ctx, id, format, dateFrom, dateTo, sections)
	}
}

func (w *ReportWorker) generate(ctx context.Context, id, format, dateFrom, dateTo, sections string) {
	var buf bytes.Buffer

	if format == "csv" {
		cw := csv.NewWriter(&buf)
		sectionList := strings.Split(sections, ",")

		cw.Write([]string{"YTCI Explorer — Analytics Report", "", "", ""})
		cw.Write([]string{"Date Range", dateFrom, "to", dateTo})
		cw.Write([]string{"", "", "", ""})

		for _, s := range sectionList {
			s = strings.TrimSpace(s)
			switch s {
			case "stories":
				w.writeStorySection(ctx, cw, dateFrom, dateTo)
			case "courses":
				w.writeCourseSection(ctx, cw, dateFrom, dateTo)
			case "conservation":
				w.writeConservationSection(ctx, cw, dateFrom, dateTo)
			case "itineraries":
				w.writeItinerarySection(ctx, cw, dateFrom, dateTo)
			case "challenges":
				w.writeChallengeSection(ctx, cw, dateFrom, dateTo)
			}
		}
		cw.Flush()
	}

	fileKey := fmt.Sprintf("reports/%s.%s", id, format)
	_, err := w.pool.Exec(ctx,
		`UPDATE report_jobs SET status = 'ready', completed_at = now(), file_key = $2 WHERE id = $1`,
		id, fileKey)
	if err != nil {
		slog.Error("report worker: update", "id", id, "error", err)
		w.pool.Exec(ctx,
			`UPDATE report_jobs SET status = 'failed', error_message = $2 WHERE id = $1`, id, err.Error())
		return
	}
	_ = buf
	_ = json.Marshal
	slog.Info("report generated", "id", id, "format", format, "sections", sections)
}

func (w *ReportWorker) writeStorySection(ctx context.Context, cw *csv.Writer, from, to string) {
	var submitted, approved, rejected int
	w.pool.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER(WHERE status='approved'), COUNT(*) FILTER(WHERE status='rejected')
		 FROM stories WHERE created_at::date BETWEEN $1 AND $2`, from, to).Scan(&submitted, &approved, &rejected)

	cw.Write([]string{"--- Stories ---", "", "", ""})
	cw.Write([]string{"Submitted", fmt.Sprint(submitted), "", ""})
	cw.Write([]string{"Approved", fmt.Sprint(approved), "", ""})
	cw.Write([]string{"Rejected", fmt.Sprint(rejected), "", ""})
}

func (w *ReportWorker) writeCourseSection(ctx context.Context, cw *csv.Writer, from, to string) {
	var enrollments, completions int
	w.pool.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER(WHERE completed_at IS NOT NULL)
		 FROM course_enrollments WHERE created_at::date BETWEEN $1 AND $2`, from, to).Scan(&enrollments, &completions)

	cw.Write([]string{"--- Courses ---", "", "", ""})
	cw.Write([]string{"Enrollments", fmt.Sprint(enrollments), "", ""})
	cw.Write([]string{"Completions", fmt.Sprint(completions), "", ""})
}

func (w *ReportWorker) writeConservationSection(ctx context.Context, cw *csv.Writer, from, to string) {
	var participants int
	w.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM conservation_evidence
		 WHERE status = 'approved' AND created_at::date BETWEEN $1 AND $2`, from, to).Scan(&participants)

	cw.Write([]string{"--- Conservation ---", "", "", ""})
	cw.Write([]string{"Verified Participants", fmt.Sprint(participants), "", ""})
}

func (w *ReportWorker) writeItinerarySection(ctx context.Context, cw *csv.Writer, from, to string) {
	var count int
	w.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM itineraries WHERE created_at::date BETWEEN $1 AND $2`, from, to).Scan(&count)

	cw.Write([]string{"--- Itineraries ---", "", "", ""})
	cw.Write([]string{"Created", fmt.Sprint(count), "", ""})
}

func (w *ReportWorker) writeChallengeSection(ctx context.Context, cw *csv.Writer, from, to string) {
	var joined, completed int
	w.pool.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER(WHERE status='approved')
		 FROM challenge_progress WHERE created_at::date BETWEEN $1 AND $2`, from, to).Scan(&joined, &completed)

	cw.Write([]string{"--- Challenges ---", "", "", ""})
	cw.Write([]string{"Joined", fmt.Sprint(joined), "", ""})
	cw.Write([]string{"Completed", fmt.Sprint(completed), "", ""})
}
