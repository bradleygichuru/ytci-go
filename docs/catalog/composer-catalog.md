# Composer catalog contract

The trip composer reads candidates from `composer_catalog` and coverage from
`composer_county_coverage`. It never reads `destinations` directly for
generation, so the curated/unverified contract lives in one place.

Migration: `testdata/migrations/0018_composer_catalog.sql`
Go: `internal/catalog/composer.go`

## `composer_catalog`

Each row has explicit provenance:

| Field | Curated | Google Places |
|---|---|---|
| `provenance` | `curated` | `google_places` |
| `eligibility` | `curated_composer_ready` | `unverified_google_places` |
| `destination_id` | set | null |
| `place_id` / `address` | null | set |
| `verified_at` | set | null |
| duration | structured | null |
| fee status/amount/basis | structured | null |
| opening hours | structured | optional Google hours, status `unverified` |
| transport modes / best seasons | structured | empty |
| rating / review count / price level | null | optional |
| hero image | curated URL or bundled placeholder | optional Google photo |

Curated rows must be `published`, `is_curated`, `verified`, and have a county
code, coordinates, taxonomy category/interests, short description, structured
duration, fee status, opening-hours status, hero attribution, source, and
`verified_at`. Unpublished, uncurated, or incomplete rows are excluded.

Unverified rows come from `composer_unverified_candidates`, not from
`destinations`. Discovery fills that table; it never creates catalog drafts.
See `docs/catalog/discovery.md` for the county/route search plan, quota, and
live counts. Rows expire after 30 days. Google photo refs are stored as
`/v1/mobile/places/photo?ref=...` and proxied server-side so clients never see
the API key. The view never exposes fees, safety, accessibility, duration, or
seasonality for Google Places candidates.

## `composer_county_coverage`

One row per county:

- `curated_count` — curated composer-ready rows.
- `unverified_count` — live Google Places candidates.
- `total_count` — both.
- `coverage_status`:
  - `not_covered` when `total_count = 0`
  - `limited` when `curated_count < 3`
  - `covered` when `curated_count >= 3`

The 10 golden fixtures span 9 counties, so those counties currently read
`limited`; the other 38 read `not_covered`.

## Filters

`catalog.ComposerFilter` extends the view with:

- `Interests` — overlap with `interest_tags`.
- `TransportMode` — one of `car`, `4x4`, `flight`, `public_transport`, `boat`,
  `walking`, `cycling`; matches `transport_modes`. Unverified rows never match.
- `Season` — one of `dry`, `green`, `migration`, `year_round`; matches
  `best_seasons` or `year_round`. Unverified rows never match.
- `CountyCodes` — restrict to selected counties.
- `Provenance` — `curated` or `google_places`.
- `BudgetBand` — entry-fee cap only, not a whole-trip budget:
  - `budget-smart` → 1,500 KES
  - `balanced` → 15,000 KES
  - `experience-first` → no cap
  - `MaxEntryFeeKes` overrides the preset.
  - USD/EUR/GBP fees convert with caller-supplied rates (defaults 130/140/165).
  - Unknown fees (`not_verified`) and Google Places rows pass the cap but stay
    visibly unverified.
- `Limit` — default 500, caller may lower.

Filtering does not score or rank. Curated rows are returned before unverified
rows, then county code and name. The composer engine owns scoring.

## Go API

```go
candidates, err := catalog.QueryComposerCandidates(ctx, pool, catalog.ComposerFilter{
    Interests: []string{"Wildlife"},
    TransportMode: "flight",
    Season: "dry",
    BudgetBand: "balanced",
})
coverage, err := catalog.QueryComposerCountyCoverage(ctx, pool)
```

Both accept `*pgxpool.Pool` or `pgx.Tx` through `catalog.Queryer`.

## Tests

`internal/catalog/composer_test.go` covers the golden fixtures, the eligibility
predicate, the unverified field contract (including expiry), all filter
families, and the `limited` / `not_covered` / `covered` coverage transitions.
