package competition

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
)

// Criteria is one judge's raw 1..10 rubric scores.
type Criteria struct {
	Relevance   int `json:"relevance"`
	Craft       int `json:"craft"`
	Originality int `json:"originality"`
	Impact      int `json:"impact"`
}

func validCriteria(c Criteria) bool {
	return c.Relevance >= 1 && c.Relevance <= 10 &&
		c.Craft >= 1 && c.Craft <= 10 &&
		c.Originality >= 1 && c.Originality <= 10 &&
		c.Impact >= 1 && c.Impact <= 10
}

// WeightedTotal turns the rubric into a 0..100 score. The rulebook weights are
// 25/30/25/20 on a 1..10 scale, so a perfect score is 100.
func WeightedTotal(c Criteria) float64 {
	total := (float64(c.Relevance)*RubricRelevanceWeight +
		float64(c.Craft)*RubricCraftWeight +
		float64(c.Originality)*RubricOriginalityWeight +
		float64(c.Impact)*RubricImpactWeight) / 10.0
	return round2(total)
}

// NormalizePublicScore expresses an entry's votes as a percentage of the
// category maximum. It is the normalization input for the future weighted mode;
// Route B exposes the raw votes as a separate People's Choice recognition and
// does not blend them into the award score.
func NormalizePublicScore(votes, categoryMax int) float64 {
	if categoryMax <= 0 || votes <= 0 {
		return 0
	}
	return round2(float64(votes) / float64(categoryMax) * 100)
}

// FinalScore applies the mode. Weighted is the only mode that mixes panel and
// public scores. people_choice_only and none award on the panel score alone.
func FinalScore(expert, public float64, mode string) float64 {
	if mode != VoteModeWeighted {
		return round2(expert)
	}
	return round2(expert*ExpertWeight + public*PublicWeight)
}

func WeightsFor(mode string) weights {
	if mode == VoteModeWeighted {
		return weights{Expert: ExpertWeight, Public: PublicWeight}
	}
	return weights{Expert: 1, Public: 0}
}

// ScoredEntry is the pure input to a ranked category.
type ScoredEntry struct {
	EntryID       string
	Title         string
	CategoryID    string
	CategoryKey   string
	CategoryLabel string
	CategorySort  int
	Expert        float64
	Craft         float64
	Originality   float64
	ScoreCount    int
	Votes         int
}

// RankCategory ranks by final score, then craft, then originality, then entry
// id. The rulebook's final panel-chair tie-break is a human decision and is
// surfaced through IsTie rather than silently invented.
func RankCategory(entries []ScoredEntry, mode string) []resultEntry {
	categoryMax := maxVotes(entries)
	rows := make([]resultEntry, 0, len(entries))
	for _, e := range entries {
		total := FinalScore(e.Expert, NormalizePublicScore(e.Votes, categoryMax), mode)
		publicScore := 0.0
		if mode == VoteModeWeighted {
			publicScore = NormalizePublicScore(e.Votes, categoryMax)
		}
		rows = append(rows, resultEntry{
			EntryID:     e.EntryID,
			Title:       e.Title,
			ExpertScore: round2(e.Expert),
			ScoreCount:  e.ScoreCount,
			PublicVotes: e.Votes,
			PublicScore: publicScore,
			FinalScore:  total,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].FinalScore != rows[j].FinalScore {
			return rows[i].FinalScore > rows[j].FinalScore
		}
		if craftOf(entries, rows[i].EntryID) != craftOf(entries, rows[j].EntryID) {
			return craftOf(entries, rows[i].EntryID) > craftOf(entries, rows[j].EntryID)
		}
		if originalityOf(entries, rows[i].EntryID) != originalityOf(entries, rows[j].EntryID) {
			return originalityOf(entries, rows[i].EntryID) > originalityOf(entries, rows[j].EntryID)
		}
		return rows[i].EntryID < rows[j].EntryID
	})
	for i := range rows {
		rows[i].Rank = i + 1
		if i > 0 && rows[i].FinalScore == rows[i-1].FinalScore {
			rows[i].IsTie = true
			rows[i-1].IsTie = true
		}
	}
	return rows
}

func craftOf(entries []ScoredEntry, id string) float64 {
	for _, e := range entries {
		if e.EntryID == id {
			return e.Craft
		}
	}
	return 0
}

func originalityOf(entries []ScoredEntry, id string) float64 {
	for _, e := range entries {
		if e.EntryID == id {
			return e.Originality
		}
	}
	return 0
}

func maxVotes(entries []ScoredEntry) int {
	max := 0
	for _, e := range entries {
		if e.Votes > max {
			max = e.Votes
		}
	}
	return max
}

// LeaderboardRows ranks raw vote totals. Used for the live board and for the
// separate People's Choice recognition; no expert score is involved.
func LeaderboardRows(entries []ScoredEntry) []leaderboardEntry {
	rows := make([]leaderboardEntry, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, leaderboardEntry{EntryID: e.EntryID, Title: e.Title, Votes: e.Votes})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Votes != rows[j].Votes {
			return rows[i].Votes > rows[j].Votes
		}
		return rows[i].EntryID < rows[j].EntryID
	})
	for i := range rows {
		rows[i].Rank = i + 1
	}
	return rows
}

// PeopleChoice returns the highest raw vote total across the edition. A tie is
// resolved deterministically by entry id; the panel can still override the
// award record explicitly.
func PeopleChoice(entries []ScoredEntry) *peopleChoiceResult {
	if len(entries) == 0 {
		return nil
	}
	rows := LeaderboardRows(entries)
	top := rows[0]
	if top.Votes <= 0 {
		return nil
	}
	return &peopleChoiceResult{EntryID: top.EntryID, Title: top.Title, Votes: top.Votes}
}

// LedgerHash is the stable fingerprint of the vote rows included in a snapshot.
// The vote set is sorted so the hash depends on the ledger, not query order.
func LedgerHash(voteIDs []string) string {
	ids := append([]string(nil), voteIDs...)
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
