# MICE admin operations — change-control note

Status: additive to contract **1.0.0** (no existing operation changed)
Date: 2026-11-23
Owner: `stream:mice`
Parent surface: [mice-admin.md](../../ytci-mobile/docs/mice-admin.md)
OpenAPI: `api/openapi/contracts.v1.yaml` (operations 112–124)

## Why this exists

The MICE operations admin (#363) shipped with eight jobs labelled "local":
they were recorded in a browser-local audit trail because the frozen
interface had no write. That made the surface unsafe in production — a
lifecycle change, waitlist offer, registration correction, check-in undo or
speaker consent decision could be lost when the browser storage was cleared,
and no other operator or reviewer could see it.

This change adds the missing admin operations. It is **additive**:

- every pre-existing 1.0.0 path, method, operationId, schema and response is
  unchanged;
- the new operations are `mice`-tagged and admin-scoped;
- the MICE vocabulary contract (`api/contracts/mice.v1.yaml`) did not change;
- the role contract did not change. The new operations reuse the existing
  permissions `events.publish`, `events.edit_draft`, `events.create`,
  `events.override_checkin`, `events.verify_organizer` and `events.read`.

`TestOpenAPIValid` moved from 111 to 120 operations for the first batch and now
to 124 for this follow-up; its frozen-path table is the deliberate gate for this
note.

## New operations

| Method | Path | operationId | Permission | Audit action |
| --- | --- | --- | --- | --- |
| POST | `/v1/admin/events/{id}/lifecycle` | `adminChangeEventLifecycle` | `events.publish` (+ `events.publish_paid` for paid publish) | `event.cancel`, `event.unpublish`, `event.archive`, `event.publish`, `event.restore` |
| POST | `/v1/admin/events/{id}/waitlist-offers` | `adminCreateWaitlistOffer` | `events.edit_draft` | `waitlist.offer_sent` |
| POST | `/v1/admin/events/{id}/waitlist-offers/{registrationId}/decision` | `adminDecideWaitlistOffer` | `events.edit_draft` | `waitlist.offer_accepted`, `waitlist.offer_withdrawn` |
| POST | `/v1/admin/events/{id}/checkins/{checkInId}/undo` | `adminUndoCheckIn` | `events.override_checkin` | `checkin.undone` |
| PATCH | `/v1/admin/events/{id}/registrations/{registrationId}` | `adminUpdateRegistration` | `events.edit_draft` | `registration.updated`, `registration.cancelled` |
| PATCH | `/v1/admin/events/{id}/people/{personId}` | `adminUpdatePerson` | `events.edit_draft` | `speaker.updated` |
| DELETE | `/v1/admin/events/{id}/people/{personId}` | `adminRemoveSpeaker` | `events.edit_draft` | `speaker.removed` |
| POST | `/v1/admin/events/{id}/people/{personId}/consent` | `adminSetSpeakerConsent` | `events.edit_draft` | `speaker.consent_granted`, `speaker.consent_declined`, `speaker.consent_pending` |
| GET | `/v1/admin/mice/audit` | `adminListMiceAudit` | `events.read` | — (read) |
| GET | `/v1/admin/venues/{id}` | `adminGetVenue` | `events.read` | — (read) |
| POST | `/v1/admin/venues/{id}/evidence` | `adminAddVenueEvidence` | `events.create` | `venue.evidence_added` |
| POST | `/v1/admin/venues/{id}/retire` | `adminRetireVenue` | `events.create` | `venue.retired`, `venue.merged` |
| POST | `/v1/admin/organizers/{id}/evidence-requests` | `adminRequestOrganizerEvidence` | `events.verify_organizer` | `organizer.evidence_requested` |

### New schemas

`EventLifecycleWrite`, `WaitlistOfferWrite`, `WaitlistOfferDecision`,
`WaitlistOffer`, `CheckInUndoWrite`, `RegistrationWrite`,
`SpeakerConsentWrite`, `MiceAuditEvent`, `VenueEvidence`,
`VenueEvidenceWrite`, `VenueRetireWrite`, `OrganizerEvidenceRequest`,
`OrganizerEvidenceRequestWrite`, `OrganizerEvidenceRequestResult`.

`Venue` gained the optional `retiredAt`, `retiredReason`, `mergedIntoVenueId`
and `evidence` properties. `OrganizerProfile` gained the optional
`evidenceRequests` property. Both are additive: an older client ignores them.

`Speaker` gained the optional `consentStatus` property. That is additive: an
older client ignores it, and the public agenda still keys off `isPublic`.

### Behaviour notes

- **Lifecycle** maps `cancel → cancelled`, `unpublish → draft`,
  `archive → archived`, `publish/restore → published`. `archived` is a new
  `events.status` value; public discovery excludes it. Publish and restore run
  the same readiness and paid-event gates as `adminPublishEvent`.
- **Waitlist** has one open offer per event. The 24-hour window is capped at
  event start. Accepting promotes to confirmed; withdrawing clears the window
  and keeps the FIFO position. If a seat was already auto-promoted and the
  offer is withdrawn, that registration is cancelled and the next person is
  promoted.
- **Registration correction** requires a reason. A checked-in registration
  must be un-checked first (`409 UNDO_CHECKIN_REQUIRED`). Cancelling a
  confirmed registration promotes the next waitlisted reader in the same
  transaction. Answers may correct an existing key (legacy answers included)
  but cannot invent a key that the event's question table does not define.
- **Check-in undo** deletes the check-in row, returns the registration to
  confirmed and keeps an audit row. It is not restricted to the check-in
  window, because corrections frequently happen after the event.
- **Speaker consent** writes `event_people.consent_status` and projects
  `is_public = consent != 'declined'`. Declined speakers stay visible to the
  admin agenda (so consent can be restored) and disappear from the public one.
  `DELETE` removes the speaker from every session in the event and keeps the
  global person record.
- **Audit** is append-only in `mice_audit_events`; the handler writes the audit
  row in the same transaction as the action. The read is newest-first with the
  frozen `limit`/`cursor` envelope and optional `entityType`, `entityId` and
  `eventId` filters.
- **Venue evidence** is append-only: `claim` (what was asserted), `note` (the
  operator's account) and an optional `reference` (inspection ticket, photo,
  report). Corrections are new rows, so the history stays readable. The admin
  venue read returns evidence newest first.
- **Venue retirement/merge** sets `retired_at` and `retired_reason`. With
  `mergedIntoVenueId`, every `events.venue_id` that pointed at the source is
  re-pointed to the target in the same transaction (and `county_gid` follows the
  target), the source keeps its row and evidence, and the audit action is
  `venue.merged`. A self-merge, an unknown target, a retired target and a repeat
  retirement are rejected. Retired venues are invisible to the public: the
  public venue read returns 404, and public event payloads withhold the venue
  reference (`venue` and `venueDetail`) while the event itself stays listable.
  Merged events resolve to the surviving venue.
- **Organizer evidence request** requires `events.verify_organizer` and a note
  of 10–2000 characters. The request keeps the record pending; recording a
  verification decision resolves every open request. The admin organizer read
  returns requests newest first.

## Migration

`testdata/migrations/0074_mice_admin_ops.sql` (additive):

- extends the `events_status_check` with `archived`;
- adds `event_people.consent_status`, `consent_updated_at`,
  `consent_updated_by`;
- creates `mice_audit_events` and its indexes.

`testdata/migrations/0075_mice_venue_evidence.sql` (additive):

- adds `venues.retired_at`, `retired_reason` and `merged_into_venue_id`;
- creates `venue_evidence` (append-only, venue cascade) with a venue/time index;
- creates `organizer_evidence_requests` with an organizer/time index.

Both are backwards compatible with the previous binary: the new columns default
to NULL and the new tables are only read by the endpoints above.

## Remaining gaps (intentionally left out)

None for the two jobs this change closes. Deliberately not included:

- resolving an individual evidence request by id (a verification decision
  resolves every open request); there is no partial-resolution UI yet;
- an admin venue list filter for retired/merged records (the list includes them
  and the screen hides retired rows client-side);
- re-pointing historical `event_change_notices` or bookings when a venue is
  merged (only `events.venue_id` and `county_gid` move).
