# Species and language API

Status: v1 read/write slice for ticket
[#369](https://github.com/bradleygichuru/ytci-mobile/issues/369). Shapes come from
`api/openapi/contracts.v1.yaml` (tag `species`); vocabulary comes from
`api/contracts/species.v1.yaml`; the product decisions are ADR 0013 (species
content model and licensing) and ADR 0014 (language architecture), recorded on
[#322](https://github.com/bradleygichuru/ytci-mobile/issues/322) and
[#323](https://github.com/bradleygichuru/ytci-mobile/issues/323).

## Auth and roles

Public reads are unauthenticated. Reader saves and language packs need a session
token. Admin routes authenticate every role and each handler checks its own
species permission, because species roles are not platform admins.

| Permission | Roles |
|---|---|
| `species.read` | every signed-in role, public otherwise |
| `species.create` | `content_editor`, `administrator`, `super_admin` |
| `species.edit_base` | `content_editor`, `species_expert`, `administrator`, `super_admin` |
| `species.expert_review` | `species_expert` |
| `species.translate` | `translator` |
| `species.review_language` | `language_reviewer` |
| `species.review_rights` | `rights_reviewer` |
| `species.upload_media` | `content_editor` |
| `species.publish` / `species.unpublish` | `administrator`, `super_admin` |
| `species.rights_withdraw` | `super_admin` |

`translator` and `language_reviewer` cannot be the same actor, and neither can
`content_editor` (media uploader) and `rights_reviewer`. The queue enforces both
at decision time; the publish gate re-checks translation separation.

## Model

`species` carries the language-neutral identity: scientific name, authorship,
rank, taxonomic status, accepted-species link, GBIF key, IUCN taxon id, Wikidata
QID, slug, `content_version`, and `publication_state`
(`draft | in_review | published | unpublished | tombstoned`).

Everything language-specific hangs off it:

- `species_names` — many names per language, each with script, region/county,
  source, preferred flag and review status. There is no universal name claim.
- `species_content` — one prose document per language with per-field status
  (`missing | draft | reviewed | stale`), `source_version`, translator and
  reviewer refs, and `published_at`.
- `species_media` + `species_media_alt` — managed assets with credit, rights
  holder, licence, sensitive-location flag and per-language alt text.
- `species_audio` — one track per language with narrator, required transcript,
  duration and rights.
- `species_sources` — citation, URL, access date, licence, permitted use,
  attribution and snapshot hash.
- `species_rights` — per-asset rights, consent record, expiry and withdrawal.
- `species_where_to_see` — evidence links to destinations, counties or protected
  areas. Never a guaranteed sighting.
- `species_links` — joins to destinations, courses and Audio Stories.
- `species_reviews` — append-only review evidence.
- `species_saves` / `species_review_queue` — migration `0044_species_workflow.sql`.

Migrations `0043` and `0044` are the species range (`0043` is the frozen
contract slice owned by the platform stream).

## Endpoints

### Public

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/public/species` | Published index. Filters: `q`, `language`, `iucn`, `countyGid`, `limit`, `cursor`. |
| GET | `/v1/public/species/{slug}` | Full record resolved to `?language=` with a visible fallback block. |
| GET | `/v1/public/species/{slug}/languages` | Reviewed coverage per language with tier and fallback flag. |

`q` matches the scientific name and every reviewed name. `language` narrows the
index to species with reviewed prose or names in that language. A tombstoned
record returns `410 SPECIES_WITHDRAWN`; an unknown or unpublished slug returns
`404 SPECIES_NOT_FOUND`.

### Reader

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/mobile/species/saved` | The reader's saved species. |
| POST | `/v1/mobile/species/{slug}/save` | Save. |
| DELETE | `/v1/mobile/species/{slug}/save` | Unsave. |
| GET | `/v1/mobile/species/packs/{language}` | Explicit offline pack manifest. |

Packs are built from reviewed content or reviewed names for published species.
English and Swahili are cached by the client; other languages are explicit
downloads and the manifest never triggers a background fetch.

### Admin

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/admin/species` | Catalogue, filter `state`. |
| POST | `/v1/admin/species` | Create the base record (slug optional, derived from the scientific name). |
| GET | `/v1/admin/species/{id}` | Full record with drafts, sources and review state. |
| PATCH | `/v1/admin/species/{id}` | Update taxonomy or status. |
| POST | `/v1/admin/species/{id}/variants` | Upsert one language's prose. |
| POST | `/v1/admin/species/{id}/names` | Upsert a sourced name. |
| POST | `/v1/admin/species/{id}/media` | Register a managed media asset. |
| POST | `/v1/admin/species/{id}/audio` | Register a per-language track. |
| POST | `/v1/admin/species/{id}/publish` | Publish one reviewed language variant. |
| POST | `/v1/admin/species/{id}/unpublish` | Unpublish, or tombstone after a rights withdrawal. |
| GET | `/v1/admin/species/review` | Queue, filter `stage`. |
| POST | `/v1/admin/species/review/{id}/decision` | `approved`, `changes_requested`, `rejected`. |
| GET/POST | `/v1/admin/species/sources` | Source registry. |
| GET/POST | `/v1/admin/species/rights` | Rights records. |

## Language resolution and fallback

Resolution is requested language → English, per document:

1. A reviewed, published variant with `source_version` at the species'
   `content_version` resolves normally.
2. `missing`, `stale`, `not_reviewed` or `unpublished` fall back to English and
   return `fallback: { used: true, reason, chip: "English" }`.
3. Swahili is never a silent substitute and machine translation is never
   published. `requestedLanguage` and `resolvedLanguage` are both returned.

The language selector only shows languages with a reviewed asset. Coverage tiers
are `full`, `summary`, `names` and `audio`; a name-only language is never
presented as fully localized.

## Publish gate

`POST /v1/admin/species/{id}/publish` requires `{ "language": "en" }` and returns
`409` with a typed code when any check fails, in this order:

| Code | Condition |
|---|---|
| `CONTENT_MISSING` | No variant for the language. |
| `CONTENT_NOT_REVIEWED` | Variant is not `reviewed`. |
| `REVIEW_SEPARATION` | Non-English variant lacks a translator, reviewer, or they match. |
| `SOURCE_MISSING` | No reviewed source is attached. No source, no publish. |
| `RIGHTS_MISSING` | A reviewed media/audio asset has no rights record, an expired one, a withdrawn one, or a non-commercial licence. |
| `REVIEW_PENDING` | A translation, language, expert or rights item is still pending for the variant. |

Publishing marks the species `published` and stamps `published_at` on the
reviewed assets for that language only.

### Staleness

Attaching a second source to a species is treated as a source re-check: it bumps
`content_version` and marks every dependent translation `stale`, clearing
`published_at` and re-queueing the language review. Readers then see the
English variant with a `stale` chip until the translation is re-reviewed and
re-published.

### Rights withdrawal

`POST /unpublish` with `tombstone: true` requires `species.rights_withdraw`
(`super_admin`). It withdraws every rights record for the species, marks media,
audio and content `stale`, clears `published_at`, sets the species
`tombstoned`, writes an audit row, and makes public reads return `410`. Audit
rows are never deleted.

## Licence boundaries

`validateSourceWrite` runs before any source row is written
(`internal/species/licence_test.go`):

- **IUCN** — category, assessment, version and as-of date only. Prose, threats
  and range maps are rejected; the API links out.
- **GBIF** — occurrences must be CC0 or CC BY. CC BY-NC is excluded.
- **KWS** — written permission recorded as `PERMISSION-<ref>`, or `link_only`.
- **Wikipedia** — `link_only`; prose copy is rejected.
- **Wikidata** — cross-reference only, never a local-name authority.

Rights records reject non-commercial licences and require a consent record for
KWS rights.

## Where-to-see

Evidence rows carry type (`survey`, `occurrence_record`,
`expert_observation`, `published_report`), confidence, review status and an
optional source. The public payload returns only reviewed rows and renders
`Recorded at <place>`; there are no precise coordinates and no live occurrence
map. Rows are authored by content intake and reviewed in the queue — the frozen
contract has no create endpoint for them.

## Tests

```sh
go build ./...
go vet ./internal/species/... ./internal/server/...
go test ./internal/species/... -count=1
go test ./internal/server/... -run 'Species' -count=1   # testcontainers/PostGIS
```

Coverage includes the full authoring → review → publish lifecycle, source
missing, self-review rejection at the HTTP edge, translator ≠ reviewer,
rights reviewer ≠ uploader, visible English fallback, search across languages,
source re-check staleness, reader saves, rights-withdrawal tombstoning and the
licence boundary unit cases.
