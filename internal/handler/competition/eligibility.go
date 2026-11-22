package competition

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// MaxCategoriesPerEntrant is the rulebook limit. The DB enforces one entry
	// per category; this cap needs entrant/team identity and lives in the API.
	MaxCategoriesPerEntrant = 2
)

// VoteError is a machine-readable reason a vote was rejected.
type VoteError struct {
	Code    string
	Message string
}

func (e *VoteError) Error() string { return e.Message }

// VoteCheck carries every precondition for casting one vote. The handler fills
// it from the database so the decision itself stays deterministic and testable.
type VoteCheck struct {
	EditionStatus     string
	PublicVoteMode    string
	VotingOpensAt     *time.Time
	VotingClosesAt    *time.Time
	Now               time.Time
	AccountCreatedAt  time.Time
	EmailVerified     bool
	IsSelf            bool
	IsTeamMember      bool
	IsJudgeInCategory bool
	AlreadyVotedEntry bool
	VotesInCategory   int
	VotesTotal        int
	MaxPerCategory    int
	MaxTotal          int
}

// CheckVote returns the first reason a vote is not allowed. Order is deliberate:
// window and account gates come before identity and cap checks so a voter who is
// not yet eligible never learns the entry-level rules.
func CheckVote(c VoteCheck) *VoteError {
	now := c.Now
	if now.IsZero() {
		now = time.Now()
	}
	if c.PublicVoteMode == VoteModeNone {
		return &VoteError{Code: "VOTING_DISABLED", Message: "public voting is not enabled for this edition"}
	}
	if !votingStatuses[c.EditionStatus] {
		return &VoteError{Code: "VOTING_CLOSED", Message: "voting is not open for this edition"}
	}
	if c.VotingOpensAt == nil || c.VotingClosesAt == nil {
		return &VoteError{Code: "VOTING_CLOSED", Message: "voting window is not configured"}
	}
	if now.Before(*c.VotingOpensAt) {
		return &VoteError{Code: "VOTING_NOT_OPEN", Message: "voting has not opened yet"}
	}
	if now.After(*c.VotingClosesAt) {
		return &VoteError{Code: "VOTING_CLOSED", Message: "voting has closed"}
	}
	if !c.EmailVerified {
		return &VoteError{Code: "EMAIL_UNVERIFIED", Message: "verify your email before voting"}
	}
	if !c.AccountCreatedAt.Before(*c.VotingOpensAt) {
		return &VoteError{Code: "ACCOUNT_TOO_NEW", Message: "your account must have existed before voting opened"}
	}
	if c.IsSelf {
		return &VoteError{Code: "SELF_VOTE", Message: "you cannot vote for your own entry"}
	}
	if c.IsTeamMember {
		return &VoteError{Code: "TEAM_VOTE", Message: "you cannot vote for a team entry you are part of"}
	}
	if c.IsJudgeInCategory {
		return &VoteError{Code: "JUDGE_BLOCKED", Message: "judges cannot vote in categories they judge"}
	}
	if c.AlreadyVotedEntry {
		return &VoteError{Code: "ALREADY_VOTED", Message: "you have already voted for this entry"}
	}
	perCategory := c.MaxPerCategory
	if perCategory <= 0 {
		perCategory = VoteCapPerCategory
	}
	total := c.MaxTotal
	if total <= 0 {
		total = VoteCapTotal
	}
	if c.VotesInCategory >= perCategory {
		return &VoteError{Code: "VOTE_CAP_CATEGORY", Message: fmt.Sprintf("you can vote for at most %d entries in a category", perCategory)}
	}
	if c.VotesTotal >= total {
		return &VoteError{Code: "VOTE_CAP_TOTAL", Message: fmt.Sprintf("you can cast at most %d votes in total", total)}
	}
	return nil
}

// ValidateAgeRange accepts any configured range. It never assumes a legal band.
func ValidateAgeRange(minAge, maxAge *int) error {
	if minAge != nil && *minAge < 0 {
		return fmt.Errorf("minAge must not be negative")
	}
	if maxAge != nil && *maxAge < 0 {
		return fmt.Errorf("maxAge must not be negative")
	}
	if minAge != nil && maxAge != nil && *minAge > *maxAge {
		return fmt.Errorf("minAge must not exceed maxAge")
	}
	return nil
}

// AgeWithinRange applies the configured edition band. Nil bounds are open.
func AgeWithinRange(age int, minAge, maxAge *int) bool {
	if minAge != nil && age < *minAge {
		return false
	}
	if maxAge != nil && age > *maxAge {
		return false
	}
	return true
}

// teamMemberRef is deliberately permissive: repository submissions store
// team_members as display JSON, while competition entries may carry user ids or
// emails. Vote blocking uses whichever identifiers are present.
type teamMemberRef struct {
	UserID  string `json:"userId"`
	UserID2 string `json:"user_id"`
	ID      string `json:"id"`
	Email   string `json:"email"`
}

// TeamMemberIDs extracts user ids from a team_members JSON value.
func TeamMemberIDs(raw json.RawMessage) []string {
	return collectTeamMembers(raw, func(ref teamMemberRef) string {
		if ref.UserID != "" {
			return ref.UserID
		}
		if ref.UserID2 != "" {
			return ref.UserID2
		}
		return ref.ID
	})
}

// TeamMemberEmails extracts emails from a team_members JSON value.
func TeamMemberEmails(raw json.RawMessage) []string {
	return collectTeamMembers(raw, func(ref teamMemberRef) string { return ref.Email })
}

func collectTeamMembers(raw json.RawMessage, pick func(teamMemberRef) string) []string {
	if len(raw) == 0 {
		return nil
	}
	var refs []teamMemberRef
	if err := json.Unmarshal(raw, &refs); err != nil {
		// Some legacy rows store a single object or a list of strings.
		var one teamMemberRef
		if err := json.Unmarshal(raw, &one); err == nil {
			refs = []teamMemberRef{one}
		}
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if v := strings.TrimSpace(pick(ref)); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
