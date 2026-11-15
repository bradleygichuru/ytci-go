package push

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildMessageCarriesDataImageAndDefaultSound(t *testing.T) {
	original := map[string]any{"alertId": "alert-1"}
	message := BuildMessage(MessageSpec{
		To: "ExponentPushToken[test]", Title: "Heavy rainfall", Body: "Move to higher ground.",
		Data: original, ImageURL: "https://cdn.example/alert.png",
	})

	assert.Equal(t, "default", message.Sound)
	data, ok := message.Data.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "alert-1", data["alertId"])
	assert.Equal(t, "https://cdn.example/alert.png", data["imageUrl"])
	assert.NotContains(t, original, "imageUrl", "the caller's data map must not be mutated")

	empty := BuildMessage(MessageSpec{To: "t", Title: "x", Body: "y"})
	assert.Nil(t, empty.Data)
	assert.Equal(t, "default", empty.Sound)
}
