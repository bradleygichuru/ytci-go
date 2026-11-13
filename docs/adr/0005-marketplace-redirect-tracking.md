# ADR-0005: Allow anonymous outbound redirect tracking

## Status

Accepted

## Context

The Marketplace, Stays, and eSIM surfaces are guest-accessible. The app needs to know which surfaces drive outbound handoffs so the programme can judge partner fit and future live-rate work. ADR-0004 says write/action routes remain behind `AuthGate`, but requiring sign-in to record a handoff would either lose guest data or block the handoff itself, which must open the partner site regardless of tracking.

## Decision

`POST /v1/mobile/marketplace/redirects` is the one write route allowed in the `OptionalAuth` group.

- If a session is present, the handler records `user_id`; otherwise it records `NULL`.
- It stores only `entity_type`, `entity_id`, `source`, `platform`, and `created_at`.
- It does **not** store the URL, query string, email, name, IP address, or any free-text user input.
- It validates that the target entity exists and is published before inserting.
- The mobile client fires the request without awaiting it; a tracking failure never blocks opening the partner site.
- The endpoint is rate-limited with the existing mobile authenticated limiter.

## Considered Options

1. **Optional-auth tracking** (chosen) — preserves guest analytics without gating the handoff; stores no user content.
2. **Require `AuthGate`** — cleanly follows ADR-0004, but loses guest handoff data and adds friction before an external redirect.
3. **Client-only analytics** — the existing `analytics_events` table requires a `user_id`, so guest handoffs would be invisible.

## Consequences

- **Positive:** guest handoff data is measurable; no PII is stored; the handoff is never blocked by tracking.
- **Positive:** the exception is narrow and documented rather than an undeclared write route.
- **Negative:** anonymous callers are keyed on an empty user id by the current limiter; if abuse appears, add an IP-keyed limiter for this route.
- **Negative:** redirect rows are not user content and must not be treated as evidence of a completed booking.
