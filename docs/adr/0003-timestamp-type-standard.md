# ADR-0003: Standardize timestamp scanning with pgtype.Timestamp

## Status
Accepted

## Context
Hand-written SQL queries in the expo and admin packages need to scan PostgreSQL `timestamp` columns into Go structs for JSON serialization. The codebase had three competing patterns:

| Pattern | Example | How it works |
|---|---|---|
| A: `::text` SQL cast | `admin/challenges.go`, `admin/events.go` | SQL: `created_at::text` → column arrives as text → scan into `string` |
| B: `pgtype.Timestamp` | sqlc-generated code, `expo/comments.go` | Scan into `pgtype.Timestamp`, format with `.Time.Format(time.RFC3339)` |
| C: `time.Time` | `admin/stories.go`, `admin/media.go` | Scan into `time.Time`, format with `.Format(time.RFC3339)` |

Additionally, several handlers declared `CreatedAt string` and scanned a raw `timestamp` column directly into it — a combination that silently fails because pgx cannot decode binary timestamp (OID 1114) into `*string`.

## Decision
All hand-written SQL queries in the expo package must scan timestamp columns into `pgtype.Timestamp` intermediaries, then format to RFC 3339 strings before JSON serialization.

```go
var createdAt pgtype.Timestamp
rows.Scan(..., &createdAt, ...)
i.CreatedAt = createdAt.Time.Format(time.RFC3339)
```

## Considered Options

1. **`created_at::text` SQL cast** (Pattern A) — smallest SQL diff, but PostgreSQL's `::text` on `timestamp` produces `2024-01-15 14:30:00` (space-separated, no timezone), not RFC 3339. Format depends on `DateStyle` setting. Loses type safety.

2. **`pgtype.Timestamp` intermediary** (Pattern B, chosen) — zero SQL changes, format control in Go (guaranteed RFC 3339), NULL-safe via `.Valid`, matches sqlc-generated code and `expo/comments.go`.

3. **`time.Time` intermediary** (Pattern C) — standard Go type, simpler for NOT NULL columns. Doesn't handle NULL gracefully (needs `*time.Time`). Less aligned with the pgx/sqlc ecosystem.

## Consequences
- **Positive**: Consistent pattern across all hand-written queries; format guaranteed RFC 3339 regardless of PostgreSQL `DateStyle`; NULL-safe via `.Valid`; zero SQL changes needed.
- **Positive**: Matches what sqlc generates and what `expo/comments.go` already does — proven in the same package.
- **Negative**: Slightly more verbose than raw `string` scanning (one extra variable + format call per scan).
- **Trade-off**: For NOT NULL columns like `created_at`, `time.Time` would be simpler. We accept `pgtype.Timestamp` for consistency with the sqlc ecosystem and future-proofing against nullable columns.
