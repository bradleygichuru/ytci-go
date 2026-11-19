// Package species implements the species and language vertical: the
// language-neutral catalogue, per-language prose, names, media and audio,
// sourced rights, where-to-see evidence and the review/publish gate.
//
// Vocabulary lives in api/contracts/species.v1.yaml and the HTTP shapes in
// api/openapi/contracts.v1.yaml; this package must match both.
package species

import "time"

// Summary is the list shape shared by the public index and the admin catalogue.
type Summary struct {
	Slug             string   `json:"slug"`
	ScientificName   string   `json:"scientificName"`
	NameEn           string   `json:"nameEn"`
	NameSw           *string  `json:"nameSw"`
	IUCNCategory     *string  `json:"iucnCategory"`
	TaxonomicStatus  string   `json:"taxonomicStatus"`
	PublicationState string   `json:"publicationState"`
	HeroMediaURL     *string  `json:"heroMediaUrl"`
	Languages        []string `json:"languages"`
	Saved            bool     `json:"saved"`
}

// IUCN is the link-out block. Narrative, threats and range maps are never
// stored or served (ADR 0013 §3).
type IUCN struct {
	Category       string `json:"category"`
	AssessmentID   string `json:"assessmentId"`
	RedListVersion string `json:"redListVersion"`
	AsOf           string `json:"asOf"`
	Label          string `json:"label"`
}

// Fallback explains why a field or document resolved to English.
type Fallback struct {
	Used   bool   `json:"used"`
	Reason string `json:"reason"`
	Chip   string `json:"chip"`
}

// Detail is the full species record.
type Detail struct {
	Summary
	ID                string       `json:"id"`
	Authorship        *string      `json:"authorship"`
	Rank              string       `json:"rank"`
	GBIFKey           *string      `json:"gbifKey"`
	IUCN              *IUCN        `json:"iucn"`
	RequestedLanguage string       `json:"requestedLanguage"`
	ResolvedLanguage  string       `json:"resolvedLanguage"`
	Fallback          *Fallback    `json:"fallback"`
	Content           *Variant     `json:"content"`
	Names             []Name       `json:"names"`
	Media             []Media      `json:"media"`
	Audio             []Audio      `json:"audio"`
	Sources           []Source     `json:"sources"`
	WhereToSee        []WhereToSee `json:"whereToSee"`
	Links             []Link       `json:"links"`
	Coverage          []Coverage   `json:"coverage"`
	ContentVersion    int          `json:"contentVersion"`
}

// Variant is one per-language prose document.
type Variant struct {
	Language      string            `json:"language"`
	CoverageTier  string            `json:"coverageTier"`
	Summary       *string           `json:"summary"`
	Description   *string           `json:"description"`
	Habitat       *string           `json:"habitat"`
	Range         *string           `json:"range"`
	WhereToSee    *string           `json:"whereToSee"`
	Conservation  *string           `json:"conservation"`
	FieldStatus   map[string]string `json:"fieldStatus"`
	ReviewStatus  string            `json:"reviewStatus"`
	SourceVersion int               `json:"sourceVersion"`
	StaleReason   *string           `json:"staleReason"`
	TranslatorRef *string           `json:"translatorRef"`
	ReviewerRef   *string           `json:"reviewerRef"`
	PublishedAt   *time.Time        `json:"publishedAt"`
}

// VariantWrite is the admin upsert body.
type VariantWrite struct {
	Language      string            `json:"language"`
	CoverageTier  string            `json:"coverageTier"`
	Summary       *string           `json:"summary"`
	Description   *string           `json:"description"`
	Habitat       *string           `json:"habitat"`
	Range         *string           `json:"range"`
	WhereToSee    *string           `json:"whereToSee"`
	Conservation  *string           `json:"conservation"`
	FieldStatus   map[string]string `json:"fieldStatus"`
	SourceVersion *int              `json:"sourceVersion"`
	TranslatorRef *string           `json:"translatorRef"`
}

// Name is a sourced language/region name.
type Name struct {
	Language     string  `json:"language"`
	Script       string  `json:"script"`
	Name         string  `json:"name"`
	NameType     string  `json:"nameType"`
	Region       *string `json:"region"`
	CountyGID    *int    `json:"countyGid"`
	ReviewStatus string  `json:"reviewStatus"`
	Preferred    bool    `json:"preferred"`
	Source       *Source `json:"source"`
}

// NameWrite is the admin upsert body.
type NameWrite struct {
	Language  string  `json:"language"`
	Script    string  `json:"script"`
	Name      string  `json:"name"`
	NameType  string  `json:"nameType"`
	Region    *string `json:"region"`
	CountyGID *int    `json:"countyGid"`
	SourceID  *string `json:"sourceId"`
	Preferred *bool   `json:"preferred"`
}

// Media is a managed media asset with per-language alt text.
type Media struct {
	ID                string            `json:"id"`
	MediaKind         string            `json:"mediaKind"`
	URL               string            `json:"url"`
	Width             *int              `json:"width"`
	Height            *int              `json:"height"`
	Credit            string            `json:"credit"`
	RightsHolder      string            `json:"rightsHolder"`
	LicenceCode       string            `json:"licenceCode"`
	LicenceURL        *string           `json:"licenceUrl"`
	SensitiveLocation bool              `json:"sensitiveLocation"`
	Alt               map[string]string `json:"alt"`
}

// MediaWrite is the admin create body.
type MediaWrite struct {
	MediaKind         string            `json:"mediaKind"`
	StorageKey        string            `json:"storageKey"`
	Width             *int              `json:"width"`
	Height            *int              `json:"height"`
	RightsID          *string           `json:"rightsId"`
	Credit            string            `json:"credit"`
	RightsHolder      string            `json:"rightsHolder"`
	LicenceCode       string            `json:"licenceCode"`
	LicenceURL        *string           `json:"licenceUrl"`
	SensitiveLocation bool              `json:"sensitiveLocation"`
	Alt               map[string]string `json:"alt"`
}

// Audio is a per-language narrated track.
type Audio struct {
	Language        string  `json:"language"`
	URL             string  `json:"url"`
	Narrator        string  `json:"narrator"`
	Transcript      string  `json:"transcript"`
	DurationSeconds int     `json:"durationSeconds"`
	ReviewStatus    string  `json:"reviewStatus"`
	Rights          *Rights `json:"rights"`
}

// AudioWrite is the admin create body.
type AudioWrite struct {
	Language        string  `json:"language"`
	StorageKey      string  `json:"storageKey"`
	Narrator        string  `json:"narrator"`
	Transcript      string  `json:"transcript"`
	DurationSeconds int     `json:"durationSeconds"`
	RightsID        *string `json:"rightsId"`
}

// Source is a citation with licence and permitted use.
type Source struct {
	ID           string  `json:"id"`
	Publisher    string  `json:"publisher"`
	Citation     string  `json:"citation"`
	URL          *string `json:"url"`
	AccessedAt   string  `json:"accessedAt"`
	LicenceCode  string  `json:"licenceCode"`
	LicenceURL   *string `json:"licenceUrl"`
	PermittedUse string  `json:"permittedUse"`
	Attribution  string  `json:"attribution"`
	SnapshotHash *string `json:"snapshotHash"`
	ReviewStatus string  `json:"reviewStatus"`
}

// SourceWrite is the admin create body.
type SourceWrite struct {
	SpeciesID    *string `json:"speciesId"`
	Publisher    string  `json:"publisher"`
	Citation     string  `json:"citation"`
	URL          *string `json:"url"`
	AccessedAt   string  `json:"accessedAt"`
	LicenceCode  string  `json:"licenceCode"`
	LicenceURL   *string `json:"licenceUrl"`
	PermittedUse string  `json:"permittedUse"`
	Attribution  string  `json:"attribution"`
	SnapshotHash *string `json:"snapshotHash"`
}

// Rights records licensed use of one media or audio asset.
type Rights struct {
	ID            string     `json:"id"`
	SubjectType   string     `json:"subjectType"`
	SubjectID     string     `json:"subjectId"`
	RightsHolder  string     `json:"rightsHolder"`
	LicenceCode   string     `json:"licenceCode"`
	LicenceURL    *string    `json:"licenceUrl"`
	PermittedUse  string     `json:"permittedUse"`
	Attribution   string     `json:"attribution"`
	ConsentRecord *string    `json:"consentRecord"`
	ExpiresAt     *time.Time `json:"expiresAt"`
	WithdrawnAt   *time.Time `json:"withdrawnAt"`
}

// RightsWrite is the admin create body.
type RightsWrite struct {
	SubjectType   string     `json:"subjectType"`
	SubjectID     string     `json:"subjectId"`
	RightsHolder  string     `json:"rightsHolder"`
	LicenceCode   string     `json:"licenceCode"`
	LicenceURL    *string    `json:"licenceUrl"`
	PermittedUse  string     `json:"permittedUse"`
	Attribution   string     `json:"attribution"`
	ConsentRecord *string    `json:"consentRecord"`
	ExpiresAt     *time.Time `json:"expiresAt"`
}

// WhereToSee is evidence, never a guarantee.
type WhereToSee struct {
	DestinationSlug *string `json:"destinationSlug"`
	CountyGID       *int    `json:"countyGid"`
	County          *string `json:"county"`
	ProtectedArea   *string `json:"protectedArea"`
	EvidenceType    string  `json:"evidenceType"`
	Confidence      string  `json:"confidence"`
	ReviewStatus    string  `json:"reviewStatus"`
	Copy            string  `json:"copy"`
	Source          *Source `json:"source"`
}

// Link joins a species to a destination, course, audio story or event.
type Link struct {
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
	Title      string `json:"title"`
	Href       string `json:"href"`
}

// Coverage is one language's honest availability.
type Coverage struct {
	Language      string `json:"language"`
	Name          string `json:"name"`
	Tier          string `json:"tier"`
	Available     bool   `json:"available"`
	ReviewedNames int    `json:"reviewedNames"`
	Audio         bool   `json:"audio"`
	Fallback      bool   `json:"fallback"`
}

// ReviewItem is one queue row.
type ReviewItem struct {
	ID            string     `json:"id"`
	EntityType    string     `json:"entityType"`
	EntityID      string     `json:"entityId"`
	SpeciesSlug   string     `json:"speciesSlug"`
	Language      *string    `json:"language"`
	Stage         string     `json:"stage"`
	Status        string     `json:"status"`
	AssignedTo    *string    `json:"assignedTo"`
	TranslatorRef *string    `json:"translatorRef"`
	ReviewerRef   *string    `json:"reviewerRef"`
	SubmittedAt   *time.Time `json:"submittedAt"`
}

// PackEntry is one species entry in an offline language pack.
type PackEntry struct {
	Slug           string `json:"slug"`
	ContentVersion int    `json:"contentVersion"`
	HasAudio       bool   `json:"hasAudio"`
}

// Pack is the explicit local-language download manifest.
type Pack struct {
	Language    string      `json:"language"`
	Version     string      `json:"version"`
	SizeBytes   int64       `json:"sizeBytes"`
	GeneratedAt time.Time   `json:"generatedAt"`
	Entries     []PackEntry `json:"entries"`
}

// SpeciesWrite is the admin create/update body.
type SpeciesWrite struct {
	ScientificName    string  `json:"scientificName"`
	Slug              *string `json:"slug"`
	Authorship        *string `json:"authorship"`
	TaxonomicRank     *string `json:"taxonomicRank"`
	TaxonomicStatus   *string `json:"taxonomicStatus"`
	AcceptedSpeciesID *string `json:"acceptedSpeciesId"`
	GBIFKey           *string `json:"gbifKey"`
	IUCNTaxonID       *string `json:"iucnTaxonId"`
	WikidataQID       *string `json:"wikidataQid"`
	IUCNCategory      *string `json:"iucnCategory"`
	IUCNAssessmentID  *string `json:"iucnAssessmentId"`
	RedListVersion    *string `json:"redListVersion"`
	StatusAsOf        *string `json:"statusAsOf"`
}

var iucnLabels = map[string]string{
	"CR": "Critically Endangered",
	"EN": "Endangered",
	"VU": "Vulnerable",
	"NT": "Near Threatened",
	"LC": "Least Concern",
	"DD": "Data Deficient",
	"NE": "Not Evaluated",
}
