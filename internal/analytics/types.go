// Package analytics implements the first-party product analytics vertical
// frozen in api/openapi/contracts.v1.yaml (tag `analytics`) and ADR 0016.
//
// The package is pseudonymous by construction: event rows carry an
// analytics_id, never a user_id. The analytics_id -> user_id mapping is
// restricted to analytics_id_map and is only read for export, erasure and
// audited support access.
package analytics

import "time"

// Consent states from api/contracts/analytics.v1.yaml.
const (
	ConsentUnknown = "unknown"
	ConsentGranted = "granted"
	ConsentDenied  = "denied"
)

// Actor types from the frozen identity schema.
const (
	ActorGuest        = "guest"
	ActorAccount      = "account"
	ActorUnder18      = "under_18_private"
	ConsentScope      = "analytics"
	defaultConsentVer = "2026-11-01"
)

// Event families from ADR 0016.
const (
	FamilyScreenView  = "screen_view"
	FamilyAction      = "action"
	FamilyJourneyStep = "journey_step"
)

// InstallRequest is the body of POST /v1/mobile/analytics/installs.
type InstallRequest struct {
	InstallID  string `json:"installId"`
	Platform   string `json:"platform"`
	OSVersion  string `json:"osVersion,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
	Build      string `json:"build,omitempty"`
	Locale     string `json:"locale,omitempty"`
	ActorType  string `json:"actorType,omitempty"`
}

// Consent is the frozen AnalyticsConsent schema.
type Consent struct {
	AnalyticsID string     `json:"analyticsId"`
	Scope       string     `json:"scope"`
	Version     string     `json:"version"`
	State       string     `json:"state"`
	RecordedAt  *time.Time `json:"recordedAt,omitempty"`
}

// Identity is the frozen AnalyticsIdentity schema.
type Identity struct {
	AnalyticsID string  `json:"analyticsId"`
	InstallID   string  `json:"installId"`
	ActorType   string  `json:"actorType"`
	Consent     Consent `json:"consent"`
}

// ConsentWrite is the body of PUT /v1/mobile/analytics/consent. InstallID is
// optional in the frozen schema and required for guest writes: consent must be
// provable against the install that minted the analytics_id.
type ConsentWrite struct {
	AnalyticsID string `json:"analyticsId"`
	InstallID   string `json:"installId,omitempty"`
	Version     string `json:"version"`
	State       string `json:"state"`
	Source      string `json:"source"`
}

// Event is one event envelope from the frozen AnalyticsEvent schema.
type Event struct {
	EventID    string         `json:"eventId"`
	SessionID  string         `json:"sessionId"`
	SessionSeq int            `json:"sessionSeq"`
	Family     string         `json:"family"`
	Name       string         `json:"name"`
	Route      *string        `json:"route"`
	ClientTS   string         `json:"clientTs"`
	Properties map[string]any `json:"properties,omitempty"`
}

// Batch is the body of POST /v1/mobile/analytics/batches.
type Batch struct {
	AnalyticsID    string  `json:"analyticsId"`
	InstallID      string  `json:"installId"`
	ConsentVersion string  `json:"consentVersion"`
	AppVersion     string  `json:"appVersion"`
	Build          string  `json:"build,omitempty"`
	Platform       string  `json:"platform"`
	OSVersion      string  `json:"osVersion,omitempty"`
	Locale         string  `json:"locale,omitempty"`
	TZOffsetMin    *int    `json:"tzOffsetMin,omitempty"`
	Network        string  `json:"network,omitempty"`
	ActorType      string  `json:"actorType,omitempty"`
	SessionID      string  `json:"sessionId"`
	Events         []Event `json:"events"`
}

// BatchAck is the frozen AnalyticsBatchAck schema.
type BatchAck struct {
	BatchID          string   `json:"batchId"`
	Accepted         int      `json:"accepted"`
	Dropped          int      `json:"dropped"`
	UnknownKeys      int      `json:"unknownKeys"`
	RejectedEventIDs []string `json:"rejectedEventIds,omitempty"`
}

// MetricCell is one overview metric.
type MetricCell struct {
	Metric     string   `json:"metric"`
	Value      *float64 `json:"value"`
	Unit       string   `json:"unit"`
	Suppressed bool     `json:"suppressed"`
	Definition string   `json:"definition,omitempty"`
	Caveat     *string  `json:"caveat"`
}

// DailyPoint is one overview daily point.
type DailyPoint struct {
	Day        string `json:"day"`
	DAU        *int64 `json:"dau"`
	Guests     *int64 `json:"guests"`
	Accounts   *int64 `json:"accounts"`
	Suppressed bool   `json:"suppressed"`
}

// Overview is the frozen AnalyticsOverview schema.
type Overview struct {
	From  string       `json:"from"`
	To    string       `json:"to"`
	Cells []MetricCell `json:"cells"`
	Daily []DailyPoint `json:"daily,omitempty"`
}

// JourneyStepMetric is one journey step.
type JourneyStepMetric struct {
	Journey              string  `json:"journey"`
	Step                 string  `json:"step"`
	StepIndex            int     `json:"stepIndex"`
	Reached              *int64  `json:"reached"`
	Dropped              *int64  `json:"dropped"`
	MedianDurationBucket *string `json:"medianDurationBucket"`
	Suppressed           bool    `json:"suppressed"`
}

// FunnelMetric is one journey funnel.
type FunnelMetric struct {
	Journey    string   `json:"journey"`
	Entered    *int64   `json:"entered"`
	Completed  *int64   `json:"completed"`
	Conversion *float64 `json:"conversion"`
	Suppressed bool     `json:"suppressed"`
}

// RetentionMetric is one weekly cohort cell.
type RetentionMetric struct {
	CohortWeek string   `json:"cohortWeek"`
	WeekIndex  int      `json:"weekIndex"`
	Actors     *int64   `json:"actors"`
	Retained   *int64   `json:"retained"`
	Rate       *float64 `json:"rate"`
	Suppressed bool     `json:"suppressed"`
}

// AdoptionMetric is one feature adoption row.
type AdoptionMetric struct {
	Feature       string   `json:"feature"`
	Reach         *int64   `json:"reach"`
	DepthPerActor *float64 `json:"depthPerActor"`
	RepeatRate    *float64 `json:"repeatRate"`
	Suppressed    bool     `json:"suppressed"`
}

// DataQuality is the frozen DataQuality schema.
type DataQuality struct {
	From        string             `json:"from"`
	To          string             `json:"to"`
	Ingest      IngestQuality      `json:"ingest"`
	Consent     ConsentQuality     `json:"consent,omitempty"`
	Queue       QueueQuality       `json:"queue"`
	Suppression SuppressionQuality `json:"suppression"`
	Rollups     []RollupStatus     `json:"rollups,omitempty"`
}

// IngestQuality is ingest volume and drops.
type IngestQuality struct {
	Events          int64 `json:"events"`
	Batches         int64 `json:"batches"`
	DroppedEvents   int64 `json:"droppedEvents"`
	UnknownKeys     int64 `json:"unknownKeys"`
	RejectedBatches int64 `json:"rejectedBatches"`
}

// ConsentQuality is consent coverage.
type ConsentQuality struct {
	Granted      int64 `json:"granted"`
	Denied       int64 `json:"denied"`
	Unknown      int64 `json:"unknown"`
	StaleVersion int64 `json:"staleVersion"`
}

// QueueQuality is client queue loss.
type QueueQuality struct {
	ClientDrops int64 `json:"clientDrops"`
	Expired     int64 `json:"expired"`
}

// SuppressionQuality is suppression coverage.
type SuppressionQuality struct {
	Threshold       int   `json:"threshold"`
	CellsSuppressed int64 `json:"cellsSuppressed"`
}

// RollupStatus is the health of one nightly rollup.
type RollupStatus struct {
	Rollup  string `json:"rollup"`
	LastDay string `json:"lastDay"`
	Status  string `json:"status"`
}

// Export is the frozen AnalyticsExport schema.
type Export struct {
	AnalyticsID string          `json:"analyticsId"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Events      []ExportEvent   `json:"events"`
	Sessions    []ExportSession `json:"sessions"`
}

// ExportEvent is one event in a reader export.
type ExportEvent struct {
	EventID    string         `json:"eventId"`
	SessionID  string         `json:"sessionId"`
	SessionSeq int            `json:"sessionSeq"`
	Family     string         `json:"family"`
	Name       string         `json:"name"`
	Route      *string        `json:"route"`
	ClientTS   string         `json:"clientTs"`
	Properties map[string]any `json:"properties,omitempty"`
}

// ExportSession is one session in a reader export.
type ExportSession struct {
	SessionID  string     `json:"sessionId"`
	StartedAt  time.Time  `json:"startedAt"`
	EndedAt    *time.Time `json:"endedAt"`
	EventCount int        `json:"eventCount"`
}

// ErasureRequest is the frozen ErasureRequest schema.
type ErasureRequest struct {
	ID               string     `json:"id"`
	SubjectType      string     `json:"subjectType"`
	Status           string     `json:"status"`
	RequestedAt      time.Time  `json:"requestedAt"`
	TargetBy         time.Time  `json:"targetBy"`
	BackupCycleUntil *time.Time `json:"backupCycleUntil"`
}

// Acknowledgement is the shared frozen acknowledgement.
type Acknowledgement struct {
	OK      bool   `json:"ok"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
}

// RejectError is an expected request rejection carried out of the store.
type RejectError struct {
	Status  int
	Code    string
	Message string
}

func (e *RejectError) Error() string { return e.Code + ": " + e.Message }

func reject(status int, code, message string) *RejectError {
	return &RejectError{Status: status, Code: code, Message: message}
}
