package alerts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCounties() []County {
	return []County{
		{GID: 1, Name: "Nairobi", PCode: "KE047"},
		{GID: 2, Name: "Kiambu", PCode: "KE022"},
		{GID: 3, Name: "Machakos", PCode: "KE016"},
		{GID: 4, Name: "Mombasa", PCode: "KE001"},
		{GID: 5, Name: "Kilifi", PCode: "KE003"},
		{GID: 6, Name: "Kwale", PCode: "KE002"},
		{GID: 7, Name: "Lamu", PCode: "KE005"},
		{GID: 8, Name: "Tana River", PCode: "KE004"},
		{GID: 9, Name: "Garissa", PCode: "KE007"},
		{GID: 10, Name: "Wajir", PCode: "KE008"},
		{GID: 11, Name: "Mandera", PCode: "KE009"},
		{GID: 12, Name: "Isiolo", PCode: "KE010"},
		{GID: 13, Name: "Marsabit", PCode: "KE011"},
		{GID: 14, Name: "Kakamega", PCode: "KE037"},
		{GID: 15, Name: "Bungoma", PCode: "KE039"},
	}
}

func TestMatchAreaDescExactCounties(t *testing.T) {
	idx := NewCountyIndex(testCounties())
	got := idx.MatchAreaDesc("Nairobi, Kiambu, Machakos")

	assert.Equal(t, MethodAreaDesc, got.Method)
	assert.False(t, got.Review)
	assert.Equal(t, []int{1, 2, 3}, countyGIDs(got.Counties))
	assert.Empty(t, got.Unmatched)
}

func TestMatchAreaDescAliasesAndRegions(t *testing.T) {
	idx := NewCountyIndex(testCounties())
	got := idx.MatchAreaDesc("Coastal kenya:Mombasa,Kilifi,Kwale,Lamu,Tanariver.")

	assert.Equal(t, MethodAreaDesc, got.Method)
	assert.False(t, got.Review)
	assert.ElementsMatch(t, []int{4, 5, 6, 7, 8}, countyGIDs(got.Counties))
	assert.Empty(t, got.Unmatched)
}

func TestMatchAreaDescNorthernIsAmbiguous(t *testing.T) {
	idx := NewCountyIndex(testCounties())
	got := idx.MatchAreaDesc("Coast, Western, Northern, Central and Eastern Kenya")

	assert.True(t, got.Review)
	assert.Contains(t, got.Ambiguous, "northern")
	assert.Contains(t, countyGIDs(got.Counties), 4)  // Coast: Mombasa
	assert.Contains(t, countyGIDs(got.Counties), 14) // Western: Kakamega
}

func TestMatchAreaDescUnmatchedToken(t *testing.T) {
	idx := NewCountyIndex(testCounties())
	got := idx.MatchAreaDesc("Nairobi, Atlantis")

	assert.Equal(t, []int{1}, countyGIDs(got.Counties))
	assert.Contains(t, got.Unmatched, "Atlantis")
	assert.False(t, got.Review)
}

func TestCountyIndexRequiresCounties(t *testing.T) {
	idx := NewCountyIndex(nil)
	got := idx.MatchAreaDesc("Nairobi")
	require.Empty(t, got.Counties)
	assert.Equal(t, MethodUnmapped, got.Method)
	assert.True(t, got.Review)
}

func countyGIDs(matches []CountyMatch) []int {
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.County.GID)
	}
	return out
}
