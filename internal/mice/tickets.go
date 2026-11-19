package mice

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidTicket is returned for malformed, forged or expired QR payloads.
var ErrInvalidTicket = errors.New("invalid ticket")

// ticketPayload builds the opaque, signed token described in ADR 0009 §5:
// `event_id.attendee_id.expiry.hmac`. The token is deterministic for a fixed
// (event, attendee, expiry) so a re-issued ticket is byte-identical and the
// stored hash still matches.
func (h *Handler) ticketPayload(eventID, attendeeID string, expiry time.Time) string {
	payload := eventID + "." + attendeeID + "." + strconv.FormatInt(expiry.Unix(), 10)
	mac := hmac.New(sha256.New, h.ticketSecret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

func (h *Handler) verifyTicket(token string) (eventID, attendeeID string, expiry time.Time, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		return "", "", time.Time{}, ErrInvalidTicket
	}
	eventID, attendeeID = parts[0], parts[1]
	expiryUnix, parseErr := strconv.ParseInt(parts[2], 10, 64)
	if parseErr != nil {
		return "", "", time.Time{}, ErrInvalidTicket
	}
	expected := h.ticketPayload(eventID, attendeeID, time.Unix(expiryUnix, 0))
	sig := expected[strings.LastIndex(expected, ".")+1:]
	if !hmac.Equal([]byte(sig), []byte(parts[3])) {
		return "", "", time.Time{}, ErrInvalidTicket
	}
	expiry = time.Unix(expiryUnix, 0).UTC()
	if h.clock().After(expiry) {
		return "", "", time.Time{}, ErrInvalidTicket
	}
	return eventID, attendeeID, expiry, nil
}

func ticketHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// issueTicket returns the registration's ticket, creating the row on first
// use. One ticket per registration (ADR 0009 §5).
func (h *Handler) issueTicket(ctx context.Context, tx dbTX, eventID, registrationID, attendeeID string, expires time.Time) (*Ticket, error) {
	token := h.ticketPayload(eventID, attendeeID, expires)
	hash := ticketHash(token)
	var t Ticket
	var issuedAt time.Time
	var revokedAt *time.Time
	err := tx.QueryRow(ctx, `
		INSERT INTO event_tickets (registration_id, token_hash, expires_at)
		VALUES ($1,$2,$3)
		ON CONFLICT (registration_id) DO UPDATE SET token_hash=EXCLUDED.token_hash,
			expires_at=EXCLUDED.expires_at
		RETURNING id::text, registration_id::text, issued_at, expires_at, revoked_at`,
		registrationID, hash, expires).Scan(&t.ID, &t.RegistrationID, &issuedAt, &t.ExpiresAt, &revokedAt)
	if err != nil {
		return nil, err
	}
	t.QRPayload = token
	t.IssuedAt = issuedAt
	t.State = "valid"
	switch {
	case revokedAt != nil:
		t.State = "revoked"
	case h.clock().After(t.ExpiresAt):
		t.State = "expired"
	}
	return &t, nil
}

// ticketFor loads (and lazily issues) the ticket for a registration.
func (h *Handler) ticketFor(ctx context.Context, eventID, registrationID, attendeeID string, eventEnd time.Time) (*Ticket, error) {
	var exists bool
	_ = h.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM event_tickets WHERE registration_id=$1)`, registrationID).Scan(&exists)
	expires := eventEnd.Add(24 * time.Hour)
	if exists {
		var t Ticket
		var issuedAt time.Time
		var revokedAt *time.Time
		err := h.pool.QueryRow(ctx, `
			SELECT id::text, registration_id::text, token_hash, issued_at, expires_at, revoked_at
			  FROM event_tickets WHERE registration_id=$1`, registrationID).Scan(
			&t.ID, &t.RegistrationID, &hashString{}, &issuedAt, &t.ExpiresAt, &revokedAt)
		if err != nil {
			return nil, err
		}
		t.QRPayload = h.ticketPayload(eventID, attendeeID, t.ExpiresAt)
		t.IssuedAt = issuedAt
		t.State = "valid"
		if revokedAt != nil {
			t.State = "revoked"
		} else if h.clock().After(t.ExpiresAt) {
			t.State = "expired"
		}
		return &t, nil
	}
	return h.issueTicket(ctx, h.pool, eventID, registrationID, attendeeID, expires)
}

// hashString discards a hash column read we do not need client-side.
type hashString struct{}

func (h *hashString) Scan(any) error { return nil }

// validated reports whether the token's stored hash matches.
func (h *Handler) ticketRegistration(ctx context.Context, token string) (eventID, registrationID, attendeeID string, err error) {
	eventID, attendeeID, _, err = h.verifyTicket(token)
	if err != nil {
		return "", "", "", err
	}
	err = h.pool.QueryRow(ctx, `
		SELECT t.registration_id::text, r.event_id::text
		  FROM event_tickets t
		  JOIN event_registrations r ON r.id = t.registration_id
		 WHERE t.token_hash = $1 AND t.revoked_at IS NULL`,
		ticketHash(token)).Scan(&registrationID, &eventID)
	if err != nil {
		return "", "", "", ErrInvalidTicket
	}
	return eventID, registrationID, attendeeID, nil
}
