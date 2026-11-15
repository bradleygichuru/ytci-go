package alerts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestIngestKMDEndToEnd(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode, geom)
		VALUES (1, 'Nairobi', 'KE047', ST_GeomFromText('MULTIPOLYGON(((36.7 -1.2, 36.9 -1.2, 36.9 -1.4, 36.7 -1.4, 36.7 -1.2)))', 4326))`)
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
	assert.Equal(t, 1, countyCount)

	var jobs int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM alert_fanout_jobs WHERE alert_id=$1`, alertID).Scan(&jobs))
	assert.Equal(t, 1, jobs)

	var sourceStatus string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM alert_source_runs WHERE source_id='kmd' ORDER BY id DESC LIMIT 1`).Scan(&sourceStatus))
	assert.Equal(t, "ok", sourceStatus)
	assert.Contains(t, strings.ToLower(sampleCAP), "nairobi")
}
