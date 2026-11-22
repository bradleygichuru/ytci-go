# Competition and award domain API

Ticket: #31, map #18. Status: foundation, not launched.

## Route-agnostic by design

The legal route is a client and legal gate. The domain supports all three
structuring routes without code changes:

| Route | `promotion_type` | Award basis | Public vote |
| --- | --- | --- | --- |
| A. Licensed prize competition | `prize_competition` | Panel plus weighted public vote | `weighted` |
| B. Award programme (chosen) | `award_programme` | Panel only | `people_choice_only` |
| C. Licensed operator | `licensed_operator` | Contract-defined | Any |

Defaults encode Route B: `award_programme`, `people_choice_only`, no prizes.
`weighted` remains available in the schema and scorer for a future licensed
route, but it is never the default and is never blended into award scores while
the mode is `people_choice_only`.

A written Gambling Regulatory Authority confirmation remains a launch gate. No
endpoint blocks on it, and no public rules or marketing copy is published from
this service.

## Configuration, not constants

These live on `competition_editions` and are never hardcoded in handlers or
tests:

- `min_age`, `max_age` (both nullable; no legal band is assumed)
- `rules_version`
- `opens_at`, `closes_at`, `voting_opens_at`, `voting_closes_at`,
  `announcement_at`
- `status`, `promotion_type`, `public_vote_mode`, `prize_enabled`

The rulebook's policy constants that are genuinely fixed for this edition are
the vote caps (5 per category, 10 total) and the rubric weights (25/30/25/20).
They are exposed through `GET /v1/public/competition/settings`.

## Awards under Route B

Panel scores determine grants, commissions, certificates, features and
non-cash recognition. `competition_awards.award_type` keeps the three award
slots (`category_winner`, `overall`, `people_choice`); `award_form` records the
Route B form. No money moves inside the platform.

The public vote is a separate People's Choice recognition: one vote per entry,
five per category, ten total, with the same account and identity gates.

## Endpoints

Public (`/v1/public/competition`):

- `GET /settings`
- `GET /editions`, `GET /editions/{slug}`
- `GET /editions/{slug}/categories`
- `GET /editions/{slug}/entries`
- `GET /editions/{slug}/leaderboard`
- `GET /editions/{slug}/results` (requires a final snapshot)
- `GET /editions/{slug}/winners` (requires a published edition and award)

App (`/v1/mobile/competition`, authenticated):

- `GET /editions`, `GET /editions/{slug}`
- `GET /editions/{slug}/leaderboard`, `GET /editions/{slug}/results`
- `POST /editions/{slug}/votes`, `DELETE /votes/{voteId}`
- `GET /editions/{slug}/my-votes`
- `GET /judging/assignments`
- `PUT /judging/assignments/{assignmentId}/entries/{entryId}/score`

Admin (`/v1/admin/competition`, competition gate):

- editions and categories CRUD
- entries CRUD and eligibility decisions
- judges, assignments and conflicts
- scores, score lock
- vote summary, review cases, invalidation
- freeze and recount
- results preview
- awards CRUD and publish
- consultation response list and CSV export

## Integrity rules

- Vote ledger is append-only at the API boundary. No handler deletes a vote;
  invalidation stamps `invalidated_at`, `invalidated_by` and `reason_code`.
  Editions, categories and entries with vote history cannot be deleted.
  Account erasure still cascades from the user row, which is a deliberate
  privacy exception to ledger retention (revisit with counsel).
- Idempotency keys are unique. A replay returns the original vote; a different
  voter reusing a key is rejected.
- Account must exist before `voting_opens_at`, have a verified email, and be
  inside the voting window.
- Self-votes, team-member votes and judges voting in their own category are
  blocked. Team membership reads `userId`, `user_id`, `id` or `email` from the
  entry's `team_members` JSON.
- Freeze writes a final per-category snapshot plus an overall snapshot, records
  a ledger hash, closes the edition and locks all scores.
- Public results and winners never expose voter identities, emails, phones,
  team rosters or per-judge scores.

## Follow-ups

- Public entry submission flow and consultation response capture (public site
  ticket), including guardian consent for minors.
- Vote signal collection (device and IP corroboration) and the two-signal
  review workflow UI. This ticket ships the cases, thresholds and invalidation.
- Durable audit table for admin actions and score corrections.
- Swahili copy and public rules publication, blocked on the legal route gate.
