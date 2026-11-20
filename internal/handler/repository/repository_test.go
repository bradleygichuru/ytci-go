package repository_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/handler/repository"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

// ---------------------------------------------------------------------------
// Fake storage
// ---------------------------------------------------------------------------

type fakeStore struct {
	mu      sync.Mutex
	objects map[string]r2.ObjectInfo
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: map[string]r2.ObjectInfo{}}
}

func (f *fakeStore) PresignedPutURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://upload.test/" + key, nil
}

func (f *fakeStore) PresignedPutURLWithContentType(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "https://upload.test/" + key, nil
}

func (f *fakeStore) PresignedGetURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://get.test/" + key + "?sig=1", nil
}

func (f *fakeStore) PutObject(_ context.Context, key string, _ io.Reader, contentType string) error {
	f.upload(key, contentType, 0)
	return nil
}

func (f *fakeStore) GetObject(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[key]; !ok {
		return nil, errors.New("not found")
	}
	return []byte("object"), nil
}

func (f *fakeStore) DeleteObject(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}

func (f *fakeStore) HeadObject(_ context.Context, key string) (r2.ObjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.objects[key]
	if !ok {
		return r2.ObjectInfo{}, errors.New("not found")
	}
	return info, nil
}

func (f *fakeStore) upload(key, contentType string, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = r2.ObjectInfo{Size: size, ContentType: contentType}
}

func (f *fakeStore) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type harness struct {
	pool  *pgxpool.Pool
	store *fakeStore
	root  http.Handler
}

func setup(t *testing.T) *harness {
	return setupWithBurst(t, 100)
}

func setupWithBurst(t *testing.T, submissionBurst int) *harness {
	t.Helper()
	pool := db.SetupTestDB(t)
	t.Cleanup(func() { pool.Close() })
	store := newFakeStore()
	h := repository.New(pool, store)

	root := chi.NewRouter()
	root.Route("/v1/public", func(pub chi.Router) {
		h.RegisterPublic(pub,
			middleware.NewRateLimiter(1.0/6.0, 10),
			middleware.NewRateLimiter(5.0/3600.0, submissionBurst),
		)
	})
	_, err := pool.Exec(context.Background(), `
INSERT INTO users (id, name, email, created_at, updated_at, role)
VALUES ('admin-1', 'Repository Admin', 'admin-1@test.example', now(), now(), 'administrator')
ON CONFLICT (id) DO NOTHING`)
	require.NoError(t, err)

	root.Route("/v1/admin/repository", func(ar chi.Router) {
		ar.Use(testAuth)
		ar.Use(repository.AdminGate)
		h.RegisterAdmin(ar)
	})
	return &harness{pool: pool, store: store, root: root}
}

func testAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.CtxRole, r.Header.Get("X-Test-Role"))
		ctx = context.WithValue(ctx, middleware.CtxUserID, r.Header.Get("X-Test-User"))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *harness) do(t *testing.T, method, path string, body any, role, user string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "http://test"+path, reader)
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	rec := httptest.NewRecorder()
	h.root.ServeHTTP(rec, req)

	var decoded map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	}
	return rec, decoded
}

func (h *harness) openSubmissions(t *testing.T) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(), `
UPDATE repository_settings SET submissions_open = true, terms_version = '2026-11' WHERE id = 'default'`)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Seed helpers
// ---------------------------------------------------------------------------

type seedEntry struct {
	Kind          string
	Slug          string
	Title         string
	Summary       string
	Body          string
	County        string
	Category      string
	Status        string
	AuthorName    string
	AuthorEmail   string
	AuthorPhone   string
	Moderation    string
	ReviewedBy    string
	ReferenceCode string
	Featured      bool
	PublishedAt   *time.Time
	CoverMediaID  *string
	ExternalURL   string
}

func (h *harness) seed(t *testing.T, e seedEntry) string {
	t.Helper()
	ctx := context.Background()
	if e.Kind == "" {
		e.Kind = "innovation"
	}
	if e.Status == "" {
		e.Status = "published"
	}
	if e.Title == "" {
		e.Title = "Solar cold storage"
	}
	if e.Summary == "" {
		e.Summary = "A pay-per-crate cooling service."
	}
	if e.Body == "" {
		e.Body = "The project began in 2025."
	}
	var id string
	err := h.pool.QueryRow(ctx, `
INSERT INTO repository_entries
(kind, slug, title, summary, body_md, county, category, tags, author_name, author_email,
 author_phone, status, source, featured, moderation_note, reviewed_by, reference_code,
 cover_media_id, external_url, published_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,''),$12,'admin',$13,
        NULLIF($14,''),NULLIF($15,''),NULLIF($16,''),$17,NULLIF($18,''),$19)
RETURNING id::text`,
		e.Kind, e.Slug, e.Title, e.Summary, e.Body, e.County, e.Category, []string{"youth"},
		e.AuthorName, e.AuthorEmail, e.AuthorPhone, e.Status, e.Featured, e.Moderation,
		e.ReviewedBy, e.ReferenceCode, e.CoverMediaID, e.ExternalURL, e.PublishedAt).Scan(&id)
	require.NoError(t, err)
	return id
}

func (h *harness) seedMedia(t *testing.T, key, thumb, alt, credit, mediaType string) string {
	t.Helper()
	var id string
	err := h.pool.QueryRow(context.Background(), `
INSERT INTO media_assets (object_key, thumbnail_key, alt_text, credit, type, status, entity_type, original_name, file_size_bytes)
VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''), $5, 'ready', 'repository_entry', $6, 1024)
RETURNING id::text`, key, thumb, alt, credit, mediaType, path.Base(key)).Scan(&id)
	require.NoError(t, err)
	return id
}

func ptrTime(t time.Time) *time.Time { return &t }

// ---------------------------------------------------------------------------
// Public reads
// ---------------------------------------------------------------------------

func TestPublicListFiltersSearchPaginationAndPrivacy(t *testing.T) {
	h := setup(t)

	now := time.Now().UTC().Truncate(time.Second)
	coverID := h.seedMedia(t, "repository/covers/naivasha.jpg", "repository/covers/naivasha-thumb.jpg", "Boats at dawn", "Amina W.", "image")
	innovationID := h.seed(t, seedEntry{
		Kind: "innovation", Slug: "solar-cold-storage", Title: "Solar cold storage for fish traders",
		County: "Homa Bay", Category: "Tourism Tech", AuthorName: "Kevin O.",
		AuthorEmail: "kevin@example.com", AuthorPhone: "+254700000001",
		ReferenceCode: "YTCI-SECRET1", Moderation: "internal reviewer note",
		PublishedAt: ptrTime(now.Add(-3 * time.Hour)), CoverMediaID: &coverID, Featured: true,
	})
	h.seed(t, seedEntry{
		Kind: "competition_entry", Slug: "lake-naivasha-photo-story", Title: "Lake Naivasha: a fisher's morning",
		County: "Nakuru", Category: "Photography", AuthorName: "Amina W.",
		AuthorEmail: "amina@example.com", AuthorPhone: "+254700000002",
		PublishedAt: ptrTime(now.Add(-2 * time.Hour)),
	})
	h.seed(t, seedEntry{
		Kind: "experience", Slug: "hells-gate-on-foot", Title: "Hell's Gate on foot",
		County: "Nakuru", Category: "Adventure", AuthorName: "YTCI Editorial",
		PublishedAt: ptrTime(now.Add(-1 * time.Hour)),
	})
	h.seed(t, seedEntry{Kind: "innovation", Slug: "draft-secret", Title: "Draft secret", Status: "draft", County: "Nairobi"})
	h.seed(t, seedEntry{Kind: "innovation", Slug: "submitted-secret", Title: "Submitted secret", Status: "submitted", County: "Nairobi"})

	// Newest first, published only.
	rec, body := h.do(t, "GET", "/v1/public/repository/entries", nil, "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	items := body["items"].([]any)
	require.Len(t, items, 3)
	first := items[0].(map[string]any)
	assert.Equal(t, "hells-gate-on-foot", first["slug"])
	assert.Contains(t, rec.Header().Get("Cache-Control"), "s-maxage=60")

	// PII and internal columns must not appear anywhere in the payload.
	raw := rec.Body.String()
	for _, forbidden := range []string{"amina@example.com", "+254700000002", "internal reviewer note", "YTCI-SECRET1", "authorEmail", "authorPhone", "moderationNote", "referenceCode", "reviewedBy", "search_vector", "object_key"} {
		assert.NotContains(t, raw, forbidden)
	}

	// Kind filter (single and comma-separated).
	_, body = h.do(t, "GET", "/v1/public/repository/entries?kind=innovation", nil, "", "")
	require.Len(t, body["items"], 1)
	_, body = h.do(t, "GET", "/v1/public/repository/entries?kind=innovation,experience", nil, "", "")
	require.Len(t, body["items"], 2)
	bad, badBody := h.do(t, "GET", "/v1/public/repository/entries?kind=bogus", nil, "", "")
	assert.Equal(t, http.StatusBadRequest, bad.Code)
	assert.Equal(t, "INVALID_REQUEST", badBody["error"].(map[string]any)["code"])

	// County, category and featured filters.
	_, body = h.do(t, "GET", "/v1/public/repository/entries?county=Nakuru", nil, "", "")
	require.Len(t, body["items"], 2)
	_, body = h.do(t, "GET", "/v1/public/repository/entries?category=Photography", nil, "", "")
	require.Len(t, body["items"], 1)
	_, body = h.do(t, "GET", "/v1/public/repository/entries?featured=true", nil, "", "")
	require.Len(t, body["items"], 1)
	assert.Equal(t, innovationID, body["items"].([]any)[0].(map[string]any)["id"])

	// Full-text search and short-query fallback.
	_, body = h.do(t, "GET", "/v1/public/repository/entries?q=naivasha", nil, "", "")
	require.Len(t, body["items"], 1)
	_, body = h.do(t, "GET", "/v1/public/repository/entries?q=cold", nil, "", "")
	require.Len(t, body["items"], 1)

	// Cursor pagination walks every page exactly once.
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		path := "/v1/public/repository/entries?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		rec, body := h.do(t, "GET", path, nil, "", "")
		require.Equal(t, http.StatusOK, rec.Code)
		for _, item := range body["items"].([]any) {
			seen[item.(map[string]any)["slug"].(string)] = true
		}
		next, _ := body["nextCursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	assert.Len(t, seen, 3)

	// Invalid cursor is a 400, not a silent first page.
	rec, body = h.do(t, "GET", "/v1/public/repository/entries?cursor=not-a-cursor", nil, "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "INVALID_REQUEST", body["error"].(map[string]any)["code"])

	// Non-published detail is invisible.
	rec, _ = h.do(t, "GET", "/v1/public/repository/entries/draft-secret", nil, "", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = h.do(t, "GET", "/v1/public/repository/entries/submitted-secret", nil, "", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Cover and thumbnail URLs are signed separately.
	_, body = h.do(t, "GET", "/v1/public/repository/entries/solar-cold-storage", nil, "", "")
	assert.Contains(t, body["coverUrl"], "repository/covers/naivasha.jpg")
	assert.Contains(t, body["coverThumbUrl"], "naivasha-thumb.jpg")
	coverImage := body["coverImage"].(map[string]any)
	assert.Contains(t, coverImage["url"], "naivasha.jpg")
	assert.Equal(t, "Boats at dawn", coverImage["alt"])
}

func TestPublicDetailGalleryDocumentAndFacetsStatsSettings(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC().Truncate(time.Second)

	media1 := h.seedMedia(t, "repository/gallery/one.jpg", "", "First", "Amina W.", "image")
	media2 := h.seedMedia(t, "repository/gallery/two.jpg", "", "Second", "Amina W.", "image")
	docID := h.seedMedia(t, "repository/docs/essay.pdf", "", "", "", "pdf")
	external := "https://example.com/essay"
	entryID := h.seed(t, seedEntry{
		Kind: "competition_entry", Slug: "naivasha-essay", Title: "Naivasha essay",
		County: "Nakuru", Category: "Photography", AuthorName: "Amina W.",
		PublishedAt: ptrTime(now.Add(-time.Hour)), ExternalURL: external,
		AuthorEmail: "amina@example.com",
	})
	_, err := h.pool.Exec(context.Background(), `
UPDATE repository_entries SET document_media_id = $2 WHERE id = $1`, entryID, docID)
	require.NoError(t, err)
	for i, mediaID := range []string{media1, media2} {
		_, err := h.pool.Exec(context.Background(),
			`INSERT INTO repository_entry_media (entry_id, media_id, sort_order) VALUES ($1,$2,$3)`,
			entryID, mediaID, i)
		require.NoError(t, err)
	}

	rec, body := h.do(t, "GET", "/v1/public/repository/entries/naivasha-essay", nil, "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	gallery := body["gallery"].([]any)
	require.Len(t, gallery, 2)
	assert.Equal(t, float64(0), gallery[0].(map[string]any)["sortOrder"])
	assert.Contains(t, gallery[1].(map[string]any)["url"], "two.jpg")
	assert.Contains(t, body["documentUrl"], "essay.pdf")
	document := body["document"].(map[string]any)
	assert.Equal(t, "application/pdf", document["mimeType"])
	assert.Equal(t, "essay.pdf", document["fileName"])
	assert.Equal(t, external, body["externalUrl"])
	assert.NotContains(t, rec.Body.String(), "amina@example.com")

	// Facets and stats are real counts.
	rec, body = h.do(t, "GET", "/v1/public/repository/facets", nil, "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	kinds := body["kinds"].([]any)
	require.Len(t, kinds, 1)
	assert.Equal(t, "competition_entry", kinds[0].(map[string]any)["value"])
	assert.Equal(t, "Competitions", kinds[0].(map[string]any)["label"])
	counties := body["counties"].([]any)
	require.Len(t, counties, 1)
	assert.Equal(t, float64(1), counties[0].(map[string]any)["count"])

	rec, body = h.do(t, "GET", "/v1/public/repository/stats", nil, "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(1), body["publishedEntries"])
	assert.Equal(t, float64(1), body["competitions"])
	assert.Equal(t, float64(0), body["byKind"].(map[string]any)["innovation"])

	// Settings are read from the singleton and default when absent.
	_, err = h.pool.Exec(context.Background(), `
UPDATE repository_settings SET submissions_open = true, submission_intro = 'Open now', terms_version = '2026-11' WHERE id = 'default'`)
	require.NoError(t, err)
	_, body = h.do(t, "GET", "/v1/public/repository/settings", nil, "", "")
	assert.Equal(t, true, body["submissionsOpen"])
	assert.Equal(t, "Open now", body["submissionIntro"])
	assert.Equal(t, "2026-11", body["termsVersion"])

	_, err = h.pool.Exec(context.Background(), `DELETE FROM repository_settings`)
	require.NoError(t, err)
	_, body = h.do(t, "GET", "/v1/public/repository/settings", nil, "", "")
	assert.Equal(t, false, body["submissionsOpen"])
	assert.Equal(t, "2026-11", body["termsVersion"])
}

// ---------------------------------------------------------------------------
// Submissions and uploads
// ---------------------------------------------------------------------------

func validSubmission(coverKey string) map[string]any {
	return map[string]any{
		"kind":           "competition_entry",
		"title":          "A fisher's morning",
		"summary":        "A photo essay on the lake before sunrise.",
		"bodyMd":         "## Before sunrise\n\nThe boats leave the shore.",
		"county":         "Nakuru",
		"category":       "Photography",
		"tags":           []string{"photography", "youth"},
		"authorName":     "Amina W.",
		"authorEmail":    "amina@example.com",
		"authorPhone":    "+254712345678",
		"authorAgeBand":  "18_24",
		"consent":        true,
		"consentVersion": "2026-11",
		"coverObjectKey": coverKey,
		"honeypot":       "",
	}
}

func (h *harness) presign(t *testing.T, kind, contentType string, size int64, fileName string) (key, uploadURL string) {
	t.Helper()
	rec, body := h.do(t, "POST", "/v1/public/repository/uploads/presign", map[string]any{
		"kind": kind, "fileName": fileName, "contentType": contentType, "fileSizeBytes": size,
	}, "", "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return body["objectKey"].(string), body["uploadUrl"].(string)
}

func TestSubmissionUploadValidationAndModerationLifecycle(t *testing.T) {
	h := setup(t)
	h.openSubmissions(t)

	key, uploadURL := h.presign(t, "image", "image/jpeg", 3456789, "naivasha dawn.jpg")
	assert.Contains(t, key, "repository/submissions/")
	assert.Contains(t, uploadURL, key)
	assert.NotContains(t, key, " ")

	// The browser PUT lands the exact bytes.
	h.store.upload(key, "image/jpeg", 3456789)

	body := validSubmission(key)
	rec, resp := h.do(t, "POST", "/v1/public/repository/submissions", body, "", "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	ref := resp["referenceCode"].(string)
	assert.True(t, strings.HasPrefix(ref, "YTCI-"))
	assert.Equal(t, "submitted", resp["status"])
	assert.NotContains(t, rec.Body.String(), "amina@example.com")

	// Intent is now consumed: reuse fails.
	rec, resp = h.do(t, "POST", "/v1/public/repository/submissions", body, "", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "UPLOAD_INTENT_EXPIRED", resp["error"].(map[string]any)["code"])

	// Admin queue sees the submission with contact fields and PII.
	rec, list := h.do(t, "GET", "/v1/admin/repository/entries?status=submitted", nil, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, list["items"], 1)
	item := list["items"].([]any)[0].(map[string]any)
	assert.Equal(t, "amina@example.com", item["authorEmail"])
	assert.Equal(t, "+254712345678", item["authorPhone"])
	assert.Equal(t, ref, item["referenceCode"])
	entryID := item["id"].(string)
	slug := item["slug"].(string)

	// Approve then publish, and the public read becomes visible.
	rec, item = h.do(t, "POST", "/v1/admin/repository/entries/"+entryID+"/approve", map[string]any{"note": "Looks good"}, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "approved", item["status"])
	assert.Equal(t, "admin-1", item["reviewedBy"])

	rec, item = h.do(t, "POST", "/v1/admin/repository/entries/"+entryID+"/publish", nil, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "published", item["status"])
	assert.NotNil(t, item["publishedAt"])

	rec, public := h.do(t, "GET", "/v1/public/repository/entries/"+slug, nil, "", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "amina@example.com")
	assert.Contains(t, public["coverUrl"], key)

	// Unpublish removes it from public reads.
	rec, item = h.do(t, "POST", "/v1/admin/repository/entries/"+entryID+"/unpublish", nil, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "approved", item["status"])
	rec, _ = h.do(t, "GET", "/v1/public/repository/entries/"+slug, nil, "", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Reject requires a note and only works from submitted/approved.
	rec, resp = h.do(t, "POST", "/v1/admin/repository/entries/"+entryID+"/reject", map[string]any{"note": "short"}, "administrator", "admin-1")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, item = h.do(t, "POST", "/v1/admin/repository/entries/"+entryID+"/reject", map[string]any{
		"note": "Image resolution is below the print minimum; please resubmit at 300dpi.",
	}, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "rejected", item["status"])

	// Wrong content type is rejected and the object cleaned up.
	key2, _ := h.presign(t, "image", "image/png", 2048, "wrong.png")
	h.store.upload(key2, "application/pdf", 2048)
	rec, resp = h.do(t, "POST", "/v1/public/repository/submissions", validSubmission(key2), "", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "UPLOAD_MISMATCH", resp["error"].(map[string]any)["code"])
	assert.False(t, h.store.has(key2), "mismatched object should be cleaned up")

	// Size mismatch is rejected.
	key3, _ := h.presign(t, "image", "image/jpeg", 4096, "size.jpg")
	h.store.upload(key3, "image/jpeg", 4096+10)
	rec, resp = h.do(t, "POST", "/v1/public/repository/submissions", validSubmission(key3), "", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "UPLOAD_MISMATCH", resp["error"].(map[string]any)["code"])

	// Declared oversize is rejected before an intent exists.
	rec, resp = h.do(t, "POST", "/v1/public/repository/uploads/presign", map[string]any{
		"kind": "image", "fileName": "huge.jpg", "contentType": "image/jpeg", "fileSizeBytes": 9 * 1024 * 1024,
	}, "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "FILE_TOO_LARGE", resp["error"].(map[string]any)["code"])

	rec, resp = h.do(t, "POST", "/v1/public/repository/uploads/presign", map[string]any{
		"kind": "document", "fileName": "essay.docx", "contentType": "application/msword", "fileSizeBytes": 1024,
	}, "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "INVALID_CONTENT_TYPE", resp["error"].(map[string]any)["code"])

	// Honeypot submissions never create a row.
	bot := validSubmission("")
	bot["honeypot"] = "https://spam.example"
	rec, _ = h.do(t, "POST", "/v1/public/repository/submissions", bot, "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSubmissionClosedGuardianConsentAndExpiredIntent(t *testing.T) {
	h := setup(t)

	// Closed by default.
	rec, resp := h.do(t, "POST", "/v1/public/repository/submissions", validSubmission(""), "", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "SUBMISSIONS_CLOSED", resp["error"].(map[string]any)["code"])

	h.openSubmissions(t)

	under18 := validSubmission("")
	under18["authorAgeBand"] = "under_18"
	rec, _ = h.do(t, "POST", "/v1/public/repository/submissions", under18, "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	under18["guardianConsent"] = true
	rec, _ = h.do(t, "POST", "/v1/public/repository/submissions", under18, "", "")
	assert.Equal(t, http.StatusCreated, rec.Code)

	// Unknown county and wrong category are validation errors.
	bad := validSubmission("")
	bad["county"] = "Atlantis"
	rec, resp = h.do(t, "POST", "/v1/public/repository/submissions", bad, "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, fmt.Sprint(resp), "county")

	bad = validSubmission("")
	bad["category"] = "Tourism Tech" // innovation-only category
	rec, _ = h.do(t, "POST", "/v1/public/repository/submissions", bad, "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// A disallowed embed host is a dedicated error.
	bad = validSubmission("")
	bad["embedUrl"] = "https://evil.example/watch"
	rec, resp = h.do(t, "POST", "/v1/public/repository/submissions", bad, "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "INVALID_EMBED_PROVIDER", resp["error"].(map[string]any)["code"])

	// An expired intent cannot be consumed.
	key, _ := h.presign(t, "image", "image/jpeg", 1024, "expired.jpg")
	h.store.upload(key, "image/jpeg", 1024)
	_, err := h.pool.Exec(context.Background(), `
UPDATE repository_upload_intents SET expires_at = now() - interval '1 minute' WHERE object_key = $1`, key)
	require.NoError(t, err)
	rec, resp = h.do(t, "POST", "/v1/public/repository/submissions", validSubmission(key), "", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "UPLOAD_INTENT_EXPIRED", resp["error"].(map[string]any)["code"])
}

func TestSubmissionRateLimit(t *testing.T) {
	h := setupWithBurst(t, 5)
	h.openSubmissions(t)

	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		rec, _ := h.do(t, "POST", "/v1/public/repository/submissions", map[string]any{"honeypot": ""}, "", "")
		last = rec
	}
	assert.Equal(t, http.StatusTooManyRequests, last.Code)
	assert.Equal(t, "RATE_LIMITED", func() string {
		var decoded map[string]any
		_ = json.Unmarshal(last.Body.Bytes(), &decoded)
		return decoded["error"].(map[string]any)["code"].(string)
	}())
}

// ---------------------------------------------------------------------------
// Admin
// ---------------------------------------------------------------------------

func TestAdminAuthorizationAndRoleGuards(t *testing.T) {
	h := setup(t)

	// No role (AdminGate) is a 403.
	rec, _ := h.do(t, "GET", "/v1/admin/repository/entries", nil, "", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	// A plain user is a 403.
	rec, _ = h.do(t, "GET", "/v1/admin/repository/entries", nil, "user", "u1")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	// A content editor is not a repository admin.
	rec, _ = h.do(t, "GET", "/v1/admin/repository/entries", nil, "content_editor", "u2")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	// A moderator can read but cannot create or delete.
	rec, _ = h.do(t, "GET", "/v1/admin/repository/entries", nil, "moderator", "u3")
	assert.Equal(t, http.StatusOK, rec.Code)
	rec, _ = h.do(t, "POST", "/v1/admin/repository/entries", map[string]any{"kind": "experience", "title": "X", "summary": "Y"}, "moderator", "u3")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec, _ = h.do(t, "DELETE", "/v1/admin/repository/entries/00000000-0000-0000-0000-000000000000", nil, "moderator", "u3")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAdminCRUDAndTransitions(t *testing.T) {
	h := setup(t)

	createBody := map[string]any{
		"kind": "experience", "title": "Hell's Gate on foot", "summary": "A guided walking route.",
		"bodyMd": "The gorge is best at dawn.", "county": "Nakuru", "category": "Adventure",
		"tags": []string{"hiking", "geology"}, "authorName": "YTCI Editorial", "authorOrg": "YTCI",
		"externalUrl": "https://example.com/hells-gate", "status": "draft",
	}
	rec, item := h.do(t, "POST", "/v1/admin/repository/entries", createBody, "administrator", "admin-1")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "draft", item["status"])
	assert.Equal(t, "admin", item["source"])
	id := item["id"].(string)
	assert.Equal(t, "hells-gate-on-foot", item["slug"])

	// Duplicate title gets a suffix rather than a 500.
	rec, duplicate := h.do(t, "POST", "/v1/admin/repository/entries", createBody, "administrator", "admin-1")
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.NotEqual(t, item["slug"], duplicate["slug"])

	// Partial update.
	rec, item = h.do(t, "PATCH", "/v1/admin/repository/entries/"+id, map[string]any{"summary": "Updated summary"}, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "Updated summary", item["summary"])

	// Published entries cannot be deleted; draft can.
	rec, _ = h.do(t, "DELETE", "/v1/admin/repository/entries/"+id, nil, "administrator", "admin-1")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Create and publish directly, then feature.
	createBody["status"] = "published"
	rec, item = h.do(t, "POST", "/v1/admin/repository/entries", createBody, "administrator", "admin-1")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "published", item["status"])
	id = item["id"].(string)

	rec, item = h.do(t, "POST", "/v1/admin/repository/entries/"+id+"/feature", map[string]any{"featured": true}, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, true, item["featured"])

	// Invalid transitions are 409.
	rec, _ = h.do(t, "POST", "/v1/admin/repository/entries/"+id+"/approve", nil, "administrator", "admin-1")
	assert.Equal(t, http.StatusConflict, rec.Code)
	rec, _ = h.do(t, "DELETE", "/v1/admin/repository/entries/"+id, nil, "administrator", "admin-1")
	assert.Equal(t, http.StatusConflict, rec.Code)

	// Archive via patch, then delete.
	rec, item = h.do(t, "PATCH", "/v1/admin/repository/entries/"+id, map[string]any{"status": "archived"}, "administrator", "admin-1")
	assert.Equal(t, http.StatusConflict, rec.Code) // published -> archived is not a direct patch

	rec, item = h.do(t, "POST", "/v1/admin/repository/entries/"+id+"/unpublish", nil, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "approved", item["status"])
	rec, _ = h.do(t, "DELETE", "/v1/admin/repository/entries/"+id, nil, "administrator", "admin-1")
	assert.Equal(t, http.StatusConflict, rec.Code)

	rec, item = h.do(t, "PATCH", "/v1/admin/repository/entries/"+id, map[string]any{"status": "draft"}, "administrator", "admin-1")
	assert.Equal(t, http.StatusConflict, rec.Code) // approved -> draft is not a patch transition

	// Admin list includes every status and contact fields.
	h.seed(t, seedEntry{Kind: "innovation", Slug: "with-pii", Title: "With PII", Status: "submitted", County: "Nairobi", AuthorEmail: "person@example.com"})
	rec, list := h.do(t, "GET", "/v1/admin/repository/entries", nil, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code)
	found := false
	for _, raw := range list["items"].([]any) {
		entry := raw.(map[string]any)
		if entry["slug"] == "with-pii" {
			found = true
			assert.Equal(t, "person@example.com", entry["authorEmail"])
		}
	}
	assert.True(t, found)

	// Admin settings round-trip.
	rec, settings := h.do(t, "GET", "/v1/admin/repository/settings", nil, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "default", settings["id"])
	rec, settings = h.do(t, "PATCH", "/v1/admin/repository/settings", map[string]any{
		"submissionsOpen": true, "submissionIntro": "Open for 2026", "termsVersion": "2026-12",
	}, "administrator", "admin-1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, true, settings["submissionsOpen"])
	assert.Equal(t, "2026-12", settings["termsVersion"])

	// Public settings reflect the change.
	_, settings = h.do(t, "GET", "/v1/public/repository/settings", nil, "", "")
	assert.Equal(t, true, settings["submissionsOpen"])
	assert.Equal(t, "2026-12", settings["termsVersion"])
}

func TestAdminPresignCreatesMedia(t *testing.T) {
	h := setup(t)
	rec, body := h.do(t, "POST", "/v1/admin/repository/uploads/presign", map[string]any{
		"kind": "image", "fileName": "cover.jpg", "contentType": "image/jpeg", "fileSizeBytes": 2048,
	}, "administrator", "admin-1")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	mediaID := body["mediaId"].(string)
	require.NotEmpty(t, mediaID)
	assert.Contains(t, body["objectKey"], "repository/admin/")

	var kind, status string
	err := h.pool.QueryRow(context.Background(), `SELECT type, status FROM media_assets WHERE id = $1`, mediaID).Scan(&kind, &status)
	require.NoError(t, err)
	assert.Equal(t, "image", kind)
	assert.Equal(t, "ready", status)
}
