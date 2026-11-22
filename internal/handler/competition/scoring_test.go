package competition

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWeightedTotal(t *testing.T) {
	assert.Equal(t, 100.0, WeightedTotal(Criteria{10, 10, 10, 10}))
	assert.Equal(t, 10.0, WeightedTotal(Criteria{1, 1, 1, 1}))
	// 8*25 + 7*30 + 6*25 + 5*20 = 200+210+150+100 = 660 / 10 = 66.
	assert.Equal(t, 66.0, WeightedTotal(Criteria{8, 7, 6, 5}))
}

func TestNormalizePublicScore(t *testing.T) {
	assert.Equal(t, 100.0, NormalizePublicScore(10, 10))
	assert.Equal(t, 50.0, NormalizePublicScore(5, 10))
	assert.Equal(t, 33.33, NormalizePublicScore(1, 3))
	assert.Equal(t, 0.0, NormalizePublicScore(0, 10))
	assert.Equal(t, 0.0, NormalizePublicScore(4, 0))
}

func TestFinalScoreRespectsMode(t *testing.T) {
	assert.Equal(t, 79.0, FinalScore(70, 100, VoteModeWeighted))
	assert.Equal(t, 70.0, FinalScore(70, 100, VoteModePeopleChoiceOnly))
	assert.Equal(t, 70.0, FinalScore(70, 100, VoteModeNone))
	assert.Equal(t, weights{Expert: 1, Public: 0}, WeightsFor(VoteModePeopleChoiceOnly))
	assert.Equal(t, weights{Expert: 0.7, Public: 0.3}, WeightsFor(VoteModeWeighted))
}

func TestRankCategoryTieBreak(t *testing.T) {
	entries := []ScoredEntry{
		{EntryID: "a", Title: "A", Expert: 80, Craft: 9, Originality: 8},
		{EntryID: "b", Title: "B", Expert: 80, Craft: 10, Originality: 5},
		{EntryID: "c", Title: "C", Expert: 80, Craft: 9, Originality: 9},
		{EntryID: "d", Title: "D", Expert: 90, Craft: 1, Originality: 1},
	}
	ranked := RankCategory(entries, VoteModePeopleChoiceOnly)
	assert.Equal(t, "d", ranked[0].EntryID)
	assert.Equal(t, "b", ranked[1].EntryID)
	assert.Equal(t, "c", ranked[2].EntryID)
	assert.Equal(t, "a", ranked[3].EntryID)
	assert.False(t, ranked[0].IsTie)
	assert.True(t, ranked[1].IsTie)
	assert.True(t, ranked[3].IsTie)
}

func TestLeaderboardAndPeopleChoice(t *testing.T) {
	entries := []ScoredEntry{
		{EntryID: "a", Title: "A", Votes: 4},
		{EntryID: "b", Title: "B", Votes: 9},
		{EntryID: "c", Title: "C", Votes: 9},
	}
	rows := LeaderboardRows(entries)
	assert.Equal(t, "b", rows[0].EntryID)
	assert.Equal(t, "c", rows[1].EntryID)
	assert.Equal(t, 1, rows[0].Rank)
	assert.Equal(t, 3, rows[2].Rank)

	choice := PeopleChoice(entries)
	assert.Equal(t, "b", choice.EntryID)
	assert.Equal(t, 9, choice.Votes)
	assert.Nil(t, PeopleChoice([]ScoredEntry{{EntryID: "a", Votes: 0}}))
}

func TestLedgerHashIsStable(t *testing.T) {
	a := LedgerHash([]string{"b", "a", "c"})
	b := LedgerHash([]string{"c", "b", "a"})
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, LedgerHash([]string{"a", "b"}))
	assert.Len(t, a, 64)
}

func TestAgeRangeIsConfiguration(t *testing.T) {
	min, max := 21, 45
	assert.NoError(t, ValidateAgeRange(&min, &max))
	assert.True(t, AgeWithinRange(21, &min, &max))
	assert.True(t, AgeWithinRange(45, &min, &max))
	assert.False(t, AgeWithinRange(20, &min, &max))
	assert.False(t, AgeWithinRange(46, &min, &max))
	assert.True(t, AgeWithinRange(60, nil, nil))
	badMin, badMax := 45, 21
	assert.Error(t, ValidateAgeRange(&badMin, &badMax))
	negative := -1
	assert.Error(t, ValidateAgeRange(&negative, nil))
}

func TestCheckVote(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	opened := now.Add(-time.Hour)
	closes := now.Add(time.Hour)
	base := VoteCheck{
		EditionStatus: "voting", PublicVoteMode: VoteModePeopleChoiceOnly,
		VotingOpensAt: &opened, VotingClosesAt: &closes, Now: now,
		AccountCreatedAt: now.Add(-48 * time.Hour), EmailVerified: true,
		MaxPerCategory: 5, MaxTotal: 10,
	}
	assert.Nil(t, CheckVote(base))

	cases := []struct {
		name string
		mut  func(*VoteCheck)
		code string
	}{
		{"disabled", func(c *VoteCheck) { c.PublicVoteMode = VoteModeNone }, "VOTING_DISABLED"},
		{"draft edition", func(c *VoteCheck) { c.EditionStatus = "draft" }, "VOTING_CLOSED"},
		{"missing window", func(c *VoteCheck) { c.VotingClosesAt = nil }, "VOTING_CLOSED"},
		{"not open", func(c *VoteCheck) { c.Now = opened.Add(-time.Minute) }, "VOTING_NOT_OPEN"},
		{"closed", func(c *VoteCheck) { c.Now = closes.Add(time.Minute) }, "VOTING_CLOSED"},
		{"unverified", func(c *VoteCheck) { c.EmailVerified = false }, "EMAIL_UNVERIFIED"},
		{"new account", func(c *VoteCheck) { c.AccountCreatedAt = opened }, "ACCOUNT_TOO_NEW"},
		{"self", func(c *VoteCheck) { c.IsSelf = true }, "SELF_VOTE"},
		{"team", func(c *VoteCheck) { c.IsTeamMember = true }, "TEAM_VOTE"},
		{"judge", func(c *VoteCheck) { c.IsJudgeInCategory = true }, "JUDGE_BLOCKED"},
		{"already", func(c *VoteCheck) { c.AlreadyVotedEntry = true }, "ALREADY_VOTED"},
		{"category cap", func(c *VoteCheck) { c.VotesInCategory = 5 }, "VOTE_CAP_CATEGORY"},
		{"total cap", func(c *VoteCheck) { c.VotesTotal = 10 }, "VOTE_CAP_TOTAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mut(&c)
			got := CheckVote(c)
			if assert.NotNil(t, got) {
				assert.Equal(t, tc.code, got.Code)
			}
		})
	}
}

func TestTeamMemberIdentifiers(t *testing.T) {
	raw := json.RawMessage(`[
		{"name":"A","userId":"u-1","email":"a@example.com"},
		{"name":"B","user_id":"u-2"},
		{"name":"C","id":"u-3"},
		{"name":"D","email":"D@Example.com"}
	]`)
	assert.Equal(t, []string{"u-1", "u-2", "u-3"}, TeamMemberIDs(raw))
	assert.Equal(t, []string{"a@example.com", "D@Example.com"}, TeamMemberEmails(raw))
	assert.Nil(t, TeamMemberIDs(nil))
	assert.Nil(t, TeamMemberIDs(json.RawMessage(`not json`)))
}
