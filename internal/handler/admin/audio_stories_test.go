package admin

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func readyStory() storySnapshot {
	return storySnapshot{Title: "The Last Mangrove", Summary: "A coastal story.", Producer: "YTCI", Community: "Kilifi elders", CoverID: "cover-1", CollectionID: "col-1", CollectionState: "published"}
}

func readyRevision() revisionSnapshot {
	return revisionSnapshot{PlaceScope: "county", Counties: []string{"Kilifi"}, RightsBasis: "ytci_owned", RightsHolder: "YTCI", RiskTier: "standard", Note: "Coastal displacement."}
}

func readyTrack() trackSnapshot {
	return trackSnapshot{Language: "en", Narrator: "Amina Yusuf", ObjectKey: "audio/en.m4a", Duration: 720, ConsentDate: "2026-10-01", ConsentRef: "REL-2026-014"}
}

func readyTranscript() transcriptSnapshot {
	return transcriptSnapshot{Language: "en", Paragraphs: []transcriptParagraph{
		{Text: "The mangrove keeps the coast.", StartSeconds: floatPtr(0)},
		{Text: "The elders replant it.", StartSeconds: floatPtr(42)},
	}}
}

func floatPtr(v float64) *float64 { return &v }

func TestEvaluateReadinessPass(t *testing.T) {
	result := evaluateReadiness(readyStory(), readyRevision(), []trackSnapshot{readyTrack()}, []transcriptSnapshot{readyTranscript()})
	assert.True(t, result.Ready)
	assert.Empty(t, result.Blocking)
	assert.Len(t, result.Warnings, 1) // one language
}

func TestEvaluateReadinessBlocksMissingTranscriptAndCover(t *testing.T) {
	story := readyStory()
	story.CoverID = ""
	result := evaluateReadiness(story, readyRevision(), []trackSnapshot{readyTrack()}, nil)
	assert.False(t, result.Ready)
	assert.Contains(t, codes(result.Blocking), "missing_cover")
	assert.Contains(t, codes(result.Blocking), "missing_transcript_en")
}

func TestEvaluateReadinessBlocksCueBeyondDuration(t *testing.T) {
	transcript := readyTranscript()
	transcript.Paragraphs[1].StartSeconds = floatPtr(999)
	result := evaluateReadiness(readyStory(), readyRevision(), []trackSnapshot{readyTrack()}, []transcriptSnapshot{transcript})
	assert.False(t, result.Ready)
	assert.Contains(t, codes(result.Blocking), "invalid_transcript_en")
}

func TestUnpublishedCollectionIsWarningNotBlocking(t *testing.T) {
	story := readyStory()
	story.CollectionState = "draft"
	result := evaluateReadiness(story, readyRevision(), []trackSnapshot{readyTrack()}, []transcriptSnapshot{readyTranscript()})
	assert.True(t, result.Ready)
	assert.Contains(t, codes(result.Warnings), "collection_unpublished")
}

func TestRightsEvidenceRequiredForPartnerBasis(t *testing.T) {
	rev := readyRevision()
	rev.RightsBasis = "partner_permission"
	rev.LicenceReference = ""
	result := evaluateReadiness(readyStory(), rev, []trackSnapshot{readyTrack()}, []transcriptSnapshot{readyTranscript()})
	assert.False(t, result.Ready)
	assert.Contains(t, codes(result.Blocking), "missing_rights_evidence")

	rev.LicenceReference = "MOU-2026-014"
	result = evaluateReadiness(readyStory(), rev, []trackSnapshot{readyTrack()}, []transcriptSnapshot{readyTranscript()})
	assert.True(t, result.Ready)
}

func TestSensitiveStoryWithoutContentNoteWarns(t *testing.T) {
	rev := readyRevision()
	rev.RiskTier = "sensitive"
	rev.Note = ""
	result := evaluateReadiness(readyStory(), rev, []trackSnapshot{readyTrack()}, []transcriptSnapshot{readyTranscript()})
	assert.True(t, result.Ready)
	assert.Contains(t, codes(result.Warnings), "no_content_note")
}

func TestValidateParagraphsOrdering(t *testing.T) {
	err := validateParagraphs([]transcriptParagraph{
		{Text: "one", StartSeconds: floatPtr(10)},
		{Text: "two", StartSeconds: floatPtr(5)},
	}, nil)
	assert.Error(t, err)
}

func TestValidateParagraphsRejectsEmptyAndNegative(t *testing.T) {
	assert.Error(t, validateParagraphs(nil, nil))
	assert.Error(t, validateParagraphs([]transcriptParagraph{{Text: "  "}}, nil))
	assert.Error(t, validateParagraphs([]transcriptParagraph{{Text: "one", StartSeconds: floatPtr(-2)}}, nil))
}

func TestValidateTrack(t *testing.T) {
	assert.NoError(t, validateTrack("en", "audio/en.m4a", "Amina", 600))
	assert.Error(t, validateTrack("fr", "audio/fr.m4a", "Amina", 600))
	assert.Error(t, validateTrack("en", "", "Amina", 600))
	assert.Error(t, validateTrack("en", "audio/en.m4a", "", 600))
	assert.Error(t, validateTrack("en", "audio/en.m4a", "Amina", 29))
	assert.Error(t, validateTrack("en", "audio/en.m4a", "Amina", 3601))
}

func TestReadinessBlocksMissingNarratorConsent(t *testing.T) {
	track := readyTrack()
	track.ConsentRef = ""
	result := evaluateReadiness(readyStory(), readyRevision(), []trackSnapshot{track}, []transcriptSnapshot{readyTranscript()})
	assert.False(t, result.Ready)
	assert.Contains(t, codes(result.Blocking), "missing_narrator_consent_en")
}

func TestPlaceScopeRules(t *testing.T) {
	assert.True(t, validPlace("county", []string{"Kilifi"}))
	assert.False(t, validPlace("county", nil))
	assert.True(t, validPlace("multi_county", []string{"Kilifi", "Mombasa"}))
	assert.True(t, validPlace("national", nil))
	assert.False(t, validPlace("national", []string{"Kilifi"}))
}

func TestRightsExpiryWarning(t *testing.T) {
	rev := readyRevision()
	soon := time.Now().AddDate(0, 0, 12)
	rev.RightsExpiresAt = &soon
	result := evaluateReadiness(readyStory(), rev, []trackSnapshot{readyTrack()}, []transcriptSnapshot{readyTranscript()})
	assert.Contains(t, codes(result.Warnings), "rights_expiring")
}

func TestRightsExpiredBlocks(t *testing.T) {
	rev := readyRevision()
	past := time.Now().AddDate(0, 0, -1)
	rev.RightsExpiresAt = &past
	result := evaluateReadiness(readyStory(), rev, []trackSnapshot{readyTrack()}, []transcriptSnapshot{readyTranscript()})
	assert.False(t, result.Ready)
	assert.Contains(t, codes(result.Blocking), "rights_expired")
}

func codes(items []readinessItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Code)
	}
	return out
}
