package expo

import (
	"errors"
	"math"
	"sort"
)

var budgetCategories = []string{
	"Transport", "Accommodation", "Food", "Park & entry fees",
	"Activities", "Shopping", "Conservation", "Other",
}

var budgetCurrencies = []string{"KES", "USD", "EUR", "GBP", "TZS", "UGX", "ZAR"}

var (
	errInvalidSplit   = errors.New("invalid split")
	errSplitMismatch  = errors.New("split does not match the expense total")
	errDuplicateShare = errors.New("a participant appears more than once")
	errNoParticipants = errors.New("at least one participant is required")
)

type shareInput struct {
	ParticipantID string  `json:"participantId"`
	AmountKes     int64   `json:"amountKes,omitempty"`
	Percent       float64 `json:"percent,omitempty"`
}

type splitInput struct {
	Mode           string       `json:"mode"`
	ParticipantIDs []string     `json:"participantIds,omitempty"`
	Shares         []shareInput `json:"shares,omitempty"`
}

func isValidBudgetCategory(category string) bool {
	for _, c := range budgetCategories {
		if c == category {
			return true
		}
	}
	return false
}

func isValidCurrency(currency string) bool {
	for _, c := range budgetCurrencies {
		if c == currency {
			return true
		}
	}
	return false
}

func roundKES(originalAmount, fxRate float64) int64 {
	return int64(math.Round(originalAmount * fxRate))
}

func splitEqual(amountKes int64, participantIDs []string) (map[string]int64, error) {
	if amountKes <= 0 || len(participantIDs) == 0 {
		return nil, errNoParticipants
	}
	seen := make(map[string]bool, len(participantIDs))
	for _, id := range participantIDs {
		if id == "" || seen[id] {
			return nil, errDuplicateShare
		}
		seen[id] = true
	}
	n := int64(len(participantIDs))
	base := amountKes / n
	remainder := amountKes % n
	out := make(map[string]int64, len(participantIDs))
	for i, id := range participantIDs {
		share := base
		if int64(i) < remainder {
			share++
		}
		out[id] = share
	}
	return out, nil
}

func splitCustom(amountKes int64, shares []shareInput) (map[string]int64, map[string]float64, error) {
	if amountKes <= 0 || len(shares) == 0 {
		return nil, nil, errNoParticipants
	}
	out := make(map[string]int64, len(shares))
	percents := make(map[string]float64, len(shares))
	var sum int64
	for _, s := range shares {
		if s.ParticipantID == "" || s.AmountKes < 0 {
			return nil, nil, errInvalidSplit
		}
		if _, ok := out[s.ParticipantID]; ok {
			return nil, nil, errDuplicateShare
		}
		out[s.ParticipantID] = s.AmountKes
		sum += s.AmountKes
	}
	if sum != amountKes {
		return nil, nil, errSplitMismatch
	}
	_ = percents
	return out, nil, nil
}

func splitPercent(amountKes int64, shares []shareInput) (map[string]int64, map[string]float64, error) {
	if amountKes <= 0 || len(shares) == 0 {
		return nil, nil, errNoParticipants
	}
	type pctShare struct {
		id      string
		percent float64
		bps     int64
		base    int64
		rem     int64
		index   int
	}
	items := make([]pctShare, 0, len(shares))
	seen := make(map[string]bool, len(shares))
	var totalBps int64
	for i, s := range shares {
		if s.ParticipantID == "" || s.Percent < 0 {
			return nil, nil, errInvalidSplit
		}
		if seen[s.ParticipantID] {
			return nil, nil, errDuplicateShare
		}
		seen[s.ParticipantID] = true
		bps := int64(math.Round(s.Percent * 100))
		totalBps += bps
		base := amountKes * bps / 10000
		rem := (amountKes * bps) % 10000
		items = append(items, pctShare{id: s.ParticipantID, percent: s.Percent, bps: bps, base: base, rem: rem, index: i})
	}
	if totalBps != 10000 {
		return nil, nil, errSplitMismatch
	}

	out := make(map[string]int64, len(items))
	percents := make(map[string]float64, len(items))
	var distributed int64
	for _, item := range items {
		out[item.id] = item.base
		percents[item.id] = item.percent
		distributed += item.base
	}
	remainder := amountKes - distributed

	order := make([]pctShare, len(items))
	copy(order, items)
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].rem == order[j].rem {
			return order[i].index < order[j].index
		}
		return order[i].rem > order[j].rem
	})
	for i := int64(0); i < remainder; i++ {
		out[order[i%int64(len(order))].id]++
	}
	return out, percents, nil
}

type transfer struct {
	FromID string `json:"fromParticipantId"`
	ToID   string `json:"toParticipantId"`
	Amount int64  `json:"amountKes"`
}

// suggestTransfers matches debtors to creditors greedily. Input balances are
// integer KES; negative means the participant owes money.
func suggestTransfers(balances map[string]int64, order []string) []transfer {
	type balance struct {
		id     string
		amount int64
		index  int
	}
	index := make(map[string]int, len(order))
	for i, id := range order {
		index[id] = i
	}
	var debtors, creditors []balance
	for id, amount := range balances {
		if amount < 0 {
			debtors = append(debtors, balance{id: id, amount: -amount, index: index[id]})
		} else if amount > 0 {
			creditors = append(creditors, balance{id: id, amount: amount, index: index[id]})
		}
	}
	sort.Slice(debtors, func(i, j int) bool {
		if debtors[i].amount == debtors[j].amount {
			return debtors[i].index < debtors[j].index
		}
		return debtors[i].amount > debtors[j].amount
	})
	sort.Slice(creditors, func(i, j int) bool {
		if creditors[i].amount == creditors[j].amount {
			return creditors[i].index < creditors[j].index
		}
		return creditors[i].amount > creditors[j].amount
	})

	out := make([]transfer, 0)
	i, j := 0, 0
	for i < len(debtors) && j < len(creditors) {
		amount := debtors[i].amount
		if creditors[j].amount < amount {
			amount = creditors[j].amount
		}
		if amount > 0 {
			out = append(out, transfer{FromID: debtors[i].id, ToID: creditors[j].id, Amount: amount})
		}
		debtors[i].amount -= amount
		creditors[j].amount -= amount
		if debtors[i].amount == 0 {
			i++
		}
		if creditors[j].amount == 0 {
			j++
		}
	}
	return out
}
