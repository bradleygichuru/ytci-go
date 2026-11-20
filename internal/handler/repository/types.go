package repository

import "time"

// Kinds are the four catalogue discriminators. The order is the public
// collection order used by facets and stats.
var Kinds = []string{"competition_entry", "innovation", "conservation_resource", "experience"}

var kindLabels = map[string]string{
	"competition_entry":     "Competitions",
	"innovation":            "Youth Innovation",
	"conservation_resource": "Conservation & Guides",
	"experience":            "Travel Experiences",
}

// Public submissions are limited to the two collection types an entrant can
// author. Guides and experiences are admin-authored (decision #2).
var publicKinds = map[string]bool{
	"competition_entry": true,
	"innovation":        true,
}

var ageBands = map[string]bool{
	"under_18":          true,
	"18_24":             true,
	"25_30":             true,
	"31_35":             true,
	"36_plus":           true,
	"prefer_not_to_say": true,
}

const (
	maxImageBytes    = 8 * 1024 * 1024
	maxDocumentBytes = 15 * 1024 * 1024
	maxGalleryImages = 5
	mediaURLTTL      = 15 * time.Minute
	intentTTL        = 30 * time.Minute
	publicListLimit  = 50
	publicDefault    = 20
)

var imageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

var documentTypes = map[string]bool{
	"application/pdf": true,
}

// ---------------------------------------------------------------------------
// Public DTOs
// ---------------------------------------------------------------------------

type publicImage struct {
	URL       string `json:"url"`
	Alt       string `json:"alt,omitempty"`
	Credit    string `json:"credit,omitempty"`
	ExpiresAt string `json:"expiresAt"`
}

type publicSummary struct {
	ID                  string       `json:"id"`
	Kind                string       `json:"kind"`
	Slug                string       `json:"slug"`
	Title               string       `json:"title"`
	Summary             string       `json:"summary"`
	County              *string      `json:"county"`
	Category            *string      `json:"category"`
	Tags                []string     `json:"tags"`
	AuthorName          string       `json:"authorName,omitempty"`
	AuthorOrg           *string      `json:"authorOrg"`
	CompetitionName     *string      `json:"competitionName"`
	CompetitionCategory *string      `json:"competitionCategory"`
	Featured            bool         `json:"featured"`
	CoverImage          *publicImage `json:"coverImage"`
	// Flat additive aliases for clients that prefer a single field (contract
	// §8: new response fields are additive).
	CoverURL      string  `json:"coverUrl"`
	CoverThumbURL string  `json:"coverThumbUrl"`
	PublishedAt   *string `json:"publishedAt"`
}

type galleryItem struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Alt       string `json:"alt,omitempty"`
	Caption   string `json:"caption,omitempty"`
	Credit    string `json:"credit,omitempty"`
	SortOrder int    `json:"sortOrder"`
	ExpiresAt string `json:"expiresAt"`
}

type publicDocument struct {
	URL       string `json:"url"`
	FileName  string `json:"fileName"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	ExpiresAt string `json:"expiresAt"`
}

type embedInfo struct {
	Provider string `json:"provider,omitempty"`
	URL      string `json:"url"`
}

type teamMember struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

type publicDetail struct {
	publicSummary
	BodyMD      string          `json:"bodyMd"`
	Gallery     []*galleryItem  `json:"gallery"`
	Document    *publicDocument `json:"document"`
	DocumentURL string          `json:"documentUrl,omitempty"`
	ExternalURL *string         `json:"externalUrl"`
	Embed       *embedInfo      `json:"embed"`
	TeamMembers []teamMember    `json:"teamMembers"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
}

// ---------------------------------------------------------------------------
// Admin DTO
// ---------------------------------------------------------------------------

type adminEntry struct {
	publicDetail
	Status          string  `json:"status"`
	Source          string  `json:"source"`
	ReferenceCode   *string `json:"referenceCode"`
	AuthorEmail     *string `json:"authorEmail"`
	AuthorPhone     *string `json:"authorPhone"`
	AuthorAgeBand   *string `json:"authorAgeBand"`
	ModerationNote  *string `json:"moderationNote"`
	ReviewedBy      *string `json:"reviewedBy"`
	ReviewedAt      *string `json:"reviewedAt"`
	ConsentAt       *string `json:"consentAt"`
	ConsentVersion  *string `json:"consentVersion"`
	CoverMediaID    *string `json:"coverMediaId"`
	DocumentMediaID *string `json:"documentMediaId"`
	// SlugCollision and other operational errors surface here when relevant.
}

// ---------------------------------------------------------------------------
// Query / response envelopes
// ---------------------------------------------------------------------------

type publicListResponse struct {
	Items      []publicSummary `json:"items"`
	NextCursor *string         `json:"nextCursor"`
	HasMore    bool            `json:"hasMore"`
}

type adminListResponse struct {
	Items      []adminEntry `json:"items"`
	NextCursor *string      `json:"nextCursor"`
	HasMore    bool         `json:"hasMore"`
}

type facetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type kindFacet struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type categoryFacet struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Count int    `json:"count"`
}

type facetsResponse struct {
	Kinds      []kindFacet     `json:"kinds"`
	Counties   []facetValue    `json:"counties"`
	Categories []categoryFacet `json:"categories"`
	Tags       []facetValue    `json:"tags"`
}

type statsResponse struct {
	PublishedEntries int            `json:"publishedEntries"`
	CountiesCovered  int            `json:"countiesCovered"`
	FeaturedEntries  int            `json:"featuredEntries"`
	ByKind           map[string]int `json:"byKind"`
	LastPublishedAt  *string        `json:"lastPublishedAt"`
	// Flat additive aliases (contract §8).
	Entries               int `json:"entries"`
	Counties              int `json:"counties"`
	Competitions          int `json:"competitions"`
	Innovations           int `json:"innovations"`
	ConservationResources int `json:"conservationResources"`
	Experiences           int `json:"experiences"`
}

type publicSettings struct {
	SubmissionsOpen bool   `json:"submissionsOpen"`
	SubmissionIntro string `json:"submissionIntro"`
	TermsVersion    string `json:"termsVersion"`
	UpdatedAt       string `json:"updatedAt"`
}

type adminSettings struct {
	ID              string `json:"id"`
	SubmissionsOpen bool   `json:"submissionsOpen"`
	SubmissionIntro string `json:"submissionIntro"`
	TermsVersion    string `json:"termsVersion"`
	UpdatedAt       string `json:"updatedAt"`
}

// ---------------------------------------------------------------------------
// Uploads
// ---------------------------------------------------------------------------

type presignRequest struct {
	Kind          string `json:"kind"`
	FileName      string `json:"fileName"`
	ContentType   string `json:"contentType"`
	FileSizeBytes int64  `json:"fileSizeBytes"`
}

type presignResponse struct {
	UploadURL    string `json:"uploadUrl"`
	ObjectKey    string `json:"objectKey"`
	ExpiresAt    string `json:"expiresAt"`
	MaxSizeBytes int64  `json:"maxSizeBytes"`
	// MediaID is returned by the admin presign only: admin uploads create a
	// media_assets row immediately so an entry can reference it.
	MediaID string `json:"mediaId,omitempty"`
}

// ---------------------------------------------------------------------------
// Submissions
// ---------------------------------------------------------------------------

type submissionRequest struct {
	Kind                string       `json:"kind"`
	Title               string       `json:"title"`
	Summary             string       `json:"summary"`
	BodyMD              string       `json:"bodyMd"`
	County              string       `json:"county"`
	Category            string       `json:"category"`
	Tags                []string     `json:"tags"`
	AuthorName          string       `json:"authorName"`
	AuthorEmail         string       `json:"authorEmail"`
	AuthorPhone         string       `json:"authorPhone"`
	AuthorAgeBand       string       `json:"authorAgeBand"`
	AuthorOrg           string       `json:"authorOrg"`
	TeamMembers         []teamMember `json:"teamMembers"`
	CompetitionName     string       `json:"competitionName"`
	CompetitionCategory string       `json:"competitionCategory"`
	CoverObjectKey      string       `json:"coverObjectKey"`
	GalleryObjectKeys   []string     `json:"galleryObjectKeys"`
	DocumentObjectKey   string       `json:"documentObjectKey"`
	ExternalURL         string       `json:"externalUrl"`
	EmbedURL            string       `json:"embedUrl"`
	Consent             bool         `json:"consent"`
	ConsentVersion      string       `json:"consentVersion"`
	Honeypot            string       `json:"honeypot"`
	TurnstileToken      *string      `json:"turnstileToken"`
	GuardianConsent     bool         `json:"guardianConsent"`
}

type submissionResponse struct {
	ReferenceCode string `json:"referenceCode"`
	Status        string `json:"status"`
	SubmittedAt   string `json:"submittedAt"`
}

// ---------------------------------------------------------------------------
// Admin writes
// ---------------------------------------------------------------------------

type adminCreateRequest struct {
	Kind                string       `json:"kind"`
	Slug                string       `json:"slug"`
	Title               string       `json:"title"`
	Summary             string       `json:"summary"`
	BodyMD              string       `json:"bodyMd"`
	County              string       `json:"county"`
	Category            string       `json:"category"`
	Tags                []string     `json:"tags"`
	AuthorName          string       `json:"authorName"`
	AuthorOrg           string       `json:"authorOrg"`
	TeamMembers         []teamMember `json:"teamMembers"`
	CompetitionName     string       `json:"competitionName"`
	CompetitionCategory string       `json:"competitionCategory"`
	CoverMediaID        *string      `json:"coverMediaId"`
	DocumentMediaID     *string      `json:"documentMediaId"`
	GalleryMediaIDs     []string     `json:"galleryMediaIds"`
	ExternalURL         string       `json:"externalUrl"`
	EmbedURL            string       `json:"embedUrl"`
	Status              string       `json:"status"`
	Featured            bool         `json:"featured"`
}

type adminUpdateRequest struct {
	Kind                *string       `json:"kind"`
	Slug                *string       `json:"slug"`
	Title               *string       `json:"title"`
	Summary             *string       `json:"summary"`
	BodyMD              *string       `json:"bodyMd"`
	County              *string       `json:"county"`
	Category            *string       `json:"category"`
	Tags                *[]string     `json:"tags"`
	AuthorName          *string       `json:"authorName"`
	AuthorOrg           *string       `json:"authorOrg"`
	TeamMembers         *[]teamMember `json:"teamMembers"`
	CompetitionName     *string       `json:"competitionName"`
	CompetitionCategory *string       `json:"competitionCategory"`
	CoverMediaID        *string       `json:"coverMediaId"`
	DocumentMediaID     *string       `json:"documentMediaId"`
	GalleryMediaIDs     *[]string     `json:"galleryMediaIds"`
	ExternalURL         *string       `json:"externalUrl"`
	EmbedURL            *string       `json:"embedUrl"`
	Status              *string       `json:"status"`
	Featured            *bool         `json:"featured"`
}

type moderationRequest struct {
	Note     string `json:"note"`
	Featured *bool  `json:"featured"`
}

type settingsPatch struct {
	SubmissionsOpen *bool   `json:"submissionsOpen"`
	SubmissionIntro *string `json:"submissionIntro"`
	TermsVersion    *string `json:"termsVersion"`
}
