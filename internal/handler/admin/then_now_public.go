package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/thennow"
)

type publishedExperience struct {
	ID                  string             `json:"id"`
	Title               string             `json:"title"`
	Promise             string             `json:"promise"`
	Story               string             `json:"story"`
	Period              string             `json:"period"`
	Duration            int                `json:"duration"`
	PrimaryMode         string             `json:"primaryMode"`
	Languages           []string           `json:"languages"`
	TextCards           []thennow.TextCard `json:"textCards"`
	VideoMaster         string             `json:"videoMaster"`
	VideoSize           string             `json:"videoSize"`
	Renditions          map[string]string  `json:"renditions"`
	RenditionKeys       map[string]string  `json:"renditionKeys"`
	Poster              bool               `json:"poster"`
	PosterKey           string             `json:"posterKey"`
	Captions            map[string]bool    `json:"captions"`
	CaptionKeys         map[string]string  `json:"captionKeys"`
	Transcript          bool               `json:"transcript"`
	TranscriptKey       string             `json:"transcriptKey"`
	AudioDescription    bool               `json:"audioDescription"`
	AudioDescriptionKey string             `json:"audioDescriptionKey"`
	Media               *publishedMedia    `json:"media,omitempty"`
}

// publishedMedia holds short-lived signed URLs for the assets a player needs.
// Object keys stay in the payload so a client whose URL expired can ask for a
// fresh site document instead of re-deriving storage paths.
type publishedMedia struct {
	Video            map[string]string `json:"video,omitempty"`
	Master           string            `json:"master,omitempty"`
	Poster           string            `json:"poster,omitempty"`
	Captions         map[string]string `json:"captions,omitempty"`
	Transcript       string            `json:"transcript,omitempty"`
	AudioDescription string            `json:"audioDescription,omitempty"`
	ExpiresAt        time.Time         `json:"expiresAt"`
}

type publishedSite struct {
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	Slug              string               `json:"slug"`
	County            string               `json:"county"`
	Locality          string               `json:"locality"`
	SiteType          string               `json:"siteType"`
	AccessMode        string               `json:"accessMode"`
	Sensitivity       string               `json:"sensitivity"`
	ShortDescription  string               `json:"shortDescription"`
	FullDescription   string               `json:"fullDescription"`
	SiteRules         string               `json:"siteRules"`
	SafetyNotes       string               `json:"safetyNotes"`
	StandingPointNote string               `json:"standingPointNote"`
	ImageKey          string               `json:"imageKey"`
	ImageURL          string               `json:"imageUrl,omitempty"`
	RightsLine        string               `json:"rightsLine"`
	Latitude          float64              `json:"latitude"`
	Longitude         float64              `json:"longitude"`
	GeofenceRadiusM   int                  `json:"geofenceRadiusM"`
	Experience        *publishedExperience `json:"experience"`
}

const publishedSiteSelect = `SELECT s.id::text, s.name, s.slug, s.county, COALESCE(s.locality, ''),
	s.site_type, s.access_mode, s.sensitivity, COALESCE(s.short_description, ''),
	COALESCE(s.full_description, ''), COALESCE(s.site_rules, ''), COALESCE(s.safety_notes, ''),
	COALESCE(s.standing_point_note, ''), COALESCE(s.image_key, ''),
	COALESCE(s.public_rights_line, ''), COALESCE(s.latitude, 0)::float8,
	COALESCE(s.longitude, 0)::float8, s.geofence_radius_m
	FROM heritage_sites s
	WHERE s.status = 'published'
	  AND EXISTS (
		SELECT 1 FROM then_now_experiences e
		JOIN then_now_revisions r ON r.experience_id = e.id AND r.revision_number = e.live_revision_number
		WHERE e.site_id = s.id AND e.status = 'published' AND r.status = 'published'
	  )`

func scanPublishedSite(row scanner, site *publishedSite) error {
	return row.Scan(
		&site.ID, &site.Name, &site.Slug, &site.County, &site.Locality,
		&site.SiteType, &site.AccessMode, &site.Sensitivity,
		&site.ShortDescription, &site.FullDescription, &site.SiteRules,
		&site.SafetyNotes, &site.StandingPointNote, &site.ImageKey,
		&site.RightsLine, &site.Latitude, &site.Longitude, &site.GeofenceRadiusM,
	)
}

func (h *ThenNowHandler) loadPublishedExperience(ctx context.Context, siteID string) (*publishedExperience, error) {
	var id, title string
	var snapshot []byte
	err := h.pool.QueryRow(ctx, `
		SELECT e.id::text, e.title, r.snapshot
		FROM then_now_experiences e
		JOIN then_now_revisions r ON r.experience_id = e.id AND r.revision_number = e.live_revision_number
		WHERE e.site_id = $1 AND e.status = 'published' AND r.status = 'published'
		ORDER BY e.created_at ASC LIMIT 1`, siteID).Scan(&id, &title, &snapshot)
	if err != nil {
		return nil, err
	}
	var data thennow.Snapshot
	if err := json.Unmarshal(snapshot, &data); err != nil {
		return nil, err
	}
	data = thennow.NormalizeSnapshot(data)
	return &publishedExperience{
		ID:                  id,
		Title:               title,
		Promise:             data.Promise,
		Story:               data.Story,
		Period:              data.Period,
		Duration:            data.Duration,
		PrimaryMode:         data.PrimaryMode,
		Languages:           data.Languages,
		TextCards:           data.TextCards,
		VideoMaster:         data.VideoMaster,
		VideoSize:           data.VideoSize,
		Renditions:          data.Renditions,
		RenditionKeys:       data.RenditionKeys,
		Poster:              data.Poster,
		PosterKey:           data.PosterKey,
		Captions:            data.Captions,
		CaptionKeys:         data.CaptionKeys,
		Transcript:          data.Transcript,
		TranscriptKey:       data.TranscriptKey,
		AudioDescription:    data.AudioDescription,
		AudioDescriptionKey: data.AudioDescriptionKey,
		Media:               h.signedMedia(ctx, data),
	}, nil
}

// signURL returns a short-lived GET URL for a stored object, or "" when there
// is no object key or no media store is configured. Signing is a local HMAC,
// so this never adds a network round trip to the read path.
func (h *ThenNowHandler) signURL(ctx context.Context, key string) string {
	if h.media == nil || strings.TrimSpace(key) == "" {
		return ""
	}
	url, err := h.media.PresignedGetURL(ctx, key, h.mediaTTL)
	if err != nil {
		return ""
	}
	return url
}

func (h *ThenNowHandler) signedMedia(ctx context.Context, data thennow.Snapshot) *publishedMedia {
	if h.media == nil {
		return nil
	}
	media := &publishedMedia{ExpiresAt: time.Now().Add(h.mediaTTL)}

	if len(data.RenditionKeys) > 0 {
		video := make(map[string]string, len(data.RenditionKeys))
		for name, key := range data.RenditionKeys {
			if url := h.signURL(ctx, key); url != "" {
				video[name] = url
			}
		}
		if len(video) > 0 {
			media.Video = video
		}
	}

	if len(data.CaptionKeys) > 0 {
		captions := make(map[string]string, len(data.CaptionKeys))
		for lang, key := range data.CaptionKeys {
			if url := h.signURL(ctx, key); url != "" {
				captions[lang] = url
			}
		}
		if len(captions) > 0 {
			media.Captions = captions
		}
	}

	media.Master = h.signURL(ctx, data.VideoMaster)
	media.Poster = h.signURL(ctx, data.PosterKey)
	media.Transcript = h.signURL(ctx, data.TranscriptKey)
	media.AudioDescription = h.signURL(ctx, data.AudioDescriptionKey)

	if len(media.Video) == 0 && media.Master == "" && media.Poster == "" &&
		len(media.Captions) == 0 && media.Transcript == "" && media.AudioDescription == "" {
		return nil
	}
	return media
}

func (h *ThenNowHandler) ListPublishedSites(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(), publishedSiteSelect+` ORDER BY s.name ASC`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list published sites", err)
		return
	}
	defer rows.Close()
	items := make([]publishedSite, 0)
	for rows.Next() {
		var site publishedSite
		if err := scanPublishedSite(rows, &site); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read published site", err)
			return
		}
		experience, err := h.loadPublishedExperience(r.Context(), site.ID)
		if err != nil {
			continue
		}
		site.Experience = experience
		site.ImageURL = h.signURL(r.Context(), site.ImageKey)
		items = append(items, site)
	}
	writeThenNowJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *ThenNowHandler) GetPublishedSite(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var site publishedSite
	err := scanPublishedSite(h.pool.QueryRow(r.Context(), publishedSiteSelect+` AND s.slug = $1`, slug), &site)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "published heritage site not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load published site", err)
		return
	}
	experience, err := h.loadPublishedExperience(r.Context(), site.ID)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "published heritage site not found")
		return
	}
	site.Experience = experience
	site.ImageURL = h.signURL(r.Context(), site.ImageKey)
	writeThenNowJSON(w, http.StatusOK, map[string]any{"site": site})
}
