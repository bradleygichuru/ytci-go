package admin

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/thennow"
)

// keyEchoStore returns a URL that carries the object key so a test can assert
// which object was signed without relying on a fixed stub value.
type keyEchoStore struct {
	getErr error
}

func (keyEchoStore) PresignedPutURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (s keyEchoStore) PresignedGetURL(_ context.Context, key string, _ time.Duration) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	return "https://r2.example/" + key + "?sig=1", nil
}

func (keyEchoStore) PutObject(context.Context, string, io.Reader, string) error { return nil }
func (keyEchoStore) DeleteObject(context.Context, string) error                 { return nil }

func publishedSnapshot() thennow.Snapshot {
	return thennow.Snapshot{
		VideoMaster:         "then-now/railway/master.mp4",
		RenditionKeys:       map[string]string{"1080": "then-now/railway/site-1080.mp4", "720": "then-now/railway/site-720.mp4"},
		PosterKey:           "then-now/railway/poster.jpg",
		CaptionKeys:         map[string]string{"en": "then-now/railway/en.vtt"},
		TranscriptKey:       "then-now/railway/transcript.txt",
		AudioDescriptionKey: "then-now/railway/ad.vtt",
	}
}

func TestSignedMediaSignsEveryObject(t *testing.T) {
	h := NewThenNowHandler(nil, keyEchoStore{}, 10*time.Minute)

	media := h.signedMedia(context.Background(), publishedSnapshot())
	if media == nil {
		t.Fatal("expected signed media")
	}

	for name, key := range publishedSnapshot().RenditionKeys {
		if !strings.Contains(media.Video[name], key) {
			t.Errorf("rendition %s signed %q, want key %q", name, media.Video[name], key)
		}
	}
	if !strings.Contains(media.Master, "master.mp4") {
		t.Errorf("master not signed: %q", media.Master)
	}
	if !strings.Contains(media.Poster, "poster.jpg") {
		t.Errorf("poster not signed: %q", media.Poster)
	}
	if !strings.Contains(media.Captions["en"], "en.vtt") {
		t.Errorf("caption not signed: %q", media.Captions["en"])
	}
	if !strings.Contains(media.Transcript, "transcript.txt") {
		t.Errorf("transcript not signed: %q", media.Transcript)
	}
	if !strings.Contains(media.AudioDescription, "ad.vtt") {
		t.Errorf("audio description not signed: %q", media.AudioDescription)
	}

	ttl := time.Until(media.ExpiresAt)
	if ttl <= 9*time.Minute || ttl > 10*time.Minute {
		t.Errorf("expiry %v, want about 10m", ttl)
	}
}

func TestSignedMediaSkipsMissingKeys(t *testing.T) {
	h := NewThenNowHandler(nil, keyEchoStore{}, time.Minute)

	media := h.signedMedia(context.Background(), thennow.Snapshot{
		RenditionKeys: map[string]string{"720": "then-now/railway/site-720.mp4", "540": ""},
	})
	if media == nil {
		t.Fatal("expected signed media for the one real key")
	}
	if len(media.Video) != 1 || media.Video["720"] == "" {
		t.Errorf("video = %v, want only the 720 rendition", media.Video)
	}
	if media.Poster != "" || media.Master != "" || len(media.Captions) != 0 {
		t.Errorf("unexpected signatures for absent keys: %+v", media)
	}
}

func TestSignedMediaWithoutStore(t *testing.T) {
	h := NewThenNowHandler(nil, nil, time.Minute)
	if media := h.signedMedia(context.Background(), publishedSnapshot()); media != nil {
		t.Fatalf("expected nil media when no store is configured, got %+v", media)
	}
}

func TestSignedMediaOmitsFailedSignatures(t *testing.T) {
	h := NewThenNowHandler(nil, keyEchoStore{getErr: io.ErrClosedPipe}, time.Minute)
	if media := h.signedMedia(context.Background(), publishedSnapshot()); media != nil {
		t.Fatalf("expected nil media when signing fails, got %+v", media)
	}
}
