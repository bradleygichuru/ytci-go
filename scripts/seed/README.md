# Internal-test seed data

Reproducible content for the client internal test. All scripts are idempotent.

| Script | What it seeds |
|---|---|
| `big-five.sql` | Five published species with EN/SW names, prose, hero media and where-to-see rows |
| `r2-upload-big-five.sh` | Uploads the Big Five hero images to R2 |
| `marketplace.sql` | Three Gen Z stays, three partner businesses, two eSIM providers (one featured) |
| `recognition.sql` | Six badges, three published rules, one active season |
| `audio-covers.sh` | Extracts a mid-video frame for each Audio Stories pilot and uploads it as the cover |
| `mice-production.sql` | Three venues, three verified organizers and staff, eight events, agenda, speakers, registrations, ticket handoffs, tickets and check-ins (see `mice-production.md`) |

## Order

1. `R2_ACCOUNT_ID=... R2_ACCESS_KEY_ID=... R2_SECRET_ACCESS_KEY=... R2_BUCKET=ytci scripts/seed/r2-upload-big-five.sh`
2. `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/seed/big-five.sql`
3. Upload the marketplace images (same Wikimedia helper pattern) to `marketplace/**`, then `psql ... -f scripts/seed/marketplace.sql`
4. `psql ... -f scripts/seed/recognition.sql`
5. `scripts/seed/audio-covers.sh` for the four Audio Stories covers

Images are Wikimedia Commons lead images; the SQL records the credit and licence. Marketplace images use the same convention (`image_url` stores the R2 key; the API presigns it).

## MICE production data

`mice-production.sql` adds the venues, verified organizers, events, agenda,
speakers, registrations, tickets and check-ins needed to test the MICE surfaces.
It is idempotent and never deletes rows. Apply it on its own:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/seed/mice-production.sql
```

See `mice-production.md` for counts, verification queries and current schema
gaps.
