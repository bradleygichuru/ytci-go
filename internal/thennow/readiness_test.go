package thennow

import "testing"

func completeSnapshot() Snapshot {
	return Snapshot{
		Title:         "The railway that built Nairobi",
		Promise:       "See the museum yard as it is today.",
		Story:         "A current-scene walk through the yard.",
		Duration:      77,
		PrimaryMode:   "camera_video",
		Languages:     []string{"EN", "SW"},
		RightsRecord:  "Client footage, cleared",
		RightsExpiry:  "2027-06-30",
		ConsentRecord: "Community review recorded",
		Sensitivity:   "standard",
		VideoMaster:   "then-now/railway/master.mp4",
		Renditions:    map[string]string{"1080": "Ready", "720": "Ready", "540": "Ready"},
		RenditionKeys: map[string]string{"1080": "master-1080.mp4", "720": "master-720.mp4", "540": "master-540.mp4"},
		Poster:        true,
		PosterKey:     "poster.jpg",
		Captions:      map[string]bool{"en": true, "sw": true},
		CaptionKeys:   map[string]string{"en": "en.vtt", "sw": "sw.vtt"},
		Transcript:    true,
		TranscriptKey: "transcript.txt",
		TextCards: []TextCard{
			{At: 6, Text: "1899 — the Uganda Railway reaches Nairobi."},
			{At: 22, Text: "The railway helped Nairobi grow."},
		},
	}
}

func TestExperienceReadinessPassesCompleteDraft(t *testing.T) {
	readiness := ExperienceReadiness(completeSnapshot(), "draft", 0, 1)
	if !readiness.ReadyToSubmit {
		t.Fatalf("expected ready to submit, blockers: %#v", readiness.Blockers)
	}
	if len(readiness.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", readiness.Blockers)
	}
}

func TestExperienceReadinessBlocksMissingFields(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.Title = ""
	snapshot.Story = ""
	snapshot.VideoMaster = ""
	snapshot.Renditions = map[string]string{}
	snapshot.Poster = false
	snapshot.Captions = map[string]bool{}
	snapshot.Transcript = false
	snapshot.RightsRecord = ""
	snapshot.ConsentRecord = ""
	snapshot.TextCards = nil

	readiness := ExperienceReadiness(snapshot, "draft", 0, 1)
	if readiness.ReadyToSubmit {
		t.Fatal("expected blockers to prevent submission")
	}
	sections := map[string]bool{}
	for _, blocker := range readiness.Blockers {
		sections[blocker.Section] = true
	}
	for _, section := range []string{"story", "media", "accessibility", "rights", "cards"} {
		if !sections[section] {
			t.Fatalf("expected %s blocker, got %#v", section, readiness.Blockers)
		}
	}
}

func TestExperienceReadinessBlocksFrozenRevision(t *testing.T) {
	readiness := ExperienceReadiness(completeSnapshot(), "in_review", 3, 4)
	if readiness.ReadyToSubmit {
		t.Fatal("a frozen revision cannot be submitted")
	}
	found := false
	for _, blocker := range readiness.Blockers {
		if blocker.Section == "review" && blocker.Label == "Review decision" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a review decision blocker, got %#v", readiness.Blockers)
	}
}

func TestExperienceReadinessAllowsApprovedRevisionToPublish(t *testing.T) {
	readiness := ExperienceReadiness(completeSnapshot(), "approved", 3, 4)
	if !readiness.ReadyToPublish {
		t.Fatalf("expected ready to publish, blockers: %#v", readiness.Blockers)
	}
}

func TestExperienceReadinessBlocksExpiredRights(t *testing.T) {
	snapshot := completeSnapshot()
	snapshot.RightsExpiry = "2020-01-01"
	readiness := ExperienceReadiness(snapshot, "draft", 0, 1)
	if readiness.ReadyToSubmit {
		t.Fatal("expired rights must block submission")
	}
	found := false
	for _, blocker := range readiness.Blockers {
		if blocker.Section == "rights" && blocker.Label == "Rights have expired" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected expired-rights blocker, got %#v", readiness.Blockers)
	}
}

func TestSiteReadiness(t *testing.T) {
	site := Site{
		Name:                 "Nairobi Railway Museum",
		County:               "Nairobi",
		SiteType:             "historical",
		AccessMode:           "camera_video",
		Sensitivity:          "standard",
		PhotographyPolicy:    "Allowed with attribution",
		CulturalAuthority:    "National Museums of Kenya",
		ConsentRecord:        "Community review recorded",
		PublishedExperiences: 1,
	}
	readiness := SiteReadiness(site)
	if !readiness.ReadyToPublish {
		t.Fatalf("expected ready site, blockers: %#v", readiness.Blockers)
	}

	site.ConsentRecord = ""
	site.PublishedExperiences = 0
	readiness = SiteReadiness(site)
	if readiness.ReadyToPublish {
		t.Fatal("expected missing consent and experiences to block")
	}
}
