package composer

import (
	"math"
	"strings"
)

func estimateBudget(brief Brief, plan Suggestion, totalTravelMinutes int) BudgetEstimate {
	people := int64(brief.GroupSize)
	if people < 1 {
		people = 1
	}
	days := int64(brief.Days)
	nights := days - 1
	if nights < 0 {
		nights = 0
	}

	breakdown := map[string]int64{
		"Transport":         0,
		"Accommodation":     nights * people * bandNightlyKES(brief.BudgetBand),
		"Food":              days * people * bandFoodKES(brief.BudgetBand),
		"Park & entry fees": 0,
		"Activities":        0,
		"Shopping":          0,
		"Conservation":      0,
		"Other":             0,
	}

	km := float64(totalTravelMinutes) / 60.0 * 40.0
	perKm, perDay := transportRateKES(brief.Transport)
	breakdown["Transport"] = int64(math.Round(km))*perKm + days*perDay

	activityStops := 0
	shopping := false
	conservation := false
	for _, day := range plan.Days {
		for _, stop := range day.Stops {
			if stop.FeeAmountKes != nil {
				breakdown["Park & entry fees"] += *stop.FeeAmountKes * people
			} else {
				breakdown["Park & entry fees"] += defaultFeeKES(stop.Category) * people
			}
			switch stop.Category {
			case "Activity", "Hiking", "Beach", "Photography":
				activityStops++
			case "Shopping":
				shopping = true
			}
			if stop.Category == "Conservation" {
				conservation = true
			}
			for _, interest := range stop.Interests {
				if strings.EqualFold(interest, "Conservation") {
					conservation = true
				}
			}
		}
	}
	breakdown["Activities"] = int64(activityStops) * 800 * people
	if shopping {
		breakdown["Shopping"] = 500 * people
	}
	if conservation {
		breakdown["Conservation"] = 500 * people
	}

	subtotal := int64(0)
	for _, v := range breakdown {
		subtotal += v
	}
	breakdown["Other"] = int64(math.Round(float64(subtotal) * 0.05))
	total := subtotal + breakdown["Other"]
	perPerson := int64(0)
	if people > 0 {
		perPerson = total / people
	}

	return BudgetEstimate{
		Currency:     "KES",
		Estimated:    true,
		Note:         "Composer estimate. Transport, food, accommodation, and activity costs are band estimates, not live quotes.",
		TotalKes:     total,
		PerPersonKes: perPerson,
		Breakdown:    breakdown,
	}
}

func defaultFeeKES(category string) int64 {
	switch category {
	case "Wildlife":
		return 1500
	case "Conservation":
		return 1000
	case "Activity", "Hiking":
		return 1000
	case "Culture":
		return 800
	case "Food":
		return 1000
	case "Beach":
		return 0
	case "Shopping":
		return 500
	default:
		return 500
	}
}
