# ADR-0004: Optional authentication for mobile read-only routes

## Status
Accepted

## Context

The mobile app's `/v1/mobile/*` routes were all behind `JWTAuth` middleware, which returns 401 when no token is present. This meant unauthenticated guests could not browse published content (destinations, events, stories, courses, challenges, conservation activities). The home feed and youth stories feed worked because they used `/v1/public/*` routes, but story detail pages and other read endpoints failed with 401.

The root cause: `JWTAuth` was applied to the entire `/v1` group, including mobile read-only routes that should be accessible to guests.

## Decision

Introduce an `OptionalAuth` middleware that parses JWT/session tokens when present (setting context values for downstream handlers) but passes through without 401 when no token is present. Apply it to the parent `/v1` group.

Mobile routes are split into two groups:
- **Read-only routes** (GET): `feed`, `destinations`, `destinations/{slug}`, `destinations/nearby`, `events`, `events/{id}`, `stories`, `stories/{id}`, `courses`, `courses/{id}`, `challenges`, `challenges/{id}`, `conservation`, `conservation/{id}` — use `OptionalAuth` only
- **Write/action routes** (POST, PATCH, DELETE): bucket list, profile, itineraries, media upload, story creation, likes, saves, comments, challenges join/evidence, courses enroll/quiz — remain behind `AuthGate`

Admin routes (`/v1/admin/*`) get `JWTAuth` + `AdminGate` applied explicitly at the group level.

```go
// server.go
r.Route("/v1", func(sub chi.Router) {
    sub.Use(middleware.OptionalAuth(jwks, cfg.JWTExpectedIss, cfg.JWTExpectedAud))
    mountAdminRoutes(sub, h, r2client, jwks, cfg.JWTExpectedIss, cfg.JWTExpectedAud)
    mountMobileRoutes(sub, h, r2client, authLimiter)
})
```

## Considered Options

1. **`OptionalAuth` at parent level** (chosen) — clean separation; parent provides optional context, child groups enforce hard auth where needed. Admin routes add `JWTAuth` explicitly.

2. **Keep `JWTAuth` at parent, add `OptionalAuth` to read-only subgroup** — requires bypassing the parent's `JWTAuth` for read-only routes, which isn't possible in chi's middleware model without restructuring.

3. **Move read-only routes to `/v1/public/*`** — duplicates route definitions and conflates "public" (truly no auth) with "optional auth" (auth improves response but isn't required).

## Consequences
- **Positive**: Guests can browse all published content; authenticated users get personalized responses (isLiked, isSaved, myStories)
- **Positive**: No changes needed to existing handler code — context values are populated when token is present
- **Positive**: Admin routes remain fully protected with hard auth
- **Negative**: Slight increase in middleware chain depth for `/v1` routes (negligible performance impact)
