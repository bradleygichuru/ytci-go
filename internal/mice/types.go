// Package mice implements the MICE vertical: events, venues, organizers,
// agenda, registration, tickets, check-in, reports and event notifications.
//
// The wire shapes in this package mirror api/openapi/contracts.v1.yaml
// (tag `mice`, contract 1.0.0). Vocabulary comes from
// api/contracts/mice.v1.yaml; roles and permissions come from
// api/contracts/roles.v1.yaml through internal/contracts.
package mice

import "time"

// ---------------------------------------------------------------------------
// Envelopes
// ---------------------------------------------------------------------------

// Page is the frozen cursor envelope.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
}

// ValidationError is one field failure.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error is the frozen error envelope.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Acknowledgement is the generic accepted response.
type Acknowledgement struct {
	OK      bool   `json:"ok"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

type VenueRef struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	County    string `json:"county"`
	CountyGid *int   `json:"countyGid,omitempty"`
}

type OrganizerRef struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
	TrustTier   string `json:"trustTier"`
	Label       string `json:"label"`
}

type AccessibilitySummary struct {
	State      string     `json:"state"`
	VerifiedAt *time.Time `json:"verifiedAt,omitempty"`
	Label      string     `json:"label,omitempty"`
}

type Price struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Display  string  `json:"display,omitempty"`
}

type Sales struct {
	Status     string     `json:"status"`
	TicketURL  string     `json:"ticketUrl"`
	SoldBy     string     `json:"soldBy"`
	SalesEnd   *time.Time `json:"salesEnd,omitempty"`
	Disclosure string     `json:"disclosure,omitempty"`
}

// EventSummary is the discovery card.
type EventSummary struct {
	ID               string               `json:"id"`
	Slug             string               `json:"slug,omitempty"`
	Title            string               `json:"title"`
	EventClass       string               `json:"eventClass"`
	Format           string               `json:"format"`
	Access           string               `json:"access"`
	Status           string               `json:"status"`
	StartsAt         time.Time            `json:"startsAt"`
	EndsAt           *time.Time           `json:"endsAt,omitempty"`
	Timezone         string               `json:"timezone"`
	County           string               `json:"county"`
	CountyGid        *int                 `json:"countyGid,omitempty"`
	Venue            *VenueRef            `json:"venue,omitempty"`
	Organizer        *OrganizerRef        `json:"organizer,omitempty"`
	ImageURL         string               `json:"imageUrl,omitempty"`
	Capacity         *int                 `json:"capacity,omitempty"`
	SeatsRemaining   *int                 `json:"seatsRemaining,omitempty"`
	WaitlistOpen     bool                 `json:"waitlistOpen"`
	RegistrationMode string               `json:"registrationMode,omitempty"`
	Price            *Price               `json:"price,omitempty"`
	Accessibility    AccessibilitySummary `json:"accessibility"`
	Saved            bool                 `json:"saved"`

	// Legacy passthrough fields retained for the pre-MICE mobile client.
	LocationLat *float64 `json:"locationLat,omitempty"`
	LocationLng *float64 `json:"locationLng,omitempty"`
}

type RegistrationQuestion struct {
	ID         string   `json:"id"`
	PromptEn   string   `json:"promptEn"`
	PromptSw   string   `json:"promptSw,omitempty"`
	AnswerKind string   `json:"answerKind"`
	Choices    []string `json:"choices,omitempty"`
	Required   bool     `json:"required"`
}

type Speaker struct {
	ID            string `json:"id,omitempty"`
	DisplayName   string `json:"displayName"`
	Title         string `json:"title,omitempty"`
	Organization  string `json:"organization,omitempty"`
	Role          string `json:"role,omitempty"`
	PhotoURL      string `json:"photoUrl,omitempty"`
	ConsentStatus string `json:"consentStatus,omitempty"`
}

type Session struct {
	ID          string    `json:"id"`
	Day         string    `json:"day"`
	StartsAt    time.Time `json:"startsAt"`
	EndsAt      time.Time `json:"endsAt"`
	Room        string    `json:"room,omitempty"`
	Track       string    `json:"track,omitempty"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Capacity    *int      `json:"capacity,omitempty"`
	Speakers    []Speaker `json:"speakers,omitempty"`
}

// EventDetail is the event page.
type EventDetail struct {
	EventSummary
	Description  string    `json:"description,omitempty"`
	Highlights   []string  `json:"highlights,omitempty"`
	Agenda       []Session `json:"agenda,omitempty"`
	SessionCount int       `json:"sessionCount,omitempty"`
	SpeakerCount int       `json:"speakerCount,omitempty"`
	// AttendeeCount is the legacy-compatible count of confirmed + checked-in
	// registrations (the old handler counted "interested" and overstated it).
	AttendeeCount         int                    `json:"attendeeCount"`
	AccessibilityDetail   *AccessibilityBlock    `json:"accessibilityDetail,omitempty"`
	VenueDetail           *Venue                 `json:"venueDetail,omitempty"`
	OrganizerDetail       *OrganizerProfile      `json:"organizerDetail,omitempty"`
	RegistrationQuestions []RegistrationQuestion `json:"registrationQuestions,omitempty"`
	Sales                 *Sales                 `json:"sales,omitempty"`
	Registration          *Registration          `json:"registration,omitempty"`
	Notifications         []EventNotification    `json:"notifications,omitempty"`
}

type AccessibilityBlock struct {
	ReportedBy string              `json:"reportedBy"`
	VerifiedAt *time.Time          `json:"verifiedAt,omitempty"`
	Label      string              `json:"label,omitempty"`
	Fields     AccessibilityFields `json:"fields"`
}

type AccessibilityFields struct {
	StepFreeEntrance      string `json:"stepFreeEntrance"`
	StepFreeRoute         string `json:"stepFreeRoute"`
	AccessibleToilet      string `json:"accessibleToilet"`
	AccessibleParking     string `json:"accessibleParking,omitempty"`
	AccessibleSeating     string `json:"accessibleSeating,omitempty"`
	Captioning            string `json:"captioning,omitempty"`
	SignLanguage          string `json:"signLanguage,omitempty"`
	HearingLoop           string `json:"hearingLoop,omitempty"`
	QuietRoom             string `json:"quietRoom,omitempty"`
	AssistanceDogs        string `json:"assistanceDogs,omitempty"`
	CompanionTicketPolicy string `json:"companionTicketPolicy,omitempty"`
	ContentNotes          string `json:"contentNotes,omitempty"`
}

// ---------------------------------------------------------------------------
// Venues
// ---------------------------------------------------------------------------

type Venue struct {
	VenueRef
	ID               string              `json:"id,omitempty"`
	AddressLine1     string              `json:"addressLine1,omitempty"`
	AddressLine2     string              `json:"addressLine2,omitempty"`
	Latitude         *float64            `json:"latitude,omitempty"`
	Longitude        *float64            `json:"longitude,omitempty"`
	CapacityInPerson *int                `json:"capacityInPerson,omitempty"`
	CapacityOnline   *int                `json:"capacityOnline,omitempty"`
	Rooms            []string            `json:"rooms,omitempty"`
	TransportNotes   string              `json:"transportNotes,omitempty"`
	DropoffNotes     string              `json:"dropoffNotes,omitempty"`
	ParkingNotes     string              `json:"parkingNotes,omitempty"`
	Accessibility    *AccessibilityBlock `json:"accessibility,omitempty"`
	VerifiedAt       *time.Time          `json:"verifiedAt,omitempty"`
	// Retirement/merge is public only through absence: the public venue read
	// 404s and public event payloads never carry a retired venue.
	RetiredAt         *time.Time      `json:"retiredAt,omitempty"`
	RetiredReason     string          `json:"retiredReason,omitempty"`
	MergedIntoVenueID string          `json:"mergedIntoVenueId,omitempty"`
	Evidence          []VenueEvidence `json:"evidence"`
}

// VenueEvidence is one append-only evidence claim recorded against a venue.
type VenueEvidence struct {
	ID         string    `json:"id"`
	VenueID    string    `json:"venueId"`
	Claim      string    `json:"claim"`
	Note       string    `json:"note"`
	Reference  string    `json:"reference,omitempty"`
	RecordedBy string    `json:"recordedBy,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ---------------------------------------------------------------------------
// Organizers
// ---------------------------------------------------------------------------

type OrganizerProfile struct {
	OrganizerRef
	ID                    string                     `json:"id,omitempty"`
	About                 string                     `json:"about,omitempty"`
	LogoURL               string                     `json:"logoUrl,omitempty"`
	ContactEmail          string                     `json:"contactEmail,omitempty"`
	ContactPhone          string                     `json:"contactPhone,omitempty"`
	WebsiteURL            string                     `json:"websiteUrl,omitempty"`
	VerificationStatus    string                     `json:"verificationStatus,omitempty"`
	VerifiedAt            *time.Time                 `json:"verifiedAt,omitempty"`
	VerificationExpiresAt *time.Time                 `json:"verificationExpiresAt,omitempty"`
	RevokedReason         string                     `json:"revokedReason,omitempty"`
	UpcomingEvents        []EventSummary             `json:"upcomingEvents,omitempty"`
	PastEventCount        int                        `json:"pastEventCount,omitempty"`
	EvidenceRequests      []OrganizerEvidenceRequest `json:"evidenceRequests"`
}

// OrganizerEvidenceRequest is an "ask the organizer for more evidence" note.
// It keeps the verification record pending until a decision resolves it.
type OrganizerEvidenceRequest struct {
	ID          string     `json:"id"`
	OrganizerID string     `json:"organizerId"`
	Note        string     `json:"note"`
	RequestedBy string     `json:"requestedBy,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
}

// OrganizerEvidenceRequestResult returns the new request with the organizer so
// the admin screen can re-render without a second read.
type OrganizerEvidenceRequestResult struct {
	Request   OrganizerEvidenceRequest `json:"request"`
	Organizer OrganizerProfile         `json:"organizer"`
}

// ---------------------------------------------------------------------------
// Registration, ticket, check-in
// ---------------------------------------------------------------------------

type Ticket struct {
	ID             string    `json:"id"`
	RegistrationID string    `json:"registrationId"`
	QRPayload      string    `json:"qrPayload"`
	IssuedAt       time.Time `json:"issuedAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
	State          string    `json:"state,omitempty"`
}

type CheckIn struct {
	ID             string    `json:"id"`
	RegistrationID string    `json:"registrationId"`
	EventID        string    `json:"eventId"`
	CheckedInAt    time.Time `json:"checkedInAt"`
	Method         string    `json:"method"`
	StaffRef       string    `json:"staffRef,omitempty"`
	AttendeeName   string    `json:"attendeeName,omitempty"`
	Duplicate      bool      `json:"duplicate,omitempty"`
}

type Registration struct {
	ID                    string            `json:"id"`
	EventID               string            `json:"eventId"`
	Status                string            `json:"status"`
	Answers               map[string]string `json:"answers,omitempty"`
	ReminderOffsetMinutes *int              `json:"reminderOffsetMinutes,omitempty"`
	WaitlistPosition      *int              `json:"waitlistPosition,omitempty"`
	ConfirmBy             *time.Time        `json:"confirmBy,omitempty"`
	CreatedAt             time.Time         `json:"createdAt"`
	CancelledAt           *time.Time        `json:"cancelledAt,omitempty"`
	Ticket                *Ticket           `json:"ticket,omitempty"`
	CheckIn               *CheckIn          `json:"checkIn,omitempty"`
	Event                 *EventSummary     `json:"event,omitempty"`
}

type AdminRegistration struct {
	Registration
	Attendee       *AttendeeRef `json:"attendee,omitempty"`
	AnswersSummary string       `json:"answersSummary,omitempty"`
}

type AttendeeRef struct {
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
}

type TicketHandoff struct {
	ID         string    `json:"id"`
	EventID    string    `json:"eventId"`
	CreatedAt  time.Time `json:"createdAt"`
	Price      *Price    `json:"price,omitempty"`
	TicketURL  string    `json:"ticketUrl"`
	SoldBy     string    `json:"soldBy"`
	Disclosure string    `json:"disclosure,omitempty"`
}

type CheckInScanResult struct {
	ClientScanID string   `json:"clientScanId"`
	Status       string   `json:"status"`
	Reason       string   `json:"reason,omitempty"`
	CheckIn      *CheckIn `json:"checkIn,omitempty"`
}

type CheckInScanBatchResult struct {
	Results    []CheckInScanResult `json:"results"`
	Applied    int                 `json:"applied"`
	Duplicates int                 `json:"duplicates"`
	Rejected   int                 `json:"rejected"`
}

// ---------------------------------------------------------------------------
// Reports, notifications, change notices
// ---------------------------------------------------------------------------

type EventReport struct {
	ID          string     `json:"id"`
	EventID     string     `json:"eventId,omitempty"`
	OrganizerID string     `json:"organizerId,omitempty"`
	Reason      string     `json:"reason"`
	Details     string     `json:"details,omitempty"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"createdAt"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
}

type EventNotification struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	EventVersion int        `json:"eventVersion,omitempty"`
	Title        string     `json:"title,omitempty"`
	Body         string     `json:"body,omitempty"`
	DeepLink     string     `json:"deepLink,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	ReadAt       *time.Time `json:"readAt,omitempty"`
}

type ChangeNotice struct {
	ID             string   `json:"id"`
	EventID        string   `json:"eventId"`
	FromVersion    int      `json:"fromVersion,omitempty"`
	ToVersion      int      `json:"toVersion,omitempty"`
	MaterialFields []string `json:"materialFields"`
	CopyEn         string   `json:"copyEn"`
	CopySw         string   `json:"copySw,omitempty"`
	Status         string   `json:"status"`
	RecipientCount int      `json:"recipientCount,omitempty"`
}

// ---------------------------------------------------------------------------
// Admin operations (additive to contract 1.0.0)
// ---------------------------------------------------------------------------

// WaitlistOffer is the server record of a seat offered to a waitlisted
// registration. The offer identity is the registration id: the table enforces
// one active registration per event and user.
type WaitlistOffer struct {
	EventID        string    `json:"eventId"`
	RegistrationID string    `json:"registrationId"`
	AttendeeName   string    `json:"attendeeName,omitempty"`
	OfferedAt      time.Time `json:"offeredAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
	OfferedBy      string    `json:"offeredBy,omitempty"`
	Note           string    `json:"note,omitempty"`
}

// MiceAuditEvent is one append-only admin audit row.
type MiceAuditEvent struct {
	ID         string         `json:"id"`
	EntityType string         `json:"entityType"`
	EntityID   string         `json:"entityId"`
	EventID    string         `json:"eventId,omitempty"`
	Action     string         `json:"action"`
	ActorID    string         `json:"actorId,omitempty"`
	ActorName  string         `json:"actorName,omitempty"`
	Summary    string         `json:"summary"`
	Details    map[string]any `json:"details,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
}
