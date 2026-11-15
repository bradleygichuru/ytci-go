package push

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestSendMessagesKeepsTicketsPerMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"status":"ok","id":"ticket-1"},{"status":"ok","id":"ticket-2"}]}`))
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), pushAPIURL: server.URL}
	result, err := client.SendMessages(context.Background(), []ExpoPushMessage{
		{To: "ExponentPushToken[same]", Title: "one"},
		{To: "ExponentPushToken[same]", Title: "two"},
	})
	if err != nil {
		t.Fatalf("SendMessages: %v", err)
	}
	assert.Equal(t, "ticket-1", result.TicketForIndex[0])
	assert.Equal(t, "ticket-2", result.TicketForIndex[1])
}
