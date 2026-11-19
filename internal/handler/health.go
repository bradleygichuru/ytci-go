package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type healthResponse struct {
	Status string `json:"status"`
}

type readinessResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Health is the liveness probe. It only proves the process is running and able
// to answer HTTP; it deliberately does not touch the database.
func Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// Ready is the readiness probe. It reports 200 only when the database is
// reachable and the contract schema has been migrated to the version this
// binary was built against. It is mounted outside the authenticated routes so
// load balancers can call it without credentials.
func Ready(pool *pgxpool.Pool) http.HandlerFunc {
	const requiredContracts = 6 // openapi, cloudevents, roles, mice, species, analytics

	return func(w http.ResponseWriter, r *http.Request) {
		checks := map[string]string{"database": "ok", "migrations": "ok"}
		status := http.StatusOK

		if pool == nil {
			checks["database"] = "unavailable"
			checks["migrations"] = "unknown"
			writeJSON(w, http.StatusServiceUnavailable, readinessResponse{
				Status: "unavailable", Checks: checks,
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := pool.Ping(ctx); err != nil {
			slog.Warn("readiness: database unreachable", "error", err)
			checks["database"] = "unavailable"
			checks["migrations"] = "unknown"
			writeJSON(w, http.StatusServiceUnavailable, readinessResponse{
				Status: "unavailable", Checks: checks,
			})
			return
		}

		// platform_contract_versions is the newest migration slice (0069). Its
		// presence, and a complete 1.0.0 set, means the runtime schema has been
		// migrated at least as far as this binary expects.
		var contracts int
		err := pool.QueryRow(ctx, `
			SELECT count(*) FROM platform_contract_versions WHERE version = '1.0.0'`).
			Scan(&contracts)
		switch {
		case err != nil:
			slog.Warn("readiness: contract schema missing", "error", err)
			checks["migrations"] = "missing"
			status = http.StatusServiceUnavailable
		case contracts < requiredContracts:
			slog.Warn("readiness: contract schema incomplete", "contracts", contracts)
			checks["migrations"] = "incomplete"
			status = http.StatusServiceUnavailable
		}

		state := "ok"
		if status != http.StatusOK {
			state = "unavailable"
		}
		writeJSON(w, status, readinessResponse{Status: state, Checks: checks})
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
