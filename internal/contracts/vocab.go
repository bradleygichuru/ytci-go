package contracts

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	"github.com/bradleygichuru/ytci-go/api"
)

// LoadSpec loads and validates the frozen OpenAPI document.
func LoadSpec() (*openapi3.T, error) {
	raw, err := fs.ReadFile(api.FS, api.OpenAPIPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", api.OpenAPIPath, err)
	}
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", api.OpenAPIPath, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("validate %s: %w", api.OpenAPIPath, err)
	}
	return doc, nil
}

// MiceContract is the parsed MICE vocabulary manifest.
type MiceContract struct {
	Version    string   `yaml:"version"`
	Owner      string   `yaml:"owner"`
	EventClass []string `yaml:"event_class"`
	Format     []string `yaml:"format"`
	Access     []string `yaml:"access"`
	Lifecycle  struct {
		Canonical         []string          `yaml:"canonical"`
		DeprecatedAliases map[string]string `yaml:"deprecated_aliases"`
		DerivedView       []string          `yaml:"derived_view"`
	} `yaml:"lifecycle"`
	Registration struct {
		Modes          []string `yaml:"modes"`
		Statuses       []string `yaml:"statuses"`
		CapacityCounts []string `yaml:"capacity_counts"`
		QuestionsMax   int      `yaml:"questions_max"`
	} `yaml:"registration"`
	Checkin struct {
		Methods []string `yaml:"methods"`
	} `yaml:"checkin"`
	Notifications struct {
		Kinds []string `yaml:"kinds"`
	} `yaml:"notifications"`
	Trust struct {
		Tiers                []string `yaml:"tiers"`
		VerificationStatuses []string `yaml:"verification_statuses"`
	} `yaml:"trust"`
	Reports struct {
		Reasons []string `yaml:"reasons"`
	} `yaml:"reports"`
	Accessibility struct {
		Values []string `yaml:"values"`
		Fields []string `yaml:"fields"`
	} `yaml:"accessibility"`
	MaterialFields []string `yaml:"material_fields"`
}

// SpeciesContract is the parsed species/language vocabulary manifest.
type SpeciesContract struct {
	Version           string   `yaml:"version"`
	CoverageTiers     []string `yaml:"coverage_tiers"`
	ReviewStatuses    []string `yaml:"review_statuses"`
	PublicationStates []string `yaml:"publication_states"`
	FieldStatuses     []string `yaml:"field_statuses"`
	ReviewStages      []string `yaml:"review_stages"`
	IUCNCategories    []string `yaml:"iucn_categories"`
	Languages         struct {
		UI                    []string `yaml:"ui"`
		FullContent           []string `yaml:"full_content"`
		NamesAndPronunciation []string `yaml:"names_and_pronunciation"`
	} `yaml:"languages"`
	WhereToSee struct {
		EvidenceTypes []string `yaml:"evidence_types"`
	} `yaml:"where_to_see"`
}

// AnalyticsContract is the parsed analytics vocabulary manifest.
type AnalyticsContract struct {
	Version       string   `yaml:"version"`
	EventFamilies []string `yaml:"event_families"`
	Journeys      []string `yaml:"journeys"`
	Consent       struct {
		States []string `yaml:"states"`
	} `yaml:"consent"`
	Retention struct {
		RawEventsMonths      int `yaml:"raw_events_months"`
		DailyAggregateMonths int `yaml:"daily_aggregates_months"`
	} `yaml:"retention"`
	Admin struct {
		SuppressionThreshold int `yaml:"suppression_threshold"`
	} `yaml:"admin"`
	Queue struct {
		MaxEvents  int `yaml:"max_events"`
		MaxBytes   int `yaml:"max_bytes"`
		MaxAgeDays int `yaml:"max_age_days"`
	} `yaml:"queue"`
}

// LoadMice parses the embedded MICE vocabulary.
func LoadMice() (*MiceContract, error) {
	var c MiceContract
	return &c, loadYAML(api.MicePath, &c)
}

// LoadSpecies parses the embedded species vocabulary.
func LoadSpecies() (*SpeciesContract, error) {
	var c SpeciesContract
	return &c, loadYAML(api.SpeciesPath, &c)
}

// LoadAnalytics parses the embedded analytics vocabulary.
func LoadAnalytics() (*AnalyticsContract, error) {
	var c AnalyticsContract
	return &c, loadYAML(api.AnalyticsPath, &c)
}

func loadYAML(path string, target any) error {
	raw, err := fs.ReadFile(api.FS, path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
