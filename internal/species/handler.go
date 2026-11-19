package species

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/contracts"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

var slugPath = regexp.MustCompile(`^[a-z0-9-]+$`)

var suitePerms = []string{
	contracts.PermSpeciesCreate,
	contracts.PermSpeciesEditBase,
	contracts.PermSpeciesExpertReview,
	contracts.PermSpeciesTranslate,
	contracts.PermSpeciesReviewLang,
	contracts.PermSpeciesReviewRights,
	contracts.PermSpeciesUploadMedia,
	contracts.PermSpeciesPublish,
	contracts.PermSpeciesUnpublish,
	contracts.PermSpeciesRightsWithdraw,
}

// Handler exposes the frozen species OpenAPI operations.
type Handler struct {
	repo *Repo
}

// New builds the HTTP layer around a repository.
func New(repo *Repo) *Handler { return &Handler{repo: repo} }

// NewWithPool is the wiring seam used by internal/server.
func NewWithPool(pool *pgxpool.Pool, store r2.Store) *Handler {
	return New(NewRepo(pool, store))
}

func (h *Handler) Repo() *Repo { return h.repo }

// ---------------------------------------------------------------------------
// Public
// ---------------------------------------------------------------------------

func (h *Handler) ListPublic(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	iucn := strings.ToUpper(strings.TrimSpace(q.Get("iucn")))
	if iucn != "" && !validIUCN[iucn] {
		validation(w, "iucn", "iucn must be one of CR, EN, VU, NT, LC, DD, NE")
		return
	}
	f := ListFilter{
		Query:    q.Get("q"),
		Language: q.Get("language"),
		IUCN:     iucn,
		Cursor:   q.Get("cursor"),
		Limit:    limitParam(r, 50),
	}
	if raw := q.Get("countyGid"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			validation(w, "countyGid", "countyGid must be a positive integer")
			return
		}
		f.CountyGID = &n
	}
	items, hasMore, next, err := h.repo.ListPublic(r.Context(), f, middleware.UserID(r.Context()))
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page(items, hasMore, next))
}

func (h *Handler) GetPublic(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !slugPath.MatchString(slug) {
		handler.WriteError(w, http.StatusNotFound, "SPECIES_NOT_FOUND", "species not found")
		return
	}
	tombstoned, err := h.repo.Tombstoned(r.Context(), slug)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	if tombstoned {
		handler.WriteError(w, http.StatusGone, "SPECIES_WITHDRAWN",
			"this record was withdrawn after a rights change")
		return
	}
	language := normaliseLanguage(r.URL.Query().Get("language"))
	d, err := h.repo.GetPublic(r.Context(), slug, language, middleware.UserID(r.Context()))
	if errors.Is(err, ErrNotFound) {
		handler.WriteError(w, http.StatusNotFound, "SPECIES_NOT_FOUND", "species not found")
		return
	}
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) Languages(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	coverage, err := h.repo.Languages(r.Context(), slug)
	if errors.Is(err, ErrNotFound) {
		handler.WriteError(w, http.StatusNotFound, "SPECIES_NOT_FOUND", "species not found")
		return
	}
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": coverage})
}

func (h *Handler) ListSaved(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.SavedSpecies(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	id, err := h.repo.SaveSpecies(r.Context(), slug, middleware.UserID(r.Context()))
	if errors.Is(err, ErrNotFound) {
		handler.WriteError(w, http.StatusNotFound, "SPECIES_NOT_FOUND", "species not found")
		return
	}
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "id": id, "message": "Saved."})
}

func (h *Handler) Unsave(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.UnsaveSpecies(r.Context(), chi.URLParam(r, "slug"), middleware.UserID(r.Context())); err != nil {
		writeRepoError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Pack(w http.ResponseWriter, r *http.Request) {
	pack, err := h.repo.PackManifest(r.Context(), chi.URLParam(r, "language"))
	if errors.Is(err, ErrNotFound) {
		handler.WriteError(w, http.StatusNotFound, "PACK_NOT_FOUND", "no content pack for this language")
		return
	}
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pack)
}

// ---------------------------------------------------------------------------
// Admin
// ---------------------------------------------------------------------------

func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	if !requireStaff(w, r) {
		return
	}
	state := r.URL.Query().Get("state")
	switch state {
	case "", "draft", "in_review", "published", "unpublished", "tombstoned":
	default:
		validation(w, "state", "invalid publication state")
		return
	}
	items, hasMore, next, err := h.repo.ListAdmin(r.Context(), state, r.URL.Query().Get("cursor"), limitParam(r, 50))
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page(items, hasMore, next))
}

func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePerm(w, r, contracts.PermSpeciesCreate) {
		return
	}
	var body SpeciesWrite
	if !decode(w, r, &body) {
		return
	}
	d, err := h.repo.CreateSpecies(r.Context(), middleware.UserID(r.Context()), body)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (h *Handler) AdminGet(w http.ResponseWriter, r *http.Request) {
	if !requireStaff(w, r) {
		return
	}
	d, err := h.repo.GetAdmin(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		handler.WriteError(w, http.StatusNotFound, "SPECIES_NOT_FOUND", "species not found")
		return
	}
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	if !requirePerm(w, r, contracts.PermSpeciesEditBase) {
		return
	}
	var body SpeciesWrite
	if !decode(w, r, &body) {
		return
	}
	d, err := h.repo.UpdateSpecies(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) AdminUpsertVariant(w http.ResponseWriter, r *http.Request) {
	var body VariantWrite
	if !decode(w, r, &body) {
		return
	}
	if normaliseLanguage(body.Language) == "en" {
		if !requirePerm(w, r, contracts.PermSpeciesEditBase) {
			return
		}
	} else if !requirePerm(w, r, contracts.PermSpeciesTranslate) {
		return
	}
	v, err := h.repo.UpsertVariant(r.Context(), chi.URLParam(r, "id"), middleware.UserID(r.Context()), body)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) AdminUpsertName(w http.ResponseWriter, r *http.Request) {
	if !requirePerm(w, r, contracts.PermSpeciesEditBase) {
		return
	}
	var body NameWrite
	if !decode(w, r, &body) {
		return
	}
	n, err := h.repo.UpsertName(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (h *Handler) AdminCreateMedia(w http.ResponseWriter, r *http.Request) {
	if !requirePerm(w, r, contracts.PermSpeciesUploadMedia) {
		return
	}
	var body MediaWrite
	if !decode(w, r, &body) {
		return
	}
	m, err := h.repo.CreateMedia(r.Context(), chi.URLParam(r, "id"), middleware.UserID(r.Context()), body)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (h *Handler) AdminCreateAudio(w http.ResponseWriter, r *http.Request) {
	if !requirePerm(w, r, contracts.PermSpeciesUploadMedia) {
		return
	}
	var body AudioWrite
	if !decode(w, r, &body) {
		return
	}
	a, err := h.repo.CreateAudio(r.Context(), chi.URLParam(r, "id"), middleware.UserID(r.Context()), body)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *Handler) AdminPublish(w http.ResponseWriter, r *http.Request) {
	if !requirePerm(w, r, contracts.PermSpeciesPublish) {
		return
	}
	var body struct {
		Language string `json:"language"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Language) == "" {
		validation(w, "language", "language is required")
		return
	}
	d, err := h.repo.Publish(r.Context(), chi.URLParam(r, "id"), body.Language, middleware.UserID(r.Context()))
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) AdminUnpublish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason    string `json:"reason"`
		Tombstone bool   `json:"tombstone"`
	}
	if !decode(w, r, &body) {
		return
	}
	perm := contracts.PermSpeciesUnpublish
	if body.Tombstone {
		perm = contracts.PermSpeciesRightsWithdraw
	}
	if !requirePerm(w, r, perm) {
		return
	}
	d, err := h.repo.Unpublish(r.Context(), chi.URLParam(r, "id"),
		middleware.UserID(r.Context()), body.Reason, body.Tombstone)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) AdminReviewQueue(w http.ResponseWriter, r *http.Request) {
	if !requireStaff(w, r) {
		return
	}
	stage := r.URL.Query().Get("stage")
	switch stage {
	case "", "translation", "language", "expert", "rights", "publish":
	default:
		validation(w, "stage", "invalid review stage")
		return
	}
	items, hasMore, next, err := h.repo.ReviewQueue(r.Context(), stage, r.URL.Query().Get("cursor"), limitParam(r, 50))
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page(items, hasMore, next))
}

func (h *Handler) AdminDecideReview(w http.ResponseWriter, r *http.Request) {
	if !requireStaff(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	item, err := h.repo.ReviewItem(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		handler.WriteError(w, http.StatusNotFound, "REVIEW_NOT_FOUND", "review item not found")
		return
	}
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	perm := map[string]string{
		"translation": contracts.PermSpeciesTranslate,
		"language":    contracts.PermSpeciesReviewLang,
		"expert":      contracts.PermSpeciesExpertReview,
		"rights":      contracts.PermSpeciesReviewRights,
		"publish":     contracts.PermSpeciesPublish,
	}[item.Stage]
	if perm == "" || !requirePerm(w, r, perm) {
		return
	}
	var body struct {
		Decision string `json:"decision"`
		Notes    string `json:"notes"`
	}
	if !decode(w, r, &body) {
		return
	}
	out, err := h.repo.DecideReview(r.Context(), id, middleware.UserID(r.Context()), body.Decision, body.Notes)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AdminListSources(w http.ResponseWriter, r *http.Request) {
	if !requireStaff(w, r) {
		return
	}
	items, err := h.repo.ListSources(r.Context(), r.URL.Query().Get("speciesId"))
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) AdminCreateSource(w http.ResponseWriter, r *http.Request) {
	if !requirePerm(w, r, contracts.PermSpeciesEditBase) {
		return
	}
	var body SourceWrite
	if !decode(w, r, &body) {
		return
	}
	src, err := h.repo.CreateSource(r.Context(), body)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, src)
}

func (h *Handler) AdminListRights(w http.ResponseWriter, r *http.Request) {
	if !requireStaff(w, r) {
		return
	}
	items, err := h.repo.ListRights(r.Context())
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) AdminCreateRights(w http.ResponseWriter, r *http.Request) {
	if !requirePerm(w, r, contracts.PermSpeciesReviewRights) {
		return
	}
	var body RightsWrite
	if !decode(w, r, &body) {
		return
	}
	rights, err := h.repo.CreateRights(r.Context(), middleware.UserID(r.Context()), body)
	if err != nil {
		writeRepoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, rights)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func requirePerm(w http.ResponseWriter, r *http.Request, perm string) bool {
	if contracts.MustRoles().Can(string(middleware.RoleFromCtx(r.Context())), perm) {
		return true
	}
	handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "permission denied: "+perm)
	return false
}

// requireStaff admits every species authoring/review role without opening the
// draft catalogue to ordinary readers, whose species.read is public-only.
func requireStaff(w http.ResponseWriter, r *http.Request) bool {
	role := string(middleware.RoleFromCtx(r.Context()))
	roles := contracts.MustRoles()
	if roles.Can(role, contracts.PermPlatformAdminAccess) {
		return true
	}
	for _, p := range suitePerms {
		if roles.Can(role, p) {
			return true
		}
	}
	handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "species staff access required")
	return false
}

func writeRepoError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	if errors.As(err, &ve) {
		handler.WriteValidationError(w, []model.ValidationError{{Field: ve.Field, Message: ve.Message}})
		return
	}
	var ge *GateError
	if errors.As(err, &ge) {
		handler.WriteError(w, http.StatusConflict, ge.Code, ge.Message)
		return
	}
	switch {
	case errors.Is(err, ErrNotFound):
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "not found")
	case errors.Is(err, ErrConflict):
		handler.WriteError(w, http.StatusConflict, "CONFLICT", "a record with this identifier already exists")
	default:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "species request failed", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return false
	}
	return true
}

func validation(w http.ResponseWriter, field, message string) {
	handler.WriteValidationError(w, []model.ValidationError{{Field: field, Message: message}})
}

func page(items any, hasMore bool, next string) map[string]any {
	var cursor *string
	if next != "" {
		cursor = &next
	}
	return map[string]any{"items": items, "nextCursor": cursor, "hasMore": hasMore}
}

func limitParam(r *http.Request, fallback int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return clampLimit(n)
}
