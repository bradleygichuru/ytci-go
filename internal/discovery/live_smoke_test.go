//go:build live

package discovery_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/discovery"
	"github.com/bradleygichuru/ytci-go/internal/places"
)

func TestLiveDiscoverySmoke(t *testing.T) {
	key := os.Getenv("GOOGLE_PLACES_API_KEY")
	if key == "" || os.Getenv("LIVE_PLACES_SMOKE") != "1" {
		t.Skip("set GOOGLE_PLACES_API_KEY and LIVE_PLACES_SMOKE=1 to run")
	}

	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	adapter := discovery.NewAdapter(pool, places.NewClient(key), 25)
	for _, county := range []string{"023", "003", "005"} { // Turkana, Kilifi, Lamu
		result, err := adapter.DiscoverCounty(ctx, county)
		require.NoError(t, err, county)
		t.Logf("county=%s candidates=%d requests=%d cached=%v",
			county, len(result.Candidates), result.RequestCount, result.Cached)
	}
}
