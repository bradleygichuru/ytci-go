package recognition

import (
	"context"
	"strconv"
	"time"
)

// Source adapters build the frozen CloudEvent for one award source. They keep
// occurrence-key construction in the recognition module so a source handler
// cannot invent a key. Call RecordTx from the source transaction; Record is
// available for callers that own their own boundary.

func heritageEvent(subject, eventType, sourceType, siteID, suffix string, method, result string, privacy string, at time.Time) Event {
	key := "heritage.site:" + siteID
	switch sourceType {
	case "heritage_trail":
		key = "heritage.trail:" + siteID
	case "county":
		key = "heritage.county:" + siteID
	}
	if suffix != "" {
		key += ":" + suffix
	}
	return Event{
		Source: "ke.heritage.api", Type: eventType, Subject: subject, Time: at,
		Data: Payload{
			SourceType: sourceType, SourceID: siteID, OccurrenceKey: key,
			VerificationMethod: method, VerificationResult: result,
			PrivacyClass: privacy, Sensitivity: sensitivityFor(privacy),
		},
	}
}

// RecordHeritageVisit records a verified GPS, QR or manual-code site visit.
func (e *Engine) RecordHeritageVisit(ctx context.Context, subject, siteID, method, result, privacy string, at time.Time) (RecordResult, error) {
	return e.Record(ctx, heritageEvent(subject, "ke.heritage.visit.verified.v1",
		"heritage_site", siteID, "", method, result, privacy, at))
}

// RecordHeritageTrailStop records the optional 25 XP trail stop. XP is once
// per stop because the occurrence key includes the stop id.
func (e *Engine) RecordHeritageTrailStop(ctx context.Context, subject, trailID, stopID, method, result, privacy string, at time.Time) (RecordResult, error) {
	ev := heritageEvent(subject, "ke.heritage.visit.verified.v1",
		"heritage_site", trailID+"/stop/"+stopID, "", method, result, privacy, at)
	ev.RuleKeyHint = "heritage.trail_stop"
	return e.Record(ctx, ev)
}

// RecordHeritageStoryCut records the optional 20 XP story cut.
func (e *Engine) RecordHeritageStoryCut(ctx context.Context, subject, siteID, cutID, method, result, privacy string, at time.Time) (RecordResult, error) {
	ev := heritageEvent(subject, "ke.heritage.visit.verified.v1",
		"heritage_site", siteID+"/cut/"+cutID, "", method, result, privacy, at)
	ev.RuleKeyHint = "heritage.story_cut"
	return e.Record(ctx, ev)
}

// RecordHeritageTrail records a completed heritage trail.
func (e *Engine) RecordHeritageTrail(ctx context.Context, subject, trailID, method, result, privacy string, at time.Time) (RecordResult, error) {
	return e.Record(ctx, heritageEvent(subject, "ke.heritage.trail.completed.v1",
		"heritage_trail", trailID, "", method, result, privacy, at))
}

// RecordCountyCollected records the first verified site in a county.
func (e *Engine) RecordCountyCollected(ctx context.Context, subject string, countyGID int, method, result, privacy string, at time.Time) (RecordResult, error) {
	return e.Record(ctx, heritageEvent(subject, "ke.heritage.county.collected.v1",
		"county", strconv.Itoa(countyGID), "", method, result, privacy, at))
}

// RecordConservationParticipation records moderated conservation evidence.
func (e *Engine) RecordConservationParticipation(ctx context.Context, subject, activityID, method, result string, at time.Time) (RecordResult, error) {
	return e.Record(ctx, Event{
		Source: "ke.conservation.api", Type: "ke.conservation.participation.verified.v1",
		Subject: subject, Time: at,
		Data: Payload{
			SourceType: "conservation_activity", SourceID: activityID,
			OccurrenceKey:      "conservation.activity:" + activityID,
			VerificationMethod: method, VerificationResult: result, PrivacyClass: "private",
		},
	})
}

// RecordChallengeCompleted records a moderated challenge. Difficulty selects
// the immutable rule variant (standard 50, advanced 100, flagship 150).
func (e *Engine) RecordChallengeCompleted(ctx context.Context, subject, challengeID, difficulty string, at time.Time) (RecordResult, error) {
	hint := "challenge.standard"
	switch difficulty {
	case "advanced":
		hint = "challenge.advanced"
	case "flagship":
		hint = "challenge.flagship"
	}
	return e.Record(ctx, Event{
		Source: "ke.challenges.api", Type: "ke.challenge.completed.v1",
		Subject: subject, Time: at,
		Data: Payload{
			SourceType: "challenge", SourceID: challengeID,
			OccurrenceKey:      "challenge:" + challengeID,
			VerificationMethod: "moderation", VerificationResult: "moderated",
			PrivacyClass: "private",
		},
		RuleKeyHint: hint,
	})
}

// RecordCourseCompleted records a passed quiz. Course XP is first pass only:
// the occurrence key changes with the revision but the award key does not.
func (e *Engine) RecordCourseCompleted(ctx context.Context, subject, courseID string, revision int, at time.Time) (RecordResult, error) {
	rev := revision
	return e.Record(ctx, Event{
		Source: "ke.courses.api", Type: "ke.course.completed.v1",
		Subject: subject, Time: at,
		Data: Payload{
			SourceType: "course", SourceID: courseID,
			OccurrenceKey:      "course:" + courseID + ":" + strconv.Itoa(revision),
			VerificationMethod: "quiz", VerificationResult: "verified",
			PrivacyClass: "private", SourceRevision: &rev,
		},
	})
}

// RecordEventAttendance records a staff or QR check-in, never an RSVP.
func (e *Engine) RecordEventAttendance(ctx context.Context, subject, eventID, method string, at time.Time) (RecordResult, error) {
	return e.Record(ctx, Event{
		Source: "ke.events.api", Type: "ke.event.attended.v1",
		Subject: subject, Time: at,
		Data: Payload{
			SourceType: "event", SourceID: eventID,
			OccurrenceKey:      "event.attendance:" + eventID,
			VerificationMethod: method, VerificationResult: "verified",
			PrivacyClass: "private",
		},
	})
}

// RecordAudioStoryCompleted records a server-recorded Audio Story completion.
// The engine caps XP at five per week; the badge still lands per story.
func (e *Engine) RecordAudioStoryCompleted(ctx context.Context, subject, storyID string, at time.Time) (RecordResult, error) {
	return e.Record(ctx, Event{
		Source: "ke.audio.api", Type: "ke.audio.story.completed.v1",
		Subject: subject, Time: at,
		Data: Payload{
			SourceType: "audio_story", SourceID: storyID,
			OccurrenceKey:      "audio.story:" + storyID,
			VerificationMethod: "server_recorded", VerificationResult: "server_recorded",
			PrivacyClass: "private",
		},
	})
}

// RecordCommunityStoryApproved records a moderated community story. XP is
// capped at three per week.
func (e *Engine) RecordCommunityStoryApproved(ctx context.Context, subject, storyID string, at time.Time) (RecordResult, error) {
	return e.Record(ctx, Event{
		Source: "ke.community.api", Type: "ke.community.story.approved.v1",
		Subject: subject, Time: at,
		Data: Payload{
			SourceType: "community_story", SourceID: storyID,
			OccurrenceKey:      "community.story:" + storyID,
			VerificationMethod: "moderation", VerificationResult: "moderated",
			PrivacyClass: "public",
		},
	})
}

// RecordDestinationProgressed feeds private progress only (self-declared,
// zero XP).
func (e *Engine) RecordDestinationProgressed(ctx context.Context, subject, slug string, at time.Time) (RecordResult, error) {
	return e.Record(ctx, Event{
		Source: "ke.explore.api", Type: "ke.explore.destination.progressed.v1",
		Subject: subject, Time: at,
		Data: Payload{
			SourceType: "destination", SourceID: slug,
			OccurrenceKey:      "explore.destination:" + slug,
			VerificationMethod: "server_recorded", VerificationResult: "self_declared",
			PrivacyClass: "private",
		},
	})
}

// RecordSeasonCompleted records the once-per-season completion reward.
func (e *Engine) RecordSeasonCompleted(ctx context.Context, subject, seasonKey string, at time.Time) (RecordResult, error) {
	return e.Record(ctx, Event{
		Source: "ke.seasons.api", Type: "ke.season.completed.v1",
		Subject: subject, Time: at,
		Data: Payload{
			SourceType: "season", SourceID: seasonKey,
			OccurrenceKey:      "season:" + seasonKey,
			VerificationMethod: "server_recorded", VerificationResult: "server_recorded",
			PrivacyClass: "private",
		},
	})
}

func sensitivityFor(privacy string) string {
	if privacy == "sensitive" {
		return "sensitive"
	}
	return "standard"
}
