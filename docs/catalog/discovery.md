# Google Places discovery adapter

The discovery adapter fills `composer_unverified_candidates` for counties and
route corridors. It never writes to `destinations`; the only path into the
curated catalog is explicit curation.

Code: `internal/discovery`
Migration: `testdata/migrations/0019_discovery_runs.sql`

## County search plan

`CountySearchPlan` generates points inside the county polygon:

| County area | Points | Radius |
|---|---|---|
| < 2,500 km² | 1 | ~15 km |
| < 10,000 km² | 4 | proportional |
| < 30,000 km² | 6 | proportional |
| ≥ 30,000 km² | 9 | capped at 50 km |

Examples: Nairobi 1 point, Kilifi 6, Turkana 9, Marsabit 9. Each point is a
separate Nearby Search request; results are deduped by `place_id`.

## Route corridor

`DiscoverRoute(ctx, waypoints, radiusMeters)` searches each waypoint and
dedupes. The composer engine supplies waypoints from the routing provider;
discovery never computes routes itself.

## Filtering

- Controlled taxonomy via `catalog.ClassifyGoogleTypes`.
- Hard rejection of places of worship, offices, premises, and administrative
  areas; a conservative name denylist catches churches, guest houses, cyber
  cafés, stages, and similar services whose Google types are too generic.
- Non-operational businesses are skipped.
- The included type list is the high-signal nature/culture/beach set; it stays
  under Google's 50-type limit.
- Invalid or unsupported Google types are excluded from the request lists.

## Cache and quota

`composer_discovery_runs` stores one row per UTC date bucket and scope:

- `county` scopes are keyed by county code.
- `route` scopes are keyed by a SHA-256 of waypoints + radius.
- A `success` row is reused for the rest of the day; `failed` runs can retry.
- `request_count` accumulates every attempt that day for quota accounting; `candidate_place_ids` reloads the cached candidates.
- `PLACES_DISCOVERY_DAILY_BUDGET` defaults to 50 requests/day. A run that
  would exceed the budget fails with `ErrQuotaExceeded` before calling Google.
  `NewAdapter(pool, client, budget)` also accepts an explicit budget.

Candidates expiring after 30 days are excluded by `composer_catalog`.

## Photos, rating, hours, address

The adapter carries rating, review count, price level, address, opening hours,
and a hero photo when Google returns them. Photos are stored as a relative
proxy path:

```
/v1/mobile/places/photo?ref=<photo resource name>
```

The backend adds the API key and streams the image; clients never see the key.
The current API key/project returns no `photos` field for the tested places
(Fort Jesus, Dodori, etc.), so `hero_image_url` remains null there. The
composer should use a placeholder until the project's Photos access returns
data. Nothing is fabricated.

## Live smoke

```bash
set -a; source /home/brad/code/ytci-go/.env.local; set +a
LIVE_PLACES_SMOKE=1 go test -tags live ./internal/discovery \
  -run TestLiveDiscoverySmoke -count=1 -v
```

Observed on 2026-11-14 (test database, real Google API):

| County | Requests | Candidates |
|---|---|---|
| Turkana (023) | 9 | 148 |
| Kilifi (003) | 6 | 92 |
| Lamu (005) | 4 | 13 |

## Tests

Mocked tests cover the search plan, taxonomy/name filtering, non-operational
places, curated dedupe, daily cache, quota exhaustion, route dedupe, photo
proxy paths, and the no-draft guarantee. The live smoke test is opt-in.
