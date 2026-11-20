package discovery

import (
	"os"
	"strconv"
)

// DefaultDailyRequestBudget keeps discovery inside the free-tier comfort zone
// until the composer's usage pattern is measured.
const DefaultDailyRequestBudget = 50

// DailyRequestBudgetFromEnv reads PLACES_DISCOVERY_DAILY_BUDGET, falling back
// to DefaultDailyRequestBudget when unset or invalid.
func DailyRequestBudgetFromEnv() int {
	raw := os.Getenv("PLACES_DISCOVERY_DAILY_BUDGET")
	if raw == "" {
		return DefaultDailyRequestBudget
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return DefaultDailyRequestBudget
	}
	return n
}
