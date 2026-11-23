# MICE admin operations — change-control note

Status: additive to contract **1.0.0** (no existing operation changed)
Date: 2026-11-23
Owner: `stream:mice`
Parent surface: [mice-admin.md](../../ytci-mobile/docs/mice-admin.md)
OpenAPI: `api/openapi/contracts.v1.yaml` (operations 112–120)

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
  permissions `events.publish`, `events.edit_draft`,
  `events.override_checkin` and `events.read`.

`TestOpenAPIValid` moves from 111 to 120 operations; its frozen-path table is
the deliberate gate for this note.

## New operations

| Method | Path | operationId | Permission | Audit action |
| --- | --- | --- | --- | --- |
| POST | `/v1/admin/events/{id}/lifecycle` | `adminChangeEventLifecycle` | `events.publish` (+ `events.publish_paid` for paid publish) | `event.cancel`, `event.unpublish`, `event.archive`, `event.publish`, `event.restore` |
| POST | `/v1/admin/events/{id}/waitlist-offers` | `adminCreateWaitlistOffer` | `events.edit_draft` | `waitlist.offer_sent` |
| POST | `/v1/admin/events/{id}/waitlist-offers/{registrationId}/decision` | `adminDecideWaitlistOffer` | `events.edit_draft` | `waitlist.offer_accepted`, `waitlist.offer_withdrawed` |
| POST | `/v1/admin/events/{id}/checkins/{checkInId}/undo` | `adminUndoCheckIn` | `events.override_checkin` | `checkin.undone` |
| PATCH | `/v1/admin/events/{id}/registrations/{registrationId}` | `adminUpdateRegistration` | `events.edit_draft` | `registration.updated`, `registration.cancelled` |
| PATCH | `/v1/admin/events/{id}/people/{personId}` | `adminUpdatePerson` | `events.edit_draft` | `speaker.updated` |
| DELETE | `/v1/admin/events/{id}/people/{personId}` | `adminRemoveSpeaker` | `events.edit_draft` | `speaker.removed` |
| POST | `/v1/admin/events/{id}/people/{personId}/consent` | `adminSetSpeakerConsent` | `events.edit_draft` | `speaker.consent_granted`, `speaker.consent_declined`, `speaker.consent_pending` |
| GET | `/v1/admin/mice/audit` | `adminListMiceAudit` | `events.read` | — (read) |

Note on the audit verb `waitlist.offer_withdrawed`: it follows the mechanical
`offer_` + decision + `ed` construction in the handler. It is ugly but stable;
consumers filter by entity/action prefix rather than exact spelling.

### New schemas

`EventLifecycleWrite`, `WaitlistOfferWrite`, `WaitlistOfferDecision`,
`WaitlistOffer`, `CheckInUndoWrite`, `RegistrationWrite`,
`SpeakerConsentWrite`, `MiceAuditEvent`.

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

## Migration

`testdata/migrations/0074_mice_admin_ops.sql` (additive):

- extends the `events_status_check` with `archived`;
- adds `event_people.consent_status`, `consent_updated_at`,
  `consent_updated_by`;
- creates `mice_audit_events` and its indexes.

Production applies this only when the backend deploy that writes
`consent_status` / `mice_audit_events` ships. The table and columns are
backwards compatible with the previous binary.

## Remaining gaps (intentionally left out)

- **Venue evidence edit / retire / merge** has no endpoint yet. The venues
  screen still labels evidence and retirement as local.
- **Organizer "request more evidence"** has no endpoint yet. Verification
  decisions remain server-backed; the request note remains local.

Both need their own schema decision (evidence storage and an
`organizer_verification_events.action` value), so they are not smuggled into
this change.
