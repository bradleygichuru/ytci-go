package thennow

import (
	"strings"
	"time"
)

type TextCard struct {
	At   int    `json:"at"`
	Text string `json:"text"`
}

type Snapshot struct {
	Title               string            `json:"title"`
	Promise             string            `json:"promise"`
	Story               string            `json:"story"`
	Period              string            `json:"period"`
	Duration            int               `json:"duration"`
	ContentNotes        string            `json:"contentNotes"`
	SafetyNotes         string            `json:"safetyNotes"`
	Sources             string            `json:"sources"`
	FactCheck           string            `json:"factCheck"`
	PrimaryMode         string            `json:"primaryMode"`
	Languages           []string          `json:"languages"`
	RightsRecord        string            `json:"rightsRecord"`
	RightsEvidence      string            `json:"rightsEvidence"`
	RightsExpiry        string            `json:"rightsExpiry"`
	ConsentRecord       string            `json:"consentRecord"`
	Sensitivity         string            `json:"sensitivity"`
	VideoMaster         string            `json:"videoMaster"`
	VideoSize           string            `json:"videoSize"`
	Renditions          map[string]string `json:"renditions"`
	RenditionKeys       map[string]string `json:"renditionKeys"`
	Poster              bool              `json:"poster"`
	PosterKey           string            `json:"posterKey"`
	Captions            map[string]bool   `json:"captions"`
	CaptionKeys         map[string]string `json:"captionKeys"`
	Transcript          bool              `json:"transcript"`
	TranscriptKey       string            `json:"transcriptKey"`
	AudioDescription    bool              `json:"audioDescription"`
	AudioDescriptionKey string            `json:"audioDescriptionKey"`
	TextCards           []TextCard        `json:"textCards"`
}

type ReviewNote struct {
	Author string `json:"author"`
	Role   string `json:"role"`
	At     string `json:"at"`
	Note   string `json:"note"`
}

type ClientApproval struct {
	Name string `json:"name"`
	Role string `json:"role"`
	At   string `json:"at"`
}

type ExternalReviewer struct {
	Name     string `json:"name"`
	Org      string `json:"org"`
	Decision string `json:"decision"`
}

type ReadinessItem struct {
	Section string `json:"section"`
	Label   string `json:"label"`
}

type Readiness struct {
	Blockers       []ReadinessItem `json:"blockers"`
	Warnings       []ReadinessItem `json:"warnings"`
	ReadyToSubmit  bool            `json:"readyToSubmit"`
	ReadyToPublish bool            `json:"readyToPublish"`
}

type Site struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Slug                 string  `json:"slug"`
	County               string  `json:"county"`
	Locality             string  `json:"locality"`
	SiteType             string  `json:"siteType"`
	AccessMode           string  `json:"accessMode"`
	Sensitivity          string  `json:"sensitivity"`
	Status               string  `json:"status"`
	ManagingAuthority    string  `json:"managingAuthority"`
	DestinationID        *string `json:"destinationId"`
	PhotographyPolicy    string  `json:"photographyPolicy"`
	CulturalAuthority    string  `json:"culturalAuthority"`
	ConsentRecord        string  `json:"consentRecord"`
	ConsentDate          string  `json:"consentDate"`
	SiteRules            string  `json:"siteRules"`
	SafetyNotes          string  `json:"safetyNotes"`
	ShortDescription     string  `json:"shortDescription"`
	FullDescription      string  `json:"fullDescription"`
	Sources              string  `json:"sources"`
	FactCheck            string  `json:"factCheck"`
	PublicRightsLine     string  `json:"publicRightsLine"`
	Latitude             float64 `json:"latitude"`
	Longitude            float64 `json:"longitude"`
	GeofenceRadiusM      int     `json:"geofenceRadiusM"`
	StandingPointNote    string  `json:"standingPointNote"`
	ImageKey             string  `json:"imageKey"`
	PublishedExperiences int     `json:"publishedExperiences"`
}

func normalizedSensitivity(value string) string {
	if strings.TrimSpace(value) == "" {
		return "standard"
	}
	return value
}

func hasText(value string) bool {
	return strings.TrimSpace(value) != ""
}

func ExperienceReadiness(snapshot Snapshot, revisionStatus string, liveRevision, openRevision int) Readiness {
	blockers := make([]ReadinessItem, 0)
	warnings := make([]ReadinessItem, 0)
	add := func(section, label string) {
		blockers = append(blockers, ReadinessItem{Section: section, Label: label})
	}
	warn := func(section, label string) {
		warnings = append(warnings, ReadinessItem{Section: section, Label: label})
	}

	if !hasText(snapshot.Title) {
		add("story", "Title")
	}
	if !hasText(snapshot.Promise) {
		add("story", "Promise")
	}
	if !hasText(snapshot.Story) {
		add("story", "Story")
	}
	if !hasText(snapshot.VideoMaster) {
		add("media", "Site video master")
	}
	if snapshot.Renditions == nil || snapshot.Renditions["1080"] != "Ready" || snapshot.RenditionKeys["1080"] == "" {
		add("media", "1080p video file")
	}
	if !snapshot.Poster || snapshot.PosterKey == "" {
		add("media", "Poster image")
	}
	if snapshot.Captions == nil || !snapshot.Captions["en"] || snapshot.CaptionKeys["en"] == "" {
		add("accessibility", "English captions")
	}
	if !snapshot.Transcript || snapshot.TranscriptKey == "" {
		add("accessibility", "Transcript")
	}
	if !hasText(snapshot.RightsRecord) {
		add("rights", "Rights record")
	}
	if !hasText(snapshot.ConsentRecord) {
		add("rights", "Cultural authority / consent")
	}
	if strings.TrimSpace(snapshot.RightsExpiry) != "" {
		if expiry, err := time.Parse("2006-01-02", snapshot.RightsExpiry); err == nil && expiry.Before(time.Now().UTC()) {
			add("rights", "Rights have expired")
		}
	}
	if len(snapshot.TextCards) < 2 {
		add("cards", "At least two text cards")
	}

	reviewBlockers := make([]ReadinessItem, 0)
	if revisionStatus == "in_review" {
		reviewBlockers = append(reviewBlockers, ReadinessItem{Section: "review", Label: "Review decision"})
	} else if liveRevision > 0 && openRevision > liveRevision && revisionStatus != "approved" && revisionStatus != "published" {
		reviewBlockers = append(reviewBlockers, ReadinessItem{Section: "review", Label: "Review approval for the open revision"})
	}

	for _, language := range snapshot.Languages {
		if strings.EqualFold(language, "SW") && (snapshot.Captions == nil || !snapshot.Captions["sw"]) {
			warn("accessibility", "Swahili captions")
		}
	}
	if !snapshot.AudioDescription {
		warn("accessibility", "Audio description")
	}
	if snapshot.Renditions == nil || snapshot.Renditions["540"] != "Ready" {
		warn("media", "540p video file")
	}

	allBlockers := append(blockers, reviewBlockers...)
	return Readiness{
		Blockers:       allBlockers,
		Warnings:       warnings,
		ReadyToSubmit:  len(blockers) == 0 && revisionStatus == "draft",
		ReadyToPublish: len(allBlockers) == 0 && revisionStatus == "approved",
	}
}

func SiteReadiness(site Site) Readiness {
	blockers := make([]ReadinessItem, 0)
	warnings := make([]ReadinessItem, 0)
	add := func(section, label string) {
		blockers = append(blockers, ReadinessItem{Section: section, Label: label})
	}
	if !hasText(site.Name) {
		add("identity", "Name")
	}
	if !hasText(site.County) {
		add("identity", "County")
	}
	if !hasText(site.SiteType) {
		add("identity", "Site type")
	}
	if !hasText(site.AccessMode) {
		add("policy", "Access mode")
	}
	if !hasText(site.Sensitivity) {
		add("policy", "Sensitivity")
	}
	if !hasText(site.PhotographyPolicy) {
		add("policy", "Photography policy")
	}
	if !hasText(site.CulturalAuthority) {
		add("policy", "Cultural authority")
	}
	if !hasText(site.ConsentRecord) {
		add("policy", "Consent record")
	}
	if site.PublishedExperiences == 0 {
		add("experiences", "At least one published experience")
	}
	if site.AccessMode == "not_available" {
		warnings = append(warnings, ReadinessItem{Section: "policy", Label: "Access mode is not available yet"})
	}
	return Readiness{
		Blockers:       blockers,
		Warnings:       warnings,
		ReadyToSubmit:  false,
		ReadyToPublish: len(blockers) == 0,
	}
}

func NormalizeSite(site Site) Site {
	if site.Sensitivity == "" {
		site.Sensitivity = "standard"
	}
	if site.GeofenceRadiusM == 0 {
		site.GeofenceRadiusM = 150
	}
	return site
}

func NormalizeSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Sensitivity = normalizedSensitivity(snapshot.Sensitivity)
	if snapshot.Languages == nil {
		snapshot.Languages = []string{"EN"}
	}
	if snapshot.Renditions == nil {
		snapshot.Renditions = map[string]string{}
	}
	if snapshot.Captions == nil {
		snapshot.Captions = map[string]bool{}
	}
	if snapshot.RenditionKeys == nil {
		snapshot.RenditionKeys = map[string]string{}
	}
	if snapshot.CaptionKeys == nil {
		snapshot.CaptionKeys = map[string]string{}
	}
	if snapshot.TextCards == nil {
		snapshot.TextCards = []TextCard{}
	}
	return snapshot
}
