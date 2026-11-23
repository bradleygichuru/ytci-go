package competition

import (
	"encoding/json"
	"time"
)

// Domain constants. Ages, dates, rules version and vote mode are edition
// configuration. Only the rulebook's vote caps and the published rubric
// weights are constants here, because they are policy, not legal route.
const (
	PromotionPrizeCompetition = "prize_competition"
	PromotionAwardProgramme   = "award_programme"
	PromotionLicensedOperator = "licensed_operator"

	VoteModeWeighted         = "weighted"
	VoteModePeopleChoiceOnly = "people_choice_only"
	VoteModeNone             = "none"

	VoteCapPerCategory = 5
	VoteCapTotal       = 10

	ExpertWeight = 0.70
	PublicWeight = 0.30

	RubricRelevanceWeight   = 25
	RubricCraftWeight       = 30
	RubricOriginalityWeight = 25
	RubricImpactWeight      = 20
)

var editionStatuses = map[string]bool{
	"draft": true, "consultation": true, "open": true, "judging": true,
	"voting": true, "closed": true, "published": true,
}

var visibleStatuses = map[string]bool{
	"consultation": true, "open": true, "judging": true,
	"voting": true, "closed": true, "published": true,
}

var votingStatuses = map[string]bool{
	"open": true, "judging": true, "voting": true,
}

var promotionTypes = map[string]bool{
	PromotionPrizeCompetition: true,
	PromotionAwardProgramme:   true,
	PromotionLicensedOperator: true,
}

var voteModes = map[string]bool{
	VoteModeWeighted: true, VoteModePeopleChoiceOnly: true, VoteModeNone: true,
}

var eligibilityStatuses = map[string]bool{
	"pending": true, "eligible": true, "ineligible": true,
}

var judgeStatuses = map[string]bool{
	"invited": true, "active": true, "recused": true, "inactive": true,
}

var assignmentStatuses = map[string]bool{
	"assigned": true, "recused": true, "completed": true,
}

var awardTypes = map[string]bool{
	"category_winner": true, "overall": true, "people_choice": true,
}

var awardForms = map[string]bool{
	"grant": true, "commission": true, "certificate": true,
	"feature": true, "recognition": true,
}

// edition is the shared admin/public representation. It deliberately carries
// no launch-gate prose: legal sign-off and public copy live outside the API.
type edition struct {
	ID             string     `json:"id"`
	Slug           string     `json:"slug"`
	Name           string     `json:"name"`
	Theme          *string    `json:"theme"`
	Status         string     `json:"status"`
	PromotionType  string     `json:"promotionType"`
	MinAge         *int       `json:"minAge"`
	MaxAge         *int       `json:"maxAge"`
	PublicVoteMode string     `json:"publicVoteMode"`
	PrizeEnabled   bool       `json:"prizeEnabled"`
	RulesVersion   string     `json:"rulesVersion"`
	OpensAt        *time.Time `json:"opensAt"`
	ClosesAt       *time.Time `json:"closesAt"`
	VotingOpensAt  *time.Time `json:"votingOpensAt"`
	VotingClosesAt *time.Time `json:"votingClosesAt"`
	AnnouncementAt *time.Time `json:"announcementAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type category struct {
	ID          string  `json:"id"`
	EditionID   string  `json:"editionId"`
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Description *string `json:"description"`
	SortOrder   int     `json:"sortOrder"`
}

type entryAdmin struct {
	ID                string          `json:"id"`
	EditionID         string          `json:"editionId"`
	CategoryID        string          `json:"categoryId"`
	CategoryKey       string          `json:"categoryKey"`
	CategoryLabel     string          `json:"categoryLabel"`
	RepositoryEntryID string          `json:"repositoryEntryId"`
	RepositorySlug    string          `json:"repositorySlug"`
	Title             string          `json:"title"`
	LeadUserID        *string         `json:"leadUserId"`
	TeamName          *string         `json:"teamName"`
	TeamMembers       json.RawMessage `json:"teamMembers,omitempty"`
	EligibilityStatus string          `json:"eligibilityStatus"`
	EligibilityNote   *string         `json:"eligibilityNote"`
	ScoreCount        int             `json:"scoreCount"`
	VoteCount         int             `json:"voteCount"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

// entryPublic is the blind, public-safe view. It contains no contact details,
// no age band, no team roster and no eligibility note.
type entryPublic struct {
	ID                string  `json:"id"`
	CategoryKey       string  `json:"categoryKey"`
	CategoryLabel     string  `json:"categoryLabel"`
	RepositoryEntryID string  `json:"repositoryEntryId"`
	RepositorySlug    string  `json:"repositorySlug"`
	Title             string  `json:"title"`
	Summary           string  `json:"summary"`
	County            *string `json:"county"`
	CreditName        *string `json:"creditName"`
	TeamName          *string `json:"teamName"`
	Votes             int     `json:"votes,omitempty"`
	Rank              int     `json:"rank,omitempty"`
}

type judge struct {
	ID          string    `json:"id"`
	EditionID   string    `json:"editionId"`
	UserID      *string   `json:"userId"`
	DisplayName string    `json:"displayName"`
	Email       *string   `json:"email"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
}

type assignment struct {
	ID         string    `json:"id"`
	EditionID  string    `json:"editionId"`
	CategoryID string    `json:"categoryId"`
	JudgeID    string    `json:"judgeId"`
	Status     string    `json:"status"`
	AssignedAt time.Time `json:"assignedAt"`
}

type conflict struct {
	ID         string    `json:"id"`
	JudgeID    string    `json:"judgeId"`
	EntryID    string    `json:"entryId"`
	Reason     string    `json:"reason"`
	DeclaredAt time.Time `json:"declaredAt"`
}

type score struct {
	ID           string     `json:"id"`
	AssignmentID string     `json:"assignmentId"`
	EntryID      string     `json:"entryId"`
	JudgeID      string     `json:"judgeId"`
	Relevance    int        `json:"relevance"`
	Craft        int        `json:"craft"`
	Originality  int        `json:"originality"`
	Impact       int        `json:"impact"`
	Total        float64    `json:"total"`
	Notes        *string    `json:"notes"`
	LockedAt     *time.Time `json:"lockedAt"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type vote struct {
	ID            string     `json:"id"`
	EditionID     string     `json:"editionId"`
	CategoryID    string     `json:"categoryId"`
	EntryID       string     `json:"entryId"`
	CreatedAt     time.Time  `json:"createdAt"`
	InvalidatedAt *time.Time `json:"invalidatedAt"`
	ReasonCode    *string    `json:"reasonCode"`
}

type myVote struct {
	vote
	Title       string `json:"title"`
	CategoryKey string `json:"categoryKey"`
}

type reviewCase struct {
	ID         string     `json:"id"`
	EditionID  string     `json:"editionId"`
	EntryID    *string    `json:"entryId"`
	Signal     string     `json:"signal"`
	Status     string     `json:"status"`
	OpenedAt   time.Time  `json:"openedAt"`
	ResolvedAt *time.Time `json:"resolvedAt"`
	Resolution *string    `json:"resolution"`
	ReviewerID *string    `json:"reviewerId"`
}

type snapshotSummary struct {
	ID         string    `json:"id"`
	EditionID  string    `json:"editionId"`
	CategoryID *string   `json:"categoryId"`
	LedgerHash string    `json:"ledgerHash"`
	CreatedAt  time.Time `json:"createdAt"`
	IsFinal    bool      `json:"isFinal"`
	Payload    any       `json:"payload"`
}

type award struct {
	ID          string     `json:"id"`
	EditionID   string     `json:"editionId"`
	CategoryID  *string    `json:"categoryId"`
	EntryID     string     `json:"entryId"`
	AwardType   string     `json:"awardType"`
	AwardForm   *string    `json:"awardForm"`
	Citation    *string    `json:"citation"`
	PublishedAt *time.Time `json:"publishedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	// Public winner view.
	Title       string  `json:"title,omitempty"`
	AuthorName  string  `json:"authorName,omitempty"`
	CategoryKey *string `json:"categoryKey,omitempty"`
}

type consultationResponse struct {
	ID        string    `json:"id"`
	EditionID *string   `json:"editionId"`
	Name      *string   `json:"name"`
	Email     *string   `json:"email"`
	Answers   any       `json:"answers"`
	Consent   bool      `json:"consent"`
	CreatedAt time.Time `json:"createdAt"`
}

// leaderboardEntry is one row of a public or app leaderboard. Expert scores are
// never included: they stay hidden until results publication.
type leaderboardEntry struct {
	Rank        int     `json:"rank"`
	EntryID     string  `json:"entryId"`
	Title       string  `json:"title"`
	Votes       int     `json:"votes"`
	PublicScore float64 `json:"publicScore,omitempty"`
}

type leaderboardCategory struct {
	Category category           `json:"category"`
	Entries  []leaderboardEntry `json:"entries"`
}

type leaderboardResponse struct {
	EditionID   string                `json:"editionId"`
	Mode        string                `json:"publicVoteMode"`
	GeneratedAt time.Time             `json:"generatedAt"`
	Categories  []leaderboardCategory `json:"categories"`
}

// resultEntry is the admin/public result row. Individual judge scores are
// omitted; only the averaged expert score, public vote and final score show.
type resultEntry struct {
	Rank        int     `json:"rank"`
	EntryID     string  `json:"entryId"`
	Title       string  `json:"title"`
	ExpertScore float64 `json:"expertScore"`
	ScoreCount  int     `json:"scoreCount"`
	PublicVotes int     `json:"publicVotes"`
	PublicScore float64 `json:"publicScore"`
	FinalScore  float64 `json:"finalScore"`
	IsTie       bool    `json:"isTie"`
}

type resultCategory struct {
	Category category      `json:"category"`
	Entries  []resultEntry `json:"entries"`
}

type peopleChoiceResult struct {
	EntryID string `json:"entryId"`
	Title   string `json:"title"`
	Votes   int    `json:"votes"`
}

type resultsResponse struct {
	EditionID      string              `json:"editionId"`
	Status         string              `json:"status"`
	PublicVoteMode string              `json:"publicVoteMode"`
	Frozen         bool                `json:"frozen"`
	LedgerHash     *string             `json:"ledgerHash"`
	Weights        weights             `json:"weights"`
	Categories     []resultCategory    `json:"categories"`
	PeopleChoice   *peopleChoiceResult `json:"peopleChoice"`
	Awards         []award             `json:"awards"`
}

type weights struct {
	Expert float64 `json:"expert"`
	Public float64 `json:"public"`
}

type publicSettings struct {
	PromotionTypes  []string   `json:"promotionTypes"`
	PublicVoteModes []string   `json:"publicVoteModes"`
	VoteCaps        voteCaps   `json:"voteCaps"`
	Weights         weights    `json:"weights"`
	Rubric          rubric     `json:"rubric"`
	Requirements    voteChecks `json:"votingRequirements"`
}

type voteCaps struct {
	PerCategory int `json:"perCategory"`
	Total       int `json:"total"`
}

type rubric struct {
	Relevance   int `json:"relevance"`
	Craft       int `json:"craft"`
	Originality int `json:"originality"`
	Impact      int `json:"impact"`
	ScaleMax    int `json:"scaleMax"`
}

type voteChecks struct {
	VerifiedEmail           bool `json:"verifiedEmail"`
	AccountBeforeVotingOpen bool `json:"accountBeforeVotingOpens"`
}
