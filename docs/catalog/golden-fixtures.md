# Golden destination fixtures

The 10 Home adventure carousel destinations are the first fully curated
catalog seed and the golden fixtures for backend/mobile tests.

Migration: `testdata/migrations/0017_golden_destination_fields.sql`
Seed: `testdata/seeds/golden_destinations.sql`
Runner: `DATABASE_URL=... scripts/seed-golden-destinations.sh`

The seed is idempotent, upserts by slug, archives uncurated aliases/duplicates,
and self-checks that all 10 are published and span 9 counties.

## Curator confirmation

This is a HITL seed. The program curator confirms the facts below before
production publish. Unknown facts are deliberately marked `not_verified` or
`unverified`; they are not guesses.

| Destination | County | Category | Duration | Fee status | Opening status | Hero credit | Main source |
|---|---|---|---|---|---|---|---|
| Hell's Kitchen | Kilifi | Sightseeing | 120–180 min | reported KES 200 + guide | unverified | YTCI | YTCI / Green Heart Kilifi / community |
| Chalbi Desert | Marsabit | Activity | 240–480 min | free (no gate) | verified: always open | YTCI | YTCI / northern Kenya guides |
| Lake Paradise | Marsabit | Wildlife | 120–240 min | verified KWS USD 40 adult | unverified | YTCI | KWS Oct 2025 fees |
| Lake Turkana | Turkana | Sightseeing | 240–480 min | not verified | unverified | YTCI | UNESCO / YTCI / guides |
| Fort Jesus | Mombasa | Culture | 60–120 min | verified NMK KES 550 adult | verified 08:30–17:30 daily | The Wandering Angel · CC BY 2.0 | NMK 2026 rates |
| Diani Beach | Kwale | Beach | 180–480 min | free public beach; marine park USD 25 | verified: public beach | Wedding-Hendrik · CC BY-SA 4.0 | KWS Oct 2025 / KTB |
| Maasai Mara | Narok | Wildlife | 480–1440 min | verified USD 100 low / 200 high | unverified | Daniel Case · CC BY-SA 4.0 | Narok County 2025/26 |
| Amboseli | Kajiado | Wildlife | 480–1440 min | verified KWS USD 90 adult | unverified | Ninaras · CC BY 4.0 | KWS Oct 2025 |
| Lake Nakuru | Nakuru | Wildlife | 180–360 min | verified KWS USD 90 adult | unverified | Thomas Fuhrmann · CC BY-SA 4.0 | KWS Oct 2025 |
| Karura Forest | Nairobi | Wildlife | 120–300 min | verified KES 116 citizen adult | verified 06:00–18:00 daily | Eva Ayub · CC BY-SA 4.0 | Friends of Karura 2025/26 |

## Fee sources

- **KWS** — [Conservation Fee October 2025](https://kws.go.ke/wp-content/uploads/2026/05/KWS-Conservation-Fee-October-2025.pdf):
  Amboseli and Lake Nakuru are premium parks (non-resident adult USD 90/day);
  Marsabit is a special-interest park (non-resident adult USD 40/day);
  Diani-Chale marine park is USD 25 non-resident adult/day.
- **Narok County** — [2025/26 Park Fees & Charges](https://narok.go.ke/wp-content/uploads/2026/06/FS-25-26-MM-NC-FT-Final-SQ.pdf):
  Maasai Mara non-resident adult USD 100/day January–June and USD 200/day July–December.
- **National Museums of Kenya** — [2026 admission rates](https://museums.or.ke/our-rates/):
  Fort Jesus adult KES 550 (Kenya/EAC), USD 9 (residents/rest of Africa),
  USD 17 (foreigners outside Africa); opening 08:30–17:30 daily.
- **Friends of Karura Forest** — [fees notice](https://friendsofkarura.org/wp-content/uploads/2026/03/KARURA-FEES-NOTICE-A3-2027_v8-3-1.pdf):
  adult KES 116 citizen / 232 EAC resident / 696 non-resident; 06:00–18:00,
  last entry 17:30, gates close 19:00.
- **Hell's Kitchen** — community-reported KES 200 entrance plus a guide fee;
  no official published tariff found, so it stays `reported`.
- **Chalbi Desert** — open land with no gated entrance fee; guide/transport
  costs are separate.
- **Lake Turkana** — no single lake-wide gate; access and community fees vary.

## Publication

The seed sets `verification_status='verified'` and `verified_at` via the
trigger. Do not run it against production until the curator confirms the table
above. After confirmation:

```bash
DATABASE_URL='<production internal URL>' scripts/seed-golden-destinations.sh
```

Then run the coverage query:

```sql
SELECT * FROM catalog_county_coverage WHERE curated_count > 0 ORDER BY county_code;
```

Expected: 10 curated destinations across 9 counties.
