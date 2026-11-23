# MICE production seed

`mice-production.sql` fills the public-testing gaps in the deployed MICE
vertical. It is additive and idempotent: rerunning it upserts the same rows and
never deletes data.

## What it creates

| Surface | Rows | Notes |
| --- | --- | --- |
| Venues | 3 | KICC (Nairobi), Sarova Whitesands (Mombasa), Mara Serena conference centre (Narok) |
| Venue accessibility | 3 | Tri-state fields, companion policy, evidence note, verified date |
| Organizer profiles | 3 | All `verified`; one programme partner and two verified organizers |
| Organizer staff | 5 | Scanners and managers across the three organizers |
| Events | 8 | Draft, published upcoming/ongoing/ended, and cancelled; free RSVP and paid handoff |
| Event accessibility | 8 | One row per seeded event |
| Agenda sessions | 9 | Includes two overlapping sessions in Lenana Hall as a deliberate conflict |
| Speakers | 6 people / 15 session links | Linked through `session_speakers` |
| Registrations | 14 | Includes a waitlisted attendee, a cancellation, and access needs in `answers` |
| Ticket handoffs | 7 | Paid events record interest only, per the MICE contract |
| Tickets | 2 | QR ticket rows for imported paid delegates |
| Check-ins | 2 | One event (Mombasa forum): one `qr`, one `manual`; a third delegate is left unchecked for admin testing |

Existing users are referenced by email, so the seed skips a staff/registration
row if an account is missing rather than failing. It uses fixed UUIDs for
events, sessions, people and staff, and natural keys (`venues.slug`,
`organizer_profiles.slug`, `(event_id, user_id)`, `registration_id`) elsewhere.

## Apply

```bash
cd /path/to/ytci-go
set -a && source .env.local && set +a
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/seed/mice-production.sql
```

The file manages its own transaction. Do not add `-1`, which produces a nested
transaction warning.

To validate without committing, replace the final `COMMIT;` with `ROLLBACK;`
and pipe the file to `psql -f -`.

## Verify

```sql
SELECT 'venues', count(*) FROM venues
UNION ALL SELECT 'organizer_profiles', count(*) FROM organizer_profiles
UNION ALL SELECT 'events', count(*) FROM events
UNION ALL SELECT 'event_sessions', count(*) FROM event_sessions
UNION ALL SELECT 'event_people', count(*) FROM event_people
UNION ALL SELECT 'event_registrations', count(*) FROM event_registrations
UNION ALL SELECT 'event_tickets', count(*) FROM event_tickets
UNION ALL SELECT 'event_checkins', count(*) FROM event_checkins;

SELECT status, count(*) FROM events GROUP BY status ORDER BY status;
```

Published set: `GET /v1/public/events?limit=50` returns all non-invite-only
published/scheduled/postponed/cancelled events. The seeded draft is intentionally
absent.

## Known schema gaps

- **No ticket-tier table.** The deployed schema models one price per event
  (`price_amount`, `price_currency`, `ticket_url`, `sales_end`). The paid events
  describe their tiers in the description and use the organizer handoff price.
  There is no admin endpoint to create a separate tier.
- **Event create is blocked in production.** `POST /v1/admin/events` returns
  `500` because the deployed `events` table lacks the `highlights` column that
  the insert path writes. Tracked in
  [ytci-go#73](https://github.com/bradleygichuru/ytci-go/issues/73). Event update
  and every other MICE admin write used by this seed verified successfully.
