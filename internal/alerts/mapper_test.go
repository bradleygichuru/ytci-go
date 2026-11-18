package alerts

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestMapperUnionsRegionalTextWithPolygon(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode, geom) VALUES
		(1, 'Nairobi', 'KE047', ST_GeomFromText('MULTIPOLYGON(((36.7 -1.2, 36.9 -1.2, 36.9 -1.4, 36.7 -1.4, 36.7 -1.2)))', 4326)),
		(2, 'Kiambu', 'KE022', ST_GeomFromText('MULTIPOLYGON(((36.6 -1.1, 36.7 -1.1, 36.7 -1.2, 36.6 -1.2, 36.6 -1.1)))', 4326))`)
	require.NoError(t, err)

	mapper, err := NewMapper(ctx, NewStore(pool))
	require.NoError(t, err)

	// The regional text expands to Kiambu; the polygon also clips Nairobi.
	// Neither alone is complete, so the union is recorded as mixed.
	mapping, err := mapper.MapInfo(ctx, CAPInfo{
		Areas: []CAPArea{{
			AreaDesc: "Central",
			Polygons: []string{"-1.1,36.65 -1.1,36.85 -1.3,36.85 -1.3,36.65 -1.1,36.65"},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, MethodMixed, mapping.Method)
	assert.ElementsMatch(t, []int{1, 2}, countyGIDs(mapping.Counties))
}

func TestMapperKeepsExactCountyListAuthoritative(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode, geom) VALUES
		(1, 'Nairobi', 'KE047', ST_GeomFromText('MULTIPOLYGON(((36.7 -1.2, 36.9 -1.2, 36.9 -1.4, 36.7 -1.4, 36.7 -1.2)))', 4326)),
		(2, 'Kiambu', 'KE022', ST_GeomFromText('MULTIPOLYGON(((36.6 -1.1, 36.7 -1.1, 36.7 -1.2, 36.6 -1.2, 36.6 -1.1)))', 4326))`)
	require.NoError(t, err)

	mapper, err := NewMapper(ctx, NewStore(pool))
	require.NoError(t, err)
	mapping, err := mapper.MapInfo(ctx, CAPInfo{
		Areas: []CAPArea{{
			AreaDesc: "Nairobi",
			Polygons: []string{"-1.1,36.65 -1.1,36.85 -1.3,36.85 -1.3,36.65 -1.1,36.65"},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, MethodAreaDesc, mapping.Method)
	assert.Equal(t, []int{1}, countyGIDs(mapping.Counties))
}
