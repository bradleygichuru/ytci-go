package competition

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

type consultationSubmission struct {
	EditionSlug string          `json:"editionSlug"`
	Name        string          `json:"name"`
	Email       string          `json:"email"`
	Answers     json.RawMessage `json:"answers"`
	Consent     bool            `json:"consent"`
	Website     string          `json:"website"`
}

// SubmitConsultation stores a public consultation response. It is intentionally
// unauthenticated: the consultation is open to anyone, including people who do
// not have an app account. The general public rate limiter and the honeypot are
// the launch controls.
func (h *Handler) SubmitConsultation(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var req consultationSubmission
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Website) != "" {
		// Silently accept the request so a bot does not learn the honeypot.
		writeJSON(w, http.StatusCreated, map[string]string{"status": "received"})
		return
	}
	if !req.Consent {
		handler.WriteError(w, http.StatusBadRequest, "CONSENT_REQUIRED", "consent is required")
		return
	}
	if len(req.Name) > 200 || len(req.Email) > 320 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name or email is too long")
		return
	}
	if strings.TrimSpace(req.Email) != "" {
		if _, err := mail.ParseAddress(req.Email); err != nil {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "enter a valid email address")
			return
		}
	}
	if len(req.Answers) == 0 || len(req.Answers) > 20_000 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "answers are required")
		return
	}
	var answers map[string]any
	if err := json.Unmarshal(req.Answers, &answers); err != nil || len(answers) == 0 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "answers must be a non-empty object")
		return
	}

	var editionID *string
	if slug := strings.TrimSpace(req.EditionSlug); slug != "" {
		var id string
		err := h.pool.QueryRow(r.Context(),
			`SELECT id::text FROM competition_editions WHERE slug = $1`, slug).Scan(&id)
		if err != nil {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "competition edition not found")
			return
		}
		editionID = &id
	}

	_, err := h.pool.Exec(r.Context(), `
INSERT INTO competition_consultation_responses (edition_id, name, email, answers, consent)
VALUES ($1, $2, $3, $4, true)`,
		editionID, nullableString(req.Name), nullableString(req.Email), req.Answers)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to save consultation response", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "received"})
}

func nullableString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
