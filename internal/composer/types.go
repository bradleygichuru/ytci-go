package composer

import "time"

// EngineVersion is part of the deterministic seed. Bump it whenever scoring,
// sequencing, or the response shape changes.
const EngineVersion = "composer-engine-v1"

type Profile string

const (
	ProfileBudgetSmart     Profile = "budget-smart"
	ProfileBalanced        Profile = "balanced"
	ProfileExperienceFirst Profile = "experience-first"
)

var Profiles = []Profile{ProfileBudgetSmart, ProfileBalanced, ProfileExperienceFirst}

// Brief is the composer input. Origin may carry coordinates, a county code, or
// a place name; the engine resolves it polygon-first.
type Brief struct {
	Origin             Origin   `json:"origin"`
	Days               int      `json:"days"`
	Interests          []string `json:"interests"`
	BudgetBand         string   `json:"budgetBand"`
	BudgetPerPersonKes *int64   `json:"budgetPerPersonKes,omitempty"`
	GroupType          string   `json:"groupType"`
	GroupSize          int      `json:"groupSize"`
	Transport          string   `json:"transport"`
	Pace               string   `json:"pace"`
	StartDate          *string  `json:"startDate,omitempty"`
	MustInclude        []string `json:"mustInclude,omitempty"`
	ReturnToOrigin     *bool    `json:"returnToOrigin,omitempty"`
	AllowUnverified    bool     `json:"allowUnverified,omitempty"`
	Accessibility      []string `json:"accessibility,omitempty"`
	RegenerateCount    int      `json:"regenerateCount,omitempty"`
}

type Origin struct {
	Name       string   `json:"name"`
	Latitude   *float64 `json:"latitude,omitempty"`
	Longitude  *float64 `json:"longitude,omitempty"`
	CountyCode string   `json:"countyCode,omitempty"`
}

type SuggestionResponse struct {
	Suggestions []Suggestion     `json:"suggestions"`
	Coverage    []CountyCoverage `json:"coverage"`
	Meta        Meta             `json:"meta"`
	Blocked     *Blocked         `json:"blocked,omitempty"`
}

type Meta struct {
	EngineVersion     string    `json:"engineVersion"`
	CatalogVersion    string    `json:"catalogVersion"`
	InputHash         string    `json:"inputHash"`
	GeneratedAt       time.Time `json:"generatedAt"`
	Cached            bool      `json:"cached"`
	SuggestionLimit   int       `json:"suggestionLimit"`
	SuggestionsUsed   int       `json:"suggestionsUsed"`
	RetryAfterSeconds int       `json:"retryAfterSeconds,omitempty"`
}

type Blocked struct {
	Code     string           `json:"code"`
	Message  string           `json:"message"`
	Params   map[string]any   `json:"params,omitempty"`
	Coverage []CountyCoverage `json:"coverage,omitempty"`
}

type Suggestion struct {
	ID                 string           `json:"id"`
	Profile            Profile          `json:"profile"`
	Status             string           `json:"status"` // ok | limited | blocked
	Title              string           `json:"title"`
	Summary            string           `json:"summary"`
	Seed               string           `json:"seed"`
	MatchLabel         string           `json:"matchLabel"`
	Days               []Day            `json:"days"`
	EstimatedBudget    BudgetEstimate   `json:"estimatedBudget"`
	Warnings           []Warning        `json:"warnings"`
	Coverage           []CountyCoverage `json:"coverage"`
	TotalTravelMinutes int              `json:"totalTravelMinutes"`
	CuratedCount       int              `json:"curatedCount"`
	UnverifiedCount    int              `json:"unverifiedCount"`
	Score              float64          `json:"-"`
}

type Day struct {
	Day           int         `json:"day"`
	Date          string      `json:"date"`
	Base          string      `json:"base"`
	Stops         []Stop      `json:"stops"`
	TravelMinutes int         `json:"travelMinutes"`
	Warnings      []Warning   `json:"warnings,omitempty"`
	Weather       *DayWeather `json:"weather,omitempty"`
}

type Stop struct {
	DestinationID             *string   `json:"destinationId,omitempty"`
	PlaceID                   *string   `json:"placeId,omitempty"`
	Name                      string    `json:"name"`
	Provenance                string    `json:"provenance"`
	Category                  string    `json:"category"`
	Interests                 []string  `json:"interests"`
	CountyCode                string    `json:"countyCode"`
	County                    string    `json:"county"`
	Latitude                  float64   `json:"latitude"`
	Longitude                 float64   `json:"longitude"`
	Arrive                    string    `json:"arrive"`
	Depart                    string    `json:"depart"`
	DurationMinutes           int       `json:"durationMinutes"`
	TravelFromPreviousMinutes int       `json:"travelFromPreviousMinutes"`
	EstimatedTravel           bool      `json:"estimatedTravel"`
	FeeStatus                 *string   `json:"feeStatus,omitempty"`
	FeeAmountKes              *int64    `json:"feeAmountKes,omitempty"`
	OpeningHoursStatus        *string   `json:"openingHoursStatus,omitempty"`
	Rationale                 []Reason  `json:"rationale"`
	Warnings                  []Warning `json:"warnings,omitempty"`
}

type Warning struct {
	Code     string         `json:"code"`
	Severity string         `json:"severity"` // info | caution | warning
	Message  string         `json:"message"`
	Params   map[string]any `json:"params,omitempty"`
}

type Reason struct {
	Code   string         `json:"code"`
	Text   string         `json:"text"`
	Params map[string]any `json:"params,omitempty"`
}

type BudgetEstimate struct {
	Currency     string           `json:"currency"`
	Estimated    bool             `json:"estimated"`
	Note         string           `json:"note"`
	TotalKes     int64            `json:"totalKes"`
	PerPersonKes int64            `json:"perPersonKes"`
	Breakdown    map[string]int64 `json:"breakdown"`
}

type CountyCoverage struct {
	CountyCode      string `json:"countyCode"`
	County          string `json:"county"`
	CuratedCount    int    `json:"curatedCount"`
	UnverifiedCount int    `json:"unverifiedCount"`
	TotalCount      int    `json:"totalCount"`
	Status          string `json:"coverageStatus"`
}

type DayWeather struct {
	Date              string   `json:"date"`
	Horizon           string   `json:"horizon"` // forecast | outlook
	PrecipProbability *int     `json:"precipProbability,omitempty"`
	TempMaxC          *float64 `json:"tempMaxC,omitempty"`
	WeatherCode       *int     `json:"weatherCode,omitempty"`
	Source            string   `json:"source"`
}

// ValidationError carries field-level messages for a 400 response.
type ValidationError struct {
	Fields []FieldError `json:"fields"`
}

type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "invalid brief"
	}
	return "invalid brief: " + e.Fields[0].Field + ": " + e.Fields[0].Message
}

// RateLimitError is returned when the per-user daily suggestion limit is hit.
type RateLimitError struct {
	Limit             int
	Used              int
	RetryAfterSeconds int
}

func (e *RateLimitError) Error() string {
	return "composer: daily suggestion limit reached"
}
