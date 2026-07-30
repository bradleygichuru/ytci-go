# Log underlying errors on the internal-failure path

When a handler hits an unexpected error, `handler.WriteError` writes a sanitized JSON envelope to the client (status code + generic message) and **discards the underlying Go `err`**. Every internal failure therefore surfaces as "failed to create story" / "failed to get stops" with zero server-side diagnostics — the cause of the 2025-07-30 story-creation and itinerary-stops crashes was invisible until manual reproduction.

We add a new `handler.WriteServerError(w, r, code, message, err)` used **only** for 500-level (genuine internal failure) paths. It logs the underlying `err` via `slog.Error` with the request id (pulled from the request context) *before* writing the same sanitized client envelope. The existing `handler.WriteError(w, status, code, message)` is left untouched for 4xx client-error paths, where there is usually no diagnostic `err` worth logging. The client envelope stays sanitized in both cases (no internal leakage); the server log now records the real cause for the failures that need it.

## Considered Options

- **New `WriteServerError` for 500 paths only** (chosen) — one place to thread `slog.Error` + request id; no 4xx call site needs touching; the ~265 existing `WriteError` sites keep working. Costs a second helper in the errors package and migrating only the internal-failure call sites.
- **Add `err error` to `WriteError` and migrate every call site** — single helper, uniform shape. Costs a signature change across ~265 call sites, most of which are 4xx with nothing to log. High churn for low marginal value.
- **Per-handler `slog.Error` at each 500 call site** — no new helper, but easy to forget (which is exactly what happened) and duplicates the request-id wiring everywhere.