// Package recognition implements the recognition vertical: the append-only XP
// ledger, versioned rules, badges, missions, seasons, the opt-in heritage
// leaderboard and the same-transaction outbox that drives them.
//
// Event emission follows docs/execution/cloudevents.md. The outbox worker
// evaluates published rules and writes effects idempotently, so at-least-once
// delivery still produces exactly-once effects.
package recognition

import (
	"time"
)

// Payload is the allow-listed CloudEvent data object.
type Payload struct {
	SourceType         string     `json:"source_type"`
	SourceID           string     `json:"source_id"`
	OccurrenceKey      string     `json:"occurrence_key"`
	VerificationMethod string     `json:"verification_method"`
	VerificationResult string     `json:"verification_result"`
	Sensitivity        string     `json:"sensitivity,omitempty"`
	PrivacyClass       string     `json:"privacy_class,omitempty"`
	AgeBand            string     `json:"age_band,omitempty"`
	ProgramScope       string     `json:"program_scope,omitempty"`
	ConsentVersion     string     `json:"consent_version,omitempty"`
	OccurredAt         *time.Time `json:"occurred_at,omitempty"`
	SourceRevision     *int       `json:"source_revision,omitempty"`
}

// Event is the CloudEvents-shaped envelope. ID is a UUIDv7 supplied by the
// emitter and doubles as the transport idempotency key; when empty the engine
// generates one.
type Event struct {
	SpecVersion string    `json:"specversion"`
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Type        string    `json:"type"`
	Subject     string    `json:"subject"`
	Time        time.Time `json:"time"`
	Data        Payload   `json:"data"`

	// RuleKeyHint is internal to the engine. Source adapters use it when one
	// event type has more than one published rule (challenge difficulty or an
	// optional heritage reward). It is never part of the CloudEvent payload.
	RuleKeyHint string `json:"-"`
}

// RecordResult reports what Record persisted.
type RecordResult struct {
	EventID   string
	OutboxID  string
	Duplicate bool
}

// ProcessStats summarises one outbox drain.
type ProcessStats struct {
	Claimed int
	Done    int
	Failed  int
	Awards  int
}

// Drift is one reconciliation mismatch. Reconciliation never auto-corrects.
type Drift struct {
	SubjectID string
	Projected int64
	Ledger    int64
}

// Level is the public level shape.
type Level struct {
	Value       int     `json:"value"`
	Name        string  `json:"name"`
	Threshold   int     `json:"threshold"`
	NextName    *string `json:"nextName"`
	XPIntoLevel int     `json:"xpIntoLevel"`
	XPToNext    *int    `json:"xpToNext"`
}

// XpEntry is one ledger row as rendered to a reader or administrator.
type XpEntry struct {
	ID         string    `json:"id"`
	Amount     int       `json:"amount"`
	Reason     string    `json:"reason"`
	SourceType string    `json:"sourceType,omitempty"`
	SourceID   string    `json:"sourceId,omitempty"`
	Note       *string   `json:"note"`
	CreatedAt  time.Time `json:"createdAt"`
}

// BadgeAward is a catalogue entry with the reader's earned state.
type BadgeAward struct {
	BadgeKey     string         `json:"badgeKey"`
	NameEn       string         `json:"nameEn"`
	NameSw       *string        `json:"nameSw"`
	Family       string         `json:"family"`
	Criteria     map[string]any `json:"criteria"`
	Earned       bool           `json:"earned"`
	EarnedAt     *time.Time     `json:"earnedAt"`
	Hidden       bool           `json:"hidden"`
	PrivacyClass string         `json:"privacyClass"`
	SourceType   *string        `json:"sourceType"`
	SourceID     *string        `json:"sourceId"`
	Progress     *BadgeProgress `json:"progress"`
}

// BadgeProgress is an optional progress hint for a locked badge.
type BadgeProgress struct {
	Current int `json:"current"`
	Target  int `json:"target"`
}

// Mission is deterministic progress for one curated track.
type Mission struct {
	TrackKey    string     `json:"trackKey"`
	NameEn      string     `json:"nameEn"`
	NameSw      *string    `json:"nameSw"`
	Family      string     `json:"family"`
	Current     int        `json:"current"`
	Target      int        `json:"target"`
	Completed   bool       `json:"completed"`
	CompletedAt *time.Time `json:"completedAt"`
	XPReward    int        `json:"xpReward"`
	BadgeKey    *string    `json:"badgeKey"`
}

// Season is the season catalogue shape.
type Season struct {
	Key             string    `json:"key"`
	NameEn          string    `json:"nameEn"`
	NameSw          *string   `json:"nameSw"`
	StartsAt        time.Time `json:"startsAt"`
	EndsAt          time.Time `json:"endsAt"`
	Status          string    `json:"status"`
	BadgeKey        *string   `json:"badgeKey"`
	XPAward         int       `json:"xpAward"`
	MissionFamilies []string  `json:"missionFamilies"`
}

// SeasonProgress adds the reader's private progress to a season.
type SeasonProgress struct {
	Season
	Progress SeasonProgressCounts `json:"progress"`
	OptIn    bool                 `json:"optIn"`
}

// SeasonProgressCounts is the private season summary.
type SeasonProgressCounts struct {
	XP                int  `json:"xp"`
	MissionsCompleted int  `json:"missionsCompleted"`
	MissionsTotal     int  `json:"missionsTotal"`
	BadgeEarned       bool `json:"badgeEarned"`
}

// RecognitionOverview is the mobile home payload.
type RecognitionOverview struct {
	Level             Level               `json:"level"`
	TotalXP           int64               `json:"totalXp"`
	RecentAwards      []BadgeAward        `json:"recentAwards"`
	BadgeCounts       BadgeCounts         `json:"badgeCounts"`
	Season            *OverviewSeason     `json:"season"`
	Passport          OverviewPassport    `json:"passport"`
	Leaderboard       OverviewLeaderboard `json:"leaderboard"`
	AdjustmentNotices []XpEntry           `json:"adjustmentNotices"`
}

// BadgeCounts is the overview badge tally.
type BadgeCounts struct {
	Earned int `json:"earned"`
	Total  int `json:"total"`
	Public int `json:"public"`
}

// OverviewSeason is the active season summary.
type OverviewSeason struct {
	Key             string    `json:"key"`
	NameEn          string    `json:"nameEn"`
	EndsAt          time.Time `json:"endsAt"`
	ProgressPercent int       `json:"progressPercent"`
}

// OverviewPassport is the compact passport summary.
type OverviewPassport struct {
	Counties int `json:"counties"`
	Sites    int `json:"sites"`
}

// OverviewLeaderboard is the reader's opt-in state.
type OverviewLeaderboard struct {
	OptedIn  bool    `json:"optedIn"`
	Eligible bool    `json:"eligible"`
	Reason   *string `json:"reason"`
}

// Passport is the private county and site collection.
type Passport struct {
	Counties []PassportCounty `json:"counties"`
	Sites    []PassportSite   `json:"sites"`
}

// PassportCounty is one county collection row.
type PassportCounty struct {
	CountyGID int    `json:"countyGid"`
	Name      string `json:"name"`
	Sites     int    `json:"sites"`
	Complete  bool   `json:"complete"`
}

// PassportSite is one site row.
type PassportSite struct {
	SiteRef   string     `json:"siteRef"`
	Name      string     `json:"name"`
	CountyGID *int       `json:"countyGid"`
	VisitedAt *time.Time `json:"visitedAt"`
	Sensitive bool       `json:"sensitive"`
}

// LeaderboardEntry is one public board row. It never carries an XP total or a
// user id.
type LeaderboardEntry struct {
	Rank             int     `json:"rank"`
	DisplayName      string  `json:"displayName"`
	County           *string `json:"county"`
	Level            int     `json:"level"`
	PublicBadgeCount int     `json:"publicBadgeCount"`
	VerifiedSites    int     `json:"verifiedSites"`
	TrailCompletions int     `json:"trailCompletions"`
	IsSelf           bool    `json:"isSelf"`
}

// Leaderboard is the public board or a suppression state.
type Leaderboard struct {
	SeasonKey         string             `json:"seasonKey"`
	CountyGID         *int               `json:"countyGid"`
	Suppressed        bool               `json:"suppressed"`
	SuppressionReason *string            `json:"suppressionReason"`
	Entries           []LeaderboardEntry `json:"entries"`
	UpdatedAt         *time.Time         `json:"updatedAt"`
	RankingNote       string             `json:"rankingNote"`
}

// LeaderboardState is the reader's private opt-in view.
type LeaderboardState struct {
	SeasonKey        string            `json:"seasonKey"`
	OptedIn          bool              `json:"optedIn"`
	Eligible         bool              `json:"eligible"`
	IneligibleReason *string           `json:"ineligibleReason"`
	DisplayName      *DisplayName      `json:"displayName"`
	OwnEntry         *LeaderboardEntry `json:"ownEntry"`
	Board            *Leaderboard      `json:"board"`
}

// DisplayName is a moderated leaderboard alias.
type DisplayName struct {
	DisplayName       string     `json:"displayName"`
	Status            string     `json:"status"`
	PreviousApproved  *string    `json:"previousApproved"`
	NextChangeAllowed *time.Time `json:"nextChangeAllowed"`
}

// RecognitionRule is one immutable version as rendered to admins.
type RecognitionRule struct {
	ID            string         `json:"id"`
	RuleKey       string         `json:"ruleKey"`
	Version       int            `json:"version"`
	SourceType    string         `json:"sourceType"`
	Status        string         `json:"status"`
	EffectiveFrom time.Time      `json:"effectiveFrom"`
	EffectiveTo   *time.Time     `json:"effectiveTo"`
	Criteria      map[string]any `json:"criteria"`
	XPAmount      int            `json:"xpAmount"`
	BadgeKey      *string        `json:"badgeKey"`
	CreatedBy     *string        `json:"createdBy"`
	PublishedBy   *string        `json:"publishedBy"`
}

// BadgeDefinition is the admin catalogue shape.
type BadgeDefinition struct {
	BadgeKey       string         `json:"badgeKey"`
	NameEn         string         `json:"nameEn"`
	NameSw         *string        `json:"nameSw"`
	Family         string         `json:"family"`
	Criteria       map[string]any `json:"criteria"`
	IconKey        *string        `json:"iconKey"`
	ColourToken    *string        `json:"colourToken"`
	PrivacyClass   string         `json:"privacyClass"`
	Hidden         bool           `json:"hidden"`
	PublicEligible bool           `json:"publicEligible"`
	Status         string         `json:"status"`
	Version        int            `json:"version"`
}

// RuleSimulation is a dry-run summary.
type RuleSimulation struct {
	RuleKey        string   `json:"ruleKey"`
	Version        int      `json:"version"`
	Evaluated      int      `json:"evaluated"`
	WouldAward     int      `json:"wouldAward"`
	WouldDeduct    int      `json:"wouldDeduct"`
	SampleSubjects []string `json:"sampleSubjects"`
	Notes          *string  `json:"notes"`
}

// ModerationCase is one queue item.
type ModerationCase struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	SubjectRef     *string    `json:"subjectRef"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status"`
	Decision       *string    `json:"decision"`
	DecidedAt      *time.Time `json:"decidedAt"`
	AppealDeadline *time.Time `json:"appealDeadline"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// BackfillBatch is a dry-run or approved replay.
type BackfillBatch struct {
	ID          string        `json:"id"`
	RuleKey     string        `json:"ruleKey"`
	RuleVersion int           `json:"ruleVersion"`
	Status      string        `json:"status"`
	Processed   int           `json:"processed"`
	Awarded     int           `json:"awarded"`
	Diff        *BackfillDiff `json:"diff,omitempty"`
	CreatedBy   string        `json:"createdBy"`
	ApprovedBy  *string       `json:"approvedBy"`
	CreatedAt   time.Time     `json:"createdAt"`
}

// BackfillDiff is the dry-run delta.
type BackfillDiff struct {
	WouldAward int `json:"wouldAward"`
	WouldSkip  int `json:"wouldSkip"`
	Conflicts  int `json:"conflicts"`
}

// KillSwitch is one operational switch.
type KillSwitch struct {
	Key       string     `json:"key"`
	Enabled   bool       `json:"enabled"`
	Reason    *string    `json:"reason"`
	UpdatedBy *string    `json:"updatedBy"`
	UpdatedAt *time.Time `json:"updatedAt"`
}

// AuditEntry is one immutable admin trail row.
type AuditEntry struct {
	ID         string    `json:"id"`
	ActorRef   *string   `json:"actorRef"`
	Action     string    `json:"action"`
	EntityType string    `json:"entityType"`
	EntityID   *string   `json:"entityId"`
	Reason     *string   `json:"reason"`
	CreatedAt  time.Time `json:"createdAt"`
}
