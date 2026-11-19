package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

// speciesFixture is a signed-in actor for the species vertical.
type speciesFixture struct {
	ts   *httptest.Server
	pool *pgxpool.Pool
	ctx  context.Context
}

func setupSpecies(t *testing.T) *speciesFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	t.Cleanup(pool.Close)
	setupAuthTables(t, ctx, pool)

	actors := map[string]string{
		"sp-editor":     "content_editor",
		"sp-expert":     "species_expert",
		"sp-translator": "translator",
		"sp-reviewer":   "language_reviewer",
		"sp-rights":     "rights_reviewer",
		"sp-admin":      "administrator",
		"sp-super":      "super_admin",
		"sp-reader":     "user",
	}
	for id, role := range actors {
		createSession(t, ctx, pool, id, id+"@example.test", id)
		_, err := pool.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, id, role)
		require.NoError(t, err)
	}

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored",
		AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	ts := httptest.NewServer(server.New(cfg, pool))
	t.Cleanup(ts.Close)
	return &speciesFixture{ts: ts, pool: pool, ctx: ctx}
}

func (f *speciesFixture) as(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func (f *speciesFixture) createSpecies(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	resp := doJSON(t, f.ts, http.MethodPost, "/v1/admin/species", body, f.as("sp-editor"))
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	return decodeBody(t, resp)
}

func (f *speciesFixture) addSource(t *testing.T, speciesID string) {
	t.Helper()
	resp := doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/sources", map[string]any{
		"speciesId":    speciesID,
		"publisher":    "GBIF",
		"citation":     "GBIF Backbone Taxonomy. doi:10.15468/39omei",
		"url":          "https://www.gbif.org/species/5219404",
		"accessedAt":   "2026-09-01",
		"licenceCode":  "CC-BY-4.0",
		"licenceUrl":   "https://creativecommons.org/licenses/by/4.0/",
		"permittedUse": "taxonomy_only",
		"attribution":  "GBIF.org (2026) GBIF Backbone Taxonomy",
	}, f.as("sp-editor"))
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
}

func (f *speciesFixture) upsertVariant(t *testing.T, token, speciesID, language, translator string) {
	t.Helper()
	body := map[string]any{
		"language":     language,
		"coverageTier": "full",
		"summary":      "Summary in " + language,
		"description":  "Description in " + language,
		"fieldStatus":  map[string]string{"summary": "reviewed"},
	}
	if translator != "" {
		body["translatorRef"] = translator
	}
	resp := doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/"+speciesID+"/variants", body, f.as(token))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func (f *speciesFixture) pendingReview(t *testing.T, speciesID, stage string) string {
	t.Helper()
	var id string
	err := f.pool.QueryRow(f.ctx, `
		SELECT id FROM species_review_queue
		 WHERE species_id = $1 AND stage = $2 AND status = 'pending'
		 ORDER BY submitted_at LIMIT 1`, speciesID, stage).Scan(&id)
	require.NoError(t, err, "pending %s review", stage)
	return id
}

func (f *speciesFixture) decide(t *testing.T, token, itemID, decision string) *http.Response {
	t.Helper()
	return doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/review/"+itemID+"/decision",
		map[string]any{"decision": decision}, f.as(token))
}

// TestSpeciesPublishLifecycle covers source/rights/review gates, visible EN
// fallback and stale propagation after a source re-check.
func TestSpeciesPublishLifecycle(t *testing.T) {
	f := setupSpecies(t)

	species := f.createSpecies(t, map[string]any{
		"scientificName": "Panthera leo",
		"slug":           "panthera-leo",
	})
	id := species["id"].(string)
	require.Equal(t, "draft", species["publicationState"])

	f.addSource(t, id)

	// English source document, reviewed by the species expert.
	f.upsertVariant(t, "sp-expert", id, "en", "")
	resp := f.decide(t, "sp-expert", f.pendingReview(t, id, "expert"), "approved")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Swahili translation. The same actor cannot also language-review it.
	f.upsertVariant(t, "sp-translator", id, "sw", "sp-reviewer")
	resp = f.decide(t, "sp-reviewer", f.pendingReview(t, id, "language"), "approved")
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	var selfReview map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&selfReview))
	resp.Body.Close()
	assert.Equal(t, "SELF_REVIEW", selfReview["error"].(map[string]any)["code"])

	// Re-submit with the real translator, then a different reviewer approves.
	f.upsertVariant(t, "sp-translator", id, "sw", "")
	resp = f.decide(t, "sp-reviewer", f.pendingReview(t, id, "language"), "approved")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// A media asset with a rights record. The rights reviewer must not be the
	// uploader.
	mediaResp := doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/"+id+"/media", map[string]any{
		"mediaKind":    "image",
		"storageKey":   "species/lion.jpg",
		"credit":       "YTCI media team",
		"rightsHolder": "YTCI",
		"licenceCode":  "CC-BY-4.0",
		"alt":          map[string]string{"en": "A male lion resting in tall grass."},
	}, f.as("sp-editor"))
	require.Equal(t, http.StatusCreated, mediaResp.StatusCode)
	media := decodeBody(t, mediaResp)
	mediaID := media["id"].(string)

	rightsResp := doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/rights", map[string]any{
		"subjectType":  "media",
		"subjectId":    mediaID,
		"rightsHolder": "YTCI",
		"licenceCode":  "CC-BY-4.0",
		"permittedUse": "media",
		"attribution":  "YTCI media team",
	}, f.as("sp-rights"))
	require.Equal(t, http.StatusCreated, rightsResp.StatusCode)
	rightsResp.Body.Close()

	// The rights reviewer uploaded this asset elsewhere: self-review is blocked.
	_, err := f.pool.Exec(f.ctx, `UPDATE species_media SET created_by = 'sp-rights' WHERE id = $1`, mediaID)
	require.NoError(t, err)
	rightsItem := f.pendingReview(t, id, "rights")
	resp = f.decide(t, "sp-rights", rightsItem, "approved")
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()
	_, err = f.pool.Exec(f.ctx, `UPDATE species_media SET created_by = 'sp-editor' WHERE id = $1`, mediaID)
	require.NoError(t, err)
	resp = f.decide(t, "sp-rights", rightsItem, "approved")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Publish both variants as administrator.
	for _, language := range []string{"en", "sw"} {
		resp = doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/"+id+"/publish",
			map[string]any{"language": language}, f.as("sp-admin"))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// Swahili resolves without fallback while it is reviewed.
	resp = doJSON(t, f.ts, http.MethodGet, "/v1/public/species/panthera-leo?language=sw", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	detail := decodeBody(t, resp)
	assert.Equal(t, "sw", detail["resolvedLanguage"])
	assert.Nil(t, detail["fallback"])
	assert.Contains(t, detail["content"].(map[string]any)["summary"], "sw")

	// Search over the published catalogue, scoped to a language with content.
	resp = doJSON(t, f.ts, http.MethodGet, "/v1/public/species?q=panthera&language=sw", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	list := decodeBody(t, resp)
	require.Len(t, list["items"].([]any), 1)
	resp.Body.Close()

	// A source re-check bumps the content version and stales translations.
	f.addSource(t, id)
	resp = doJSON(t, f.ts, http.MethodGet, "/v1/public/species/panthera-leo?language=sw", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	stale := decodeBody(t, resp)
	assert.Equal(t, "en", stale["resolvedLanguage"])
	fallback := stale["fallback"].(map[string]any)
	assert.Equal(t, true, fallback["used"])
	assert.Equal(t, "stale", fallback["reason"])
	assert.Equal(t, "English", fallback["chip"])

	// The reader save list is account-scoped.
	resp = doJSON(t, f.ts, http.MethodPost, "/v1/mobile/species/panthera-leo/save", nil, f.as("sp-reader"))
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = doJSON(t, f.ts, http.MethodGet, "/v1/mobile/species/saved", nil, f.as("sp-reader"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	saved := decodeBody(t, resp)
	require.Len(t, saved["items"].([]any), 1)
	resp.Body.Close()

	// Rights withdrawal tombstones the record and returns 410 to readers.
	resp = doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/"+id+"/unpublish",
		map[string]any{"reason": "rights holder withdrew permission", "tombstone": true}, f.as("sp-super"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	withdrawn := decodeBody(t, resp)
	assert.Equal(t, "tombstoned", withdrawn["publicationState"])
	assert.EqualValues(t, 2, withdrawn["contentVersion"])

	resp = doJSON(t, f.ts, http.MethodGet, "/v1/public/species/panthera-leo", nil, nil)
	require.Equal(t, http.StatusGone, resp.StatusCode)
	resp.Body.Close()

	var staleContent int
	err = f.pool.QueryRow(f.ctx, `
		SELECT count(*) FROM species_content WHERE species_id = $1 AND review_status = 'stale'`, id).Scan(&staleContent)
	require.NoError(t, err)
	assert.Equal(t, 2, staleContent)
	var withdrawnRights int
	err = f.pool.QueryRow(f.ctx, `
		SELECT count(*) FROM species_rights WHERE subject_id = $1 AND withdrawn_at IS NOT NULL`, mediaID).Scan(&withdrawnRights)
	require.NoError(t, err)
	assert.Equal(t, 1, withdrawnRights)
}

// TestSpeciesPublishGateBlocksMissingSource proves no source means no publish.
func TestSpeciesPublishGateBlocksMissingSource(t *testing.T) {
	f := setupSpecies(t)
	species := f.createSpecies(t, map[string]any{
		"scientificName": "Loxodonta africana",
		"slug":           "loxodonta-africana",
	})
	id := species["id"].(string)
	f.upsertVariant(t, "sp-expert", id, "en", "")
	resp := f.decide(t, "sp-expert", f.pendingReview(t, id, "expert"), "approved")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/"+id+"/publish",
		map[string]any{"language": "en"}, f.as("sp-admin"))
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "SOURCE_MISSING", body["error"].(map[string]any)["code"])
}

// TestSpeciesSourceLicenceGuards proves IUCN prose, KWS bulk copy and
// non-commercial GBIF occurrences are rejected at the source boundary.
func TestSpeciesSourceLicenceGuards(t *testing.T) {
	f := setupSpecies(t)
	species := f.createSpecies(t, map[string]any{
		"scientificName": "Panthera pardus",
		"slug":           "panthera-pardus",
	})
	id := species["id"].(string)

	cases := []map[string]any{
		{
			"publisher": "IUCN", "citation": "Red List narrative",
			"accessedAt": "2026-09-01", "licenceCode": "IUCN-RL",
			"permittedUse": "prose", "attribution": "IUCN",
		},
		{
			"publisher": "KWS", "citation": "KWS park report",
			"accessedAt": "2026-09-01", "licenceCode": "ALL-RIGHTS-RESERVED",
			"permittedUse": "prose", "attribution": "KWS",
		},
		{
			"publisher": "GBIF", "citation": "GBIF occurrence download",
			"accessedAt": "2026-09-01", "licenceCode": "CC-BY-NC-4.0",
			"permittedUse": "occurrence", "attribution": "GBIF.org",
		},
	}
	for _, body := range cases {
		body["speciesId"] = id
		resp := doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/sources", body, f.as("sp-editor"))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, "publisher %v", body["publisher"])
		resp.Body.Close()
	}

	// KWS link-out is allowed without a permission reference.
	resp := doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/sources", map[string]any{
		"speciesId": id, "publisher": "KWS", "citation": "KWS report landing page",
		"url": "https://www.kws.go.ke/reports", "accessedAt": "2026-09-01",
		"licenceCode": "LINK-ONLY", "permittedUse": "link_only", "attribution": "KWS",
	}, f.as("sp-editor"))
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
}

// TestSpeciesTranslatorCannotPublish proves role separation at the HTTP edge.
func TestSpeciesTranslatorCannotPublish(t *testing.T) {
	f := setupSpecies(t)
	species := f.createSpecies(t, map[string]any{
		"scientificName": "Diceros bicornis",
		"slug":           "diceros-bicornis",
	})
	id := species["id"].(string)
	resp := doJSON(t, f.ts, http.MethodPost, "/v1/admin/species/"+id+"/publish",
		map[string]any{"language": "en"}, f.as("sp-translator"))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// A plain reader cannot read drafts through the admin surface.
	resp = doJSON(t, f.ts, http.MethodGet, "/v1/admin/species/"+id, nil, f.as("sp-reader"))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}
