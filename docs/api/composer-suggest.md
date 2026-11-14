# Composer suggest API

`POST /v1/mobile/itineraries/suggest`

Authenticated. Returns three deterministic, unsaved alternatives. The response
does not create an itinerary; the client saves the chosen plan later.

## Request

```json
{
  "origin": {"name": "Nairobi", "latitude": -1.2921, "longitude": 36.8219},
  "days": 3,
  "interests": ["Wildlife", "Culture"],
  "budgetBand": "mid-range",
  "budgetPerPersonKes": 60000,
  "groupType": "couple",
  "groupSize": 2,
  "transport": "car",
  "pace": "balanced",
  "startDate": "2026-11-20",
  "mustInclude": ["maasai-mara"],
  "returnToOrigin": true,
  "allowUnverified": false,
  "accessibility": [],
  "regenerateCount": 0
}
```

| Field | Rules |
|---|---|
| `origin` | Name or lat/lng. Must resolve inside Kenya. |
| `days` | 1–14. |
| `interests` | 1–8; accepts the eight brief interests plus taxonomy aliases (`Beaches`→`Beach`, `Adventure`→`Activity`). |
| `budgetBand` | `budget`, `mid-range`, `luxury`. |
| `budgetPerPersonKes` | Optional positive hard cap. |
| `groupType` / `groupSize` | `solo`/`couple`/`friends`/`family`; 1–20; solo=1, couple=2. |
| `transport` | `car`, `public`, `tour_van`; default `car`. |
| `pace` | `relaxed`, `balanced`, `packed`; default `balanced`. |
| `startDate` | Optional future `YYYY-MM-DD`. Omitted = flexible; weather/events are omitted. |
| `mustInclude` | Up to 3 unique destination ids, slugs, or names. |
| `returnToOrigin` | Default `true`. |
| `allowUnverified` | Required when the origin county has zero curated candidates. |
| `regenerateCount` | Non-negative; changes the deterministic seed. |

Validation failures return `400` with `error.code = "INVALID_BRIEF"` and a
`fields` array of `{field, code, message}`.

Rate limit: **10 suggestions/user/day**. The 11th returns `429` with
`error.code = "RATE_LIMITED"` and `retryAfter` seconds. Cached suggestions
still consume one token. The 3 regenerations/plan/day limit applies once a
plan is saved; this endpoint is the unsaved-suggestion path.

## Response

```json
{
  "suggestions": [
    {
      "id": "6130d16e3c3f5723",
      "profile": "balanced",
      "status": "limited",
      "title": "Balanced: 3 days from Nairobi",
      "summary": "4 stops across 3 days, 6h 10m travel, 3 curated / 1 unverified, estimated KES 84200.",
      "seed": "8c9f…",
      "matchLabel": "Good",
      "days": [
        {
          "day": 1,
          "date": "2026-11-20",
          "base": "Nairobi",
          "travelMinutes": 29,
          "stops": [
            {
              "destinationId": "…",
              "name": "Karura Forest",
              "provenance": "curated",
              "category": "Wildlife",
              "interests": ["Wildlife", "Hiking", "Conservation"],
              "countyCode": "047",
              "county": "Nairobi",
              "latitude": -1.2333,
              "longitude": 36.8333,
              "arrive": "08:29",
              "depart": "09:59",
              "durationMinutes": 90,
              "travelFromPreviousMinutes": 29,
              "estimatedTravel": true,
              "feeStatus": "verified",
              "feeAmountKes": 116,
              "openingHoursStatus": "verified",
              "rationale": [
                {"code": "CURATED_PROVENANCE", "text": "YTCI-verified destination."},
                {"code": "INTEREST_MATCH", "text": "Matches a trip interest.", "params": {"interest": "Wildlife"}}
              ],
              "warnings": []
            }
          ],
          "weather": {"date": "2026-11-20", "horizon": "forecast", "precipProbability": 10, "tempMaxC": 26, "source": "Open-Meteo ECMWF IFS"}
        }
      ],
      "estimatedBudget": {
        "currency": "KES",
        "estimated": true,
        "note": "Composer estimate. Transport, food, accommodation, and activity costs are band estimates, not live quotes.",
        "totalKes": 84200,
        "perPersonKes": 42100,
        "breakdown": {
          "Transport": 0,
          "Accommodation": 0,
          "Food": 0,
          "Park & entry fees": 0,
          "Activities": 0,
          "Shopping": 0,
          "Conservation": 0,
          "Other": 0
        }
      },
      "warnings": [],
      "coverage": [],
      "totalTravelMinutes": 370,
      "curatedCount": 3,
      "unverifiedCount": 1
    }
  ],
  "coverage": [],
  "meta": {
    "engineVersion": "composer-engine-v1",
    "catalogVersion": "…",
    "inputHash": "…",
    "generatedAt": "2026-11-14T12:00:00Z",
    "cached": false,
    "suggestionLimit": 10,
    "suggestionsUsed": 1
  }
}
```

### Profiles

- `budget-smart` — budget 0.30, travel 0.25, interest 0.20, quality 0.10, weather 0.08, diversity 0.04, event 0.03.
- `balanced` — interest 0.30, travel 0.20, quality 0.15, budget 0.15, weather 0.10, diversity 0.05, event 0.05.
- `experience-first` — interest 0.35, quality 0.20, travel 0.15, weather 0.10, diversity 0.08, event 0.07, budget 0.05.

Quality: curated 1.0; unverified rating ≥4.5 with ≥100 reviews 0.70; ≥4.0
0.55; any rating 0.45; no rating 0.40. Match labels: Strong ≥0.75, Good
0.55–0.74, Stretch <0.55. No percentage is exposed.

### Status

- `ok` — the plan meets the pace target.
- `limited` — a day is empty/transfer-only, coverage is thin, or fewer stops fit.
- `blocked` — a hard constraint cannot be met (budget cap, must-include,
  curated requirement, no feasible candidate).

Top-level `blocked` appears when no alternative is usable:

```json
{"blocked": {"code": "UNVERIFIED_OPT_IN_REQUIRED", "message": "…", "params": {"county": "Turkana"}}}
```

## Warning codes

`LIMITED_COVERAGE`, `NOT_COVERED`, `UNVERIFIED_OPT_IN_REQUIRED`,
`UNVERIFIED_PLAN`, `UNVERIFIED_STOP`, `FEE_UNVERIFIED`, `FEE_REPORTED`,
`HOURS_UNVERIFIED`, `ROUTE_ESTIMATED`, `TRANSFER_DAY`, `LONG_TRANSFER`,
`PARTIAL_DAY`, `REST_OR_TRANSFER_DAY`, `MUST_INCLUDE_UNREACHABLE`,
`CURATED_REQUIRED`, `BUDGET_CAP_EXCEEDED`, `BUDGET_ESTIMATE`,
`PUBLIC_TRANSPORT_SCHEDULE_UNVERIFIED`, `ACCESSIBILITY_UNVERIFIED`, `WEATHER_UNAVAILABLE`,
`EVENTS_UNAVAILABLE`, `NO_FEASIBLE_ALTERNATIVES`.

Severities: `info`, `caution`, `warning`.

## Rationale codes

`CURATED_PROVENANCE`, `UNVERIFIED_PROVENANCE`, `INTEREST_MATCH`,
`TRAVEL_TIME`, `FEE_VERIFIED`, `HOURS_VERIFIED`, `WEATHER`, `EVENT`.
Clients localize by code; the `text` field is a default English fallback.

## Determinism, cache, and fallbacks

- Seed = hash(normalized brief + origin county + catalog version + engine
  version + regenerate count). The same brief and data snapshot returns the
  same plans.
- Suggestion cache: 24 hours. Routing cache: 7 days fresh / 30 days stale.
- Routing fallback: straight-line × 1.4 / 40 km/h + 15 min, labelled
  `estimatedTravel`. Public transport adds 30 min and a schedule warning.
- Weather: Open-Meteo `ecmwf_ifs`, 15-day horizon. Unavailable/out-of-range
  weather is omitted; the plan still generates.
- Events: program events in the plan counties/dates. Unavailable events are
  omitted.
- Coverage: the origin county must have candidates. `<3` curated adds
  `LIMITED_COVERAGE` and adjacent counties; `0` returns `NOT_COVERED`.
- Unverified candidates are capped at 50% per day and one per day when curated
  candidates exist. A plan with curated data must contain at least one curated
  stop. A fully unverified plan requires `allowUnverified`.
- Costs are always labelled estimates; no live prices, availability, or safety
  guarantees are claimed.

## Curl

```bash
curl -sS -X POST "$API/v1/mobile/itineraries/suggest" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"origin":{"name":"Nairobi","latitude":-1.2921,"longitude":36.8219},"days":3,"interests":["Wildlife"],"budgetBand":"mid-range","groupType":"couple","groupSize":2,"transport":"car","pace":"balanced"}'
```
