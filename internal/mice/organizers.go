package mice

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const organizerColumns = `o.id::text, o.slug, o.display_name, o.trust_tier, o.verification_status,
	o.about, o.logo_url, o.contact_email, o.contact_phone, o.website_url,
	o.verified_at, o.verification_expires_at, o.revoked_reason`

func scanOrganizer(row pgx.Row) (*OrganizerProfile, error) {
	var o OrganizerProfile
	var tier, about, logo, email, phone, website, revoked pgtype.Text
	var verified, expires pgtype.Timestamptz
	if err := row.Scan(&o.ID, &o.Slug, &o.DisplayName, &tier, &o.VerificationStatus,
		&about, &logo, &email, &phone, &website, &verified, &expires, &revoked); err != nil {
		return nil, err
	}
	if tier.Valid {
		o.TrustTier = tier.String
	} else {
		o.TrustTier = "community_organizer"
	}
	o.Label = trustLabel(o.TrustTier, o.VerificationStatus)
	o.About = nullText(about)
	o.LogoURL = nullText(logo)
	o.ContactEmail = nullText(email)
	o.ContactPhone = nullText(phone)
	o.WebsiteURL = nullText(website)
	o.VerifiedAt = nullTime(verified)
	o.VerificationExpiresAt = nullTime(expires)
	o.RevokedReason = nullText(revoked)
	o.EvidenceRequests = []OrganizerEvidenceRequest{}
	return &o, nil
}

func (h *Handler) loadOrganizer(ctx context.Context, id string) (*OrganizerProfile, error) {
	return scanOrganizer(h.pool.QueryRow(ctx, `SELECT `+organizerColumns+` FROM organizer_profiles o WHERE o.id=$1`, id))
}

func (h *Handler) loadOrganizerBySlug(ctx context.Context, slug string) (*OrganizerProfile, error) {
	return scanOrganizer(h.pool.QueryRow(ctx, `SELECT `+organizerColumns+` FROM organizer_profiles o WHERE o.slug=$1`, slug))
}

func (h *Handler) handleGetPublicOrganizer(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !slugPattern.MatchString(slug) {
		writeErr(w, r, http.StatusNotFound, "ORGANIZER_NOT_FOUND", "organizer not found", nil)
		return
	}
	org, err := h.loadOrganizerBySlug(r.Context(), slug)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "ORGANIZER_NOT_FOUND", "organizer not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	upcoming, _, err := h.listEvents(r.Context(), eventFilters{
		Organizer: org.ID, Public: true, From: time.Now().In(nairobi).Format("2006-01-02"),
	}, 10, nil, "")
	if err != nil {
		writeServer(w, r, err)
		return
	}
	org.UpcomingEvents = upcoming
	_ = h.pool.QueryRow(r.Context(), `
		SELECT count(*) FROM events
		 WHERE organizer_profile_id=$1 AND status IN ('published','scheduled') AND event_date < current_date`,
		org.ID).Scan(&org.PastEventCount)
	writeJSON(w, http.StatusOK, org)
}

type organizerWrite struct {
	Slug               string `json:"slug"`
	DisplayName        string `json:"displayName"`
	TrustTier          string `json:"trustTier"`
	About              string `json:"about"`
	LogoURL            string `json:"logoUrl"`
	ContactEmail       string `json:"contactEmail"`
	ContactPhone       string `json:"contactPhone"`
	WebsiteURL         string `json:"websiteUrl"`
	LegalName          string `json:"legalName"`
	RegistrationNumber string `json:"registrationNumber"`
}

func (o organizerWrite) validate() []ValidationError {
	var out []ValidationError
	if strings.TrimSpace(o.DisplayName) == "" {
		out = append(out, ValidationError{Field: "displayName", Message: "displayName is required"})
	}
	if o.Slug != "" && !slugPattern.MatchString(o.Slug) {
		out = append(out, ValidationError{Field: "slug", Message: "must match ^[a-z0-9-]+$"})
	}
	if o.TrustTier != "" && !contains([]string{"programme_partner", "verified_organizer", "community_organizer"}, o.TrustTier) {
		out = append(out, ValidationError{Field: "trustTier", Message: "unknown trust tier"})
	}
	return out
}

func (h *Handler) handleAdminListOrganizers(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "platform.admin_access") {
		return
	}
	h.expireOrganizers(r.Context())
	limit, cursor := parsePage(r)
	args := []any{}
	q := `SELECT ` + organizerColumns + ` FROM organizer_profiles o WHERE 1=1`
	if status := r.URL.Query().Get("status"); status != "" {
		args = append(args, status)
		q += fmt.Sprintf(" AND o.verification_status = $%d", len(args))
	}
	if cursor != nil {
		if sortValue, id, ok := paginationDecode(*cursor); ok {
			args = append(args, sortValue, id)
			q += fmt.Sprintf(" AND (o.display_name, o.id) > ($%d, $%d::uuid)", len(args)-1, len(args))
		}
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(" ORDER BY o.display_name ASC, o.id ASC LIMIT $%d", len(args))

	rows, err := h.pool.Query(r.Context(), q, args...)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer rows.Close()
	items := []OrganizerProfile{}
	for rows.Next() {
		o, err := scanOrganizer(rows)
		if err != nil {
			writeServer(w, r, err)
			return
		}
		if err := h.attachOrganizerEvidenceRequests(r.Context(), h.pool, o); err != nil {
			writeServer(w, r, err)
			return
		}
		items = append(items, *o)
	}
	writePage(w, limit, items, func(o OrganizerProfile) (string, bool) {
		return paginationEncode(o.DisplayName, o.ID), o.ID != ""
	})
}

func (h *Handler) handleAdminCreateOrganizer(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.create") {
		return
	}
	var body organizerWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if failures := body.validate(); len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	slug := body.Slug
	if slug == "" {
		slug = slugify(body.DisplayName)
	}
	var id string
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO organizer_profiles (slug, display_name, trust_tier, about, logo_url,
			contact_email, contact_phone, website_url, legal_name, registration_number, created_by)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),
		       NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),$11)
		RETURNING id::text`,
		slug, body.DisplayName, body.TrustTier, body.About, body.LogoURL, body.ContactEmail,
		body.ContactPhone, body.WebsiteURL, body.LegalName, body.RegistrationNumber, userID(r)).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "organizer_profiles_slug_key") {
			writeErr(w, r, http.StatusConflict, "ORGANIZER_SLUG_TAKEN", "an organizer already uses this slug", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	org, err := h.loadOrganizer(r.Context(), id)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if err := h.attachOrganizerEvidenceRequests(r.Context(), h.pool, org); err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

func (h *Handler) handleAdminUpdateOrganizer(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	id := chi.URLParam(r, "id")
	var body organizerWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if failures := body.validate(); len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	slug := body.Slug
	if slug == "" {
		slug = slugify(body.DisplayName)
	}
	tag, err := h.pool.Exec(r.Context(), `
		UPDATE organizer_profiles SET slug=$2, display_name=$3,
			trust_tier=COALESCE(NULLIF($4,''), trust_tier), about=NULLIF($5,''), logo_url=NULLIF($6,''),
			contact_email=NULLIF($7,''), contact_phone=NULLIF($8,''), website_url=NULLIF($9,''),
			legal_name=NULLIF($10,''), registration_number=NULLIF($11,''), updated_at=now()
		WHERE id=$1`,
		id, slug, body.DisplayName, body.TrustTier, body.About, body.LogoURL, body.ContactEmail,
		body.ContactPhone, body.WebsiteURL, body.LegalName, body.RegistrationNumber)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, r, http.StatusNotFound, "ORGANIZER_NOT_FOUND", "organizer not found", nil)
		return
	}
	org, err := h.loadOrganizer(r.Context(), id)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if err := h.attachOrganizerEvidenceRequests(r.Context(), h.pool, org); err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, org)
}

type verificationDecision struct {
	Action      string  `json:"action"`
	TrustTier   string  `json:"trustTier"`
	Reason      string  `json:"reason"`
	ExpiresAt   *string `json:"expiresAt"`
	EvidenceRef string  `json:"evidenceRef"`
}

func (h *Handler) handleAdminOrganizerVerification(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body verificationDecision
	if !decodeJSON(w, r, &body) {
		return
	}
	action := body.Action
	switch action {
	case "verify", "reject", "expire", "restore":
		if !requirePermission(w, r, "events.verify_organizer") {
			return
		}
	case "revoke", "appeal_decide":
		if !requirePermission(w, r, "events.revoke_organizer") {
			return
		}
	default:
		writeValidation(w, ValidationError{Field: "action", Message: "must be one of verify, reject, revoke, restore, expire, appeal_decide"})
		return
	}
	if len(body.Reason) < 10 || len(body.Reason) > 2000 {
		writeValidation(w, ValidationError{Field: "reason", Message: "reason must be between 10 and 2000 characters"})
		return
	}
	if body.TrustTier != "" && !contains([]string{"programme_partner", "verified_organizer", "community_organizer"}, body.TrustTier) {
		writeValidation(w, ValidationError{Field: "trustTier", Message: "unknown trust tier"})
		return
	}

	org, err := h.loadOrganizer(r.Context(), id)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "ORGANIZER_NOT_FOUND", "organizer not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}

	// Reviewer is not the requester (ADR 0012 §2, roles.v1.yaml
	// verifier_is_not_requester).
	requester, _ := h.verificationRequester(r.Context(), org.ID)
	if actor := userID(r); actor != "" && (actor == requester || actor == h.organizerCreator(r.Context(), org.ID)) {
		writeErr(w, r, http.StatusConflict, "REVIEWER_IS_REQUESTER",
			"the reviewer must differ from the organizer's requester", nil)
		return
	}

	now := h.clock()
	fromStatus := org.VerificationStatus
	fromTier := org.TrustTier
	toStatus := fromStatus
	toTier := fromTier
	var expires *time.Time
	var revokedAt *time.Time
	revokedReason := ""

	switch action {
	case "verify":
		if fromStatus == "revoked" {
			writeErr(w, r, http.StatusConflict, "INVALID_TRANSITION", "a revoked organizer must be restored or appealed", nil)
			return
		}
		if body.TrustTier == "" && (fromTier == "" || fromTier == "community_organizer") {
			writeValidation(w, ValidationError{Field: "trustTier", Message: "a trust tier is required to verify"})
			return
		}
		if body.TrustTier != "" {
			toTier = body.TrustTier
		}
		toStatus = "verified"
		if body.ExpiresAt != nil && *body.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, *body.ExpiresAt); err == nil {
				expires = &t
			}
		}
		if expires == nil {
			t := now.AddDate(1, 0, 0)
			expires = &t
		}
	case "reject":
		if fromStatus == "verified" {
			writeErr(w, r, http.StatusConflict, "INVALID_TRANSITION", "use revoke to remove a verified organizer", nil)
			return
		}
		toStatus = "unverified"
	case "revoke":
		if fromStatus != "verified" && fromStatus != "expired" {
			writeErr(w, r, http.StatusConflict, "INVALID_TRANSITION", "only a verified or expired organizer can be revoked", nil)
			return
		}
		toStatus = "revoked"
		revokedAt = &now
		revokedReason = body.Reason
	case "restore":
		if fromStatus != "revoked" {
			writeErr(w, r, http.StatusConflict, "INVALID_TRANSITION", "only a revoked organizer can be restored", nil)
			return
		}
		toStatus = "verified"
		t := now.AddDate(1, 0, 0)
		expires = &t
	case "expire":
		if fromStatus != "verified" {
			writeErr(w, r, http.StatusConflict, "INVALID_TRANSITION", "only a verified organizer can expire", nil)
			return
		}
		toStatus = "expired"
	case "appeal_decide":
		if fromStatus != "revoked" {
			writeErr(w, r, http.StatusConflict, "INVALID_TRANSITION", "only a revoked organizer can appeal", nil)
			return
		}
		var deadline pgtype.Timestamptz
		_ = h.pool.QueryRow(r.Context(), `SELECT appeal_deadline FROM organizer_profiles WHERE id=$1`, org.ID).Scan(&deadline)
		if deadline.Valid && now.After(deadline.Time) {
			writeErr(w, r, http.StatusConflict, "APPEAL_WINDOW_CLOSED", "the 14-day appeal window has closed", nil)
			return
		}
		toStatus = "verified"
		t := now.AddDate(1, 0, 0)
		expires = &t
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())

	var appealDeadline any
	if action == "revoke" {
		appealDeadline = now.AddDate(0, 0, 14)
	}
	_, err = tx.Exec(r.Context(), `
		UPDATE organizer_profiles SET
			verification_status=$2,
			trust_tier=COALESCE(NULLIF($3,''), trust_tier),
			verified_at=CASE WHEN $2='verified' THEN COALESCE(verified_at, now()) ELSE verified_at END,
			verification_expires_at=CASE WHEN $2='verified' THEN $4::timestamptz ELSE verification_expires_at END,
			revoked_at=CASE WHEN $2='revoked' THEN $5::timestamptz ELSE NULL END,
			revoked_reason=CASE WHEN $2='revoked' THEN $6::text ELSE NULL END,
			appeal_deadline=COALESCE($7::timestamptz, appeal_deadline),
			updated_at=now()
		WHERE id=$1`,
		org.ID, toStatus, toTier, expires, revokedAt, revokedReason, appealDeadline)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	eventAction := map[string]string{
		"verify": "verified", "reject": "rejected", "revoke": "revoked",
		"restore": "restored", "expire": "expired", "appeal_decide": "appeal_decided",
	}[action]
	_, err = tx.Exec(r.Context(), `
		INSERT INTO organizer_verification_events
			(organizer_id, action, from_status, to_status, from_tier, to_tier, actor_id, reason, evidence_ref)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''))`,
		org.ID, eventAction, fromStatus, toStatus, fromTier, toTier, userID(r), body.Reason, body.EvidenceRef)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	// A recorded decision answers any outstanding evidence request.
	_, err = tx.Exec(r.Context(), `
		UPDATE organizer_evidence_requests SET resolved_at=now()
		 WHERE organizer_id=$1 AND resolved_at IS NULL`, org.ID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if toStatus == "revoked" || toStatus == "expired" {
		if err := h.withdrawOrganizerEvents(r.Context(), tx, org.ID, toStatus, body.Reason); err != nil {
			writeServer(w, r, err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeServer(w, r, err)
		return
	}
	updated, err := h.loadOrganizer(r.Context(), org.ID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if err := h.attachOrganizerEvidenceRequests(r.Context(), h.pool, updated); err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) organizerCreator(ctx context.Context, id string) string {
	var creator pgtype.Text
	_ = h.pool.QueryRow(ctx, `SELECT created_by FROM organizer_profiles WHERE id=$1`, id).Scan(&creator)
	return nullText(creator)
}

func (h *Handler) verificationRequester(ctx context.Context, id string) (string, error) {
	var actor pgtype.Text
	err := h.pool.QueryRow(ctx, `
		SELECT actor_id FROM organizer_verification_events
		 WHERE organizer_id=$1 AND action='submitted'
		 ORDER BY created_at DESC LIMIT 1`, id).Scan(&actor)
	if err != nil && !isNoRows(err) {
		return "", err
	}
	return nullText(actor), nil
}

// withdrawOrganizerEvents unpublishes upcoming events and notifies registrants
// on revoke/expire (ADR 0012 §7).
func (h *Handler) withdrawOrganizerEvents(ctx context.Context, tx dbTX, organizerID, status, reason string) error {
	rows, err := tx.Query(ctx, `
		UPDATE events SET status='draft', updated_at=now()
		 WHERE organizer_profile_id=$1 AND status IN ('published','scheduled','postponed')
		   AND event_date >= current_date
		RETURNING id::text`, organizerID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		title := "Event withdrawn"
		body := "This event was withdrawn because the organizer's verification is no longer valid."
		if status == "expired" {
			title = "Organizer verification expired"
			body = "This event is paused because the organizer's verification has expired."
		}
		if err := h.enqueueEventChangeNotice(ctx, tx, id, title, body, false); err != nil {
			return err
		}
	}
	_ = reason
	return nil
}

// expireOrganizers lazily applies annual expiry and the 30-day grace period.
func (h *Handler) expireOrganizers(ctx context.Context) {
	now := h.clock()
	rows, err := h.pool.Query(ctx, `
		UPDATE organizer_profiles SET verification_status='expired', updated_at=now()
		 WHERE verification_status='verified' AND verification_expires_at IS NOT NULL
		   AND verification_expires_at < now()
		RETURNING id::text, verification_expires_at`)
	if err != nil {
		return
	}
	type expired struct {
		id string
		at time.Time
	}
	list := []expired{}
	for rows.Next() {
		var e expired
		var at pgtype.Timestamptz
		if rows.Scan(&e.id, &at) == nil && at.Valid {
			e.at = at.Time
			list = append(list, e)
		}
	}
	rows.Close()
	for _, e := range list {
		_, _ = h.pool.Exec(ctx, `
			INSERT INTO organizer_verification_events (organizer_id, action, from_status, to_status, reason)
			VALUES ($1,'expired','verified','expired','verification expiry job')`, e.id)
		if now.After(e.at.AddDate(0, 0, 30)) {
			_ = h.withdrawOrganizerEvents(ctx, h.pool, e.id, "expired", "verification expired")
		}
	}
}
