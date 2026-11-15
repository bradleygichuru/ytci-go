package alerts

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleFCDO = `{
  "public_updated_at": "2026-11-06T17:00:48Z",
  "details": {
    "change_description": "Updated advice on the Moyale border.",
    "parts": [
      {"slug": "safety-and-security", "title": "Safety and security", "body": "<p>Terrorists are very likely to try to carry out attacks.</p><p>Stay aware of your surroundings.</p>"},
      {"slug": "regional-risks", "title": "Regional risks", "body": "<p>Border areas require care.</p>"}
    ]
  }
}`

func TestParseFCDO(t *testing.T) {
	advice, err := ParseFCDO(strings.NewReader(sampleFCDO))
	require.NoError(t, err)

	assert.Equal(t, time.Date(2026, 11, 6, 17, 0, 48, 0, time.UTC), advice.PublicUpdatedAt)
	assert.Equal(t, "Safety and security", advice.SafetyTitle)
	assert.Equal(t, "Terrorists are very likely to try to carry out attacks.\n\nStay aware of your surroundings.", advice.SafetyText)
	assert.NotEmpty(t, advice.ContentHash)
}

func TestFCDOContentHashChanges(t *testing.T) {
	a, err := ParseFCDO(strings.NewReader(sampleFCDO))
	require.NoError(t, err)
	changed := strings.Replace(sampleFCDO, "Stay aware of your surroundings.", "Avoid all travel.", 1)
	b, err := ParseFCDO(strings.NewReader(changed))
	require.NoError(t, err)
	assert.NotEqual(t, a.ContentHash, b.ContentHash)
}
