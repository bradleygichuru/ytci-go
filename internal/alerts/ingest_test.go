package alerts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestIngestKMDEndToEnd(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `TRUNCATE kenya_counties CASCADE`)
	_, _ = pool.Exec(ctx, `TRUNCATE kenya_counties CASCADE`)
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode, geom) VALUES
		(1, 'Nairobi', 'KE047', ST_GeomFromText('MULTIPOLYGON(((36.7 -1.2, 36.9 -1.2, 36.9 -1.4, 36.7 -1.4, 36.7 -1.2)))', 4326)),
		(2, 'Kiambu', 'KE022', ST_GeomFromText('MULTIPOLYGON(((36.5 -0.9, 36.7 -0.9, 36.7 -1.2, 36.5 -1.2, 36.5 -0.9)))', 4326)),
		(3, 'Machakos', 'KE016', ST_GeomFromText('MULTIPOLYGON(((37.0 -1.1, 37.7 -1.1, 37.7 -1.8, 37.0 -1.8, 37.0 -1.1)))', 4326)) ON CONFLICT (gid) DO UPDATE SET adm1_name=EXCLUDED.adm1_name, adm1_pcode=EXCLUDED.adm1_pcode`)
	require.NoError(t, err)

	var serverURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rss.xml":
			fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel>
			  <lastBuildDate>Sat, 24 Oct 2026 15:47:00 +0000</lastBuildDate>
			  <item><title>Heavy Rainfall Advisory</title><link>%s/cap.xml</link>
			  <pubDate>Sat, 24 Oct 2026 15:47:00 +0000</pubDate>
			  <guid>urn:test:1</guid></item></channel></rss>`, serverURL)
		case "/cap.xml":
			_, _ = w.Write([]byte(sampleCAP))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	serverURL = srv.URL

	store := NewStore(pool)
	ingester, err := NewIngester(ctx, store, srv.URL+"/rss.xml", srv.URL+"/fcdo.json")
	require.NoError(t, err)

	result := ingester.IngestKMD(ctx)
	require.NoError(t, result.Err)
	assert.Equal(t, 1, result.Created)

	var alertID, severity, mappingMethod string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT id, severity, mapping_method FROM alerts WHERE source_identifier=$1`,
		"urn:oid:2.49.0.1.404.0.2026.10.24.15.47.0").Scan(&alertID, &severity, &mappingMethod))
	assert.Equal(t, "Severe", severity)
	assert.Equal(t, MethodAreaDesc, mappingMethod)

	var countyCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM alert_areas WHERE alert_id=$1`, alertID).Scan(&countyCount))
	assert.Equal(t, 3, countyCount)

	var jobs int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM alert_fanout_jobs WHERE alert_id=$1`, alertID).Scan(&jobs))
	assert.Equal(t, 1, jobs)

	var sourceStatus string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM alert_source_runs WHERE source_id='kmd' ORDER BY id DESC LIMIT 1`).Scan(&sourceStatus))
	assert.Equal(t, "ok", sourceStatus)
	assert.Contains(t, strings.ToLower(sampleCAP), "nairobi")
}

func TestFetchSendsConditionalHeaders(t *testing.T) {
	var gotETag, gotModified string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotETag = r.Header.Get("If-None-Match")
		gotModified = r.Header.Get("If-Modified-Since")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	ing := &Ingester{client: &http.Client{Timeout: time.Second}}
	_, status, _, _, err := ing.fetch(context.Background(), srv.URL, 1024,
		`"abc123"`, "Sat, 24 Oct 2026 15:47:00 GMT")
	require.ErrorIs(t, err, errNotModified)
	assert.Equal(t, http.StatusNotModified, status)
	assert.Equal(t, `"abc123"`, gotETag)
	assert.Equal(t, "Sat, 24 Oct 2026 15:47:00 GMT", gotModified)
}

func TestDueHonoursPollIntervalAndBackoff(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)
	ing := &Ingester{store: store}

	assert.True(t, ing.due(ctx, "kmd", 5*time.Minute), "first check is always due")

	_, err := pool.Exec(ctx, `UPDATE alert_sources SET last_checked_at=now(), consecutive_failures=0 WHERE id='kmd'`)
	require.NoError(t, err)
	assert.False(t, ing.due(ctx, "kmd", 5*time.Minute))

	_, err = pool.Exec(ctx, `UPDATE alert_sources SET last_checked_at=now()-interval '10 minutes' WHERE id='kmd'`)
	require.NoError(t, err)
	assert.True(t, ing.due(ctx, "kmd", 5*time.Minute))

	_, err = pool.Exec(ctx, `UPDATE alert_sources SET last_checked_at=now()-interval '10 minutes', consecutive_failures=2 WHERE id='kmd'`)
	require.NoError(t, err)
	assert.False(t, ing.due(ctx, "kmd", 5*time.Minute), "failures back off beyond the base interval")
}

func TestDueHonoursSourceEnabledAndInterval(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `UPDATE alert_sources
		SET enabled=false, poll_interval_seconds=600, last_checked_at=now()
		WHERE id='kmd'`)
	require.NoError(t, err)
	ing := &Ingester{store: NewStore(pool)}
	assert.False(t, ing.due(ctx, "kmd", 5*time.Minute), "disabled sources are skipped even before the first poll")
	_, err = pool.Exec(ctx, `UPDATE alert_sources SET last_checked_at=NULL WHERE id='kmd'`)
	require.NoError(t, err)
	assert.False(t, ing.due(ctx, "kmd", 5*time.Minute), "disabled never-polled sources are skipped")

	_, err = pool.Exec(ctx, `UPDATE alert_sources SET enabled=true, last_checked_at=now() - interval '5 minutes' WHERE id='kmd'`)
	require.NoError(t, err)
	assert.False(t, ing.due(ctx, "kmd", 5*time.Minute), "the source interval wins over the fallback")

	_, err = pool.Exec(ctx, `UPDATE alert_sources SET last_checked_at=now() - interval '11 minutes' WHERE id='kmd'`)
	require.NoError(t, err)
	assert.True(t, ing.due(ctx, "kmd", 5*time.Minute))
}
