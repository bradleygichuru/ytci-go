package composer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
)

func (e *Engine) buildPlan(
	brief Brief,
	pool []catalog.ComposerCandidate,
	origin catalog.County,
	coverage []catalog.ComposerCountyCoverage,
	profile Profile,
	inputHash string,
	matrix TravelMatrix,
	weatherByDate map[string]*DayWeather,
	events []Event,
	requireCurated bool,
) Suggestion {
	weights := profileWeights[profile]
	minStops, maxStops, maxTravel := paceLimits(brief.Pace)
	seed := seedFor(inputHash, profile)
	plan := Suggestion{
		ID:       shortHash(seed + ":plan"),
		Profile:  profile,
		Status:   "ok",
		Seed:     seed,
		Days:     make([]Day, 0, brief.Days),
		Warnings: make([]Warning, 0),
	}

	if len(matrix.Minutes) != len(pool)+1 {
		points := make([]Point, 0, len(pool)+1)
		op := originPoint(brief, origin, pool)
		points = append(points, op)
		for _, c := range pool {
			points = append(points, Point{Latitude: c.Latitude, Longitude: c.Longitude})
		}
		matrix, _ = StraightLineRouter{}.Matrix(nil, points, brief.Transport)
	}

	originIndex := 0
	current := originIndex
	used := map[string]bool{}
	usedCategories := map[string]bool{}
	usedCounties := map[string]bool{}
	mustRemaining := map[string]bool{}
	for _, m := range brief.MustInclude {
		mustRemaining[m] = true
	}
	curatedAvailable := requireCurated
	indexByKey := make(map[string]int, len(pool)*2)
	for i, c := range pool {
		indexByKey[candidateKey(c)] = i + 1
		indexByKey[c.Name] = i + 1
		if c.Slug != nil {
			indexByKey[*c.Slug] = i + 1
		}
	}

	startDate := e.Now().UTC()
	if brief.StartDate != nil {
		if parsed, err := time.Parse("2006-01-02", *brief.StartDate); err == nil {
			startDate = parsed
		}
	}

	totalTravel := 0
	totalStops := 0
	for day := 1; day <= brief.Days; day++ {
		dayDate := startDate.AddDate(0, 0, day-1)
		dayPlan := Day{
			Day: day, Date: dayDate.Format("2006-01-02"),
			Stops: make([]Stop, 0), Weather: weatherByDate[dayDate.Format("2006-01-02")],
		}
		currentTime := 8 * 60
		lunchUsed := false
		dayTravel := 0
		unverifiedToday := 0

		for len(dayPlan.Stops) < maxStops {
			best := -1
			bestScore := -1.0
			bestTravel := 0
			_ = bestTravel
			bestDuration := 0
			bestStart := 0
			for i, c := range pool {
				key := candidateKey(c)
				if used[key] {
					continue
				}
				travel := matrix.Minutes[current][i+1]
				if travel+dayTravel > maxTravel {
					if len(dayPlan.Stops) > 0 || travel > 480 {
						continue
					}
				}
				duration := visitMinutes(c)
				start := currentTime + travel
				if !lunchUsed && start >= 12*60 {
					start += 60
					lunchUsed = true
				}
				open, close := openingWindow(c)
				if start < open {
					start = open
				}
				if start+duration > close {
					if start+30 > close {
						continue
					}
					duration = close - start
				}
				if start+duration > 18*60 {
					if len(dayPlan.Stops) > 0 {
						continue
					}
					duration = 18*60 - start
					if duration < 30 {
						continue
					}
				}
				if c.Provenance != catalog.ProvenanceCurated && curatedAvailable {
					maxUnverified := (len(dayPlan.Stops) + 1) / 2
					if maxUnverified < 1 {
						maxUnverified = 1
					}
					if unverifiedToday >= maxUnverified {
						continue
					}
				}
				score := weights.total(
					interestScore(brief, c),
					1-float64(travel)/float64(maxTravel),
					qualityScore(c),
					candidateBudgetScore(brief, c),
					weatherScore(dayPlan.Weather),
					diversityCandidate(c, usedCategories, usedCounties, len(dayPlan.Stops)),
					eventScoreFor(c, events, dayDate),
				)
				if isMustInclude(c, mustRemaining) {
					score += 10
				}
				if betterChoice(score, c, travel, bestScore, best, bestTravel, pool, seed) {
					best = i + 1
					bestScore = score
					bestTravel = travel
					bestDuration = duration
					bestStart = start
				}
			}
			if best <= 0 {
				break
			}
			c := pool[best-1]
			stop := makeStop(c, bestStart, bestDuration, bestTravel, matrix.Estimated, dayPlan.Weather, events, dayDate)
			dayPlan.Stops = append(dayPlan.Stops, stop)
			if bestTravel > maxTravel {
				dayPlan.Warnings = appendWarning(dayPlan.Warnings, Warning{
					Code: "TRANSFER_DAY", Severity: "caution",
					Message: "This leg exceeds the pace travel cap and is flagged as a transfer.",
					Params:  map[string]any{"travelMinutes": bestTravel, "capMinutes": maxTravel},
				})
			}
			if bestTravel > 480 {
				dayPlan.Warnings = appendWarning(dayPlan.Warnings, Warning{
					Code: "LONG_TRANSFER", Severity: "warning",
					Message: "This leg exceeds the recommended 8-hour transfer limit.",
					Params:  map[string]any{"travelMinutes": bestTravel},
				})
			}
			used[candidateKey(c)] = true
			usedCategories[c.Category] = true
			usedCounties[c.CountyCode] = true
			deleteMustInclude(c, mustRemaining)
			current = best
			currentTime = bestStart + bestDuration
			dayTravel += bestTravel
			totalTravel += bestTravel
			totalStops++
			if c.Provenance != catalog.ProvenanceCurated {
				unverifiedToday++
			}
		}

		if len(dayPlan.Stops) == 0 && day < brief.Days {
			bestBase := -1
			bestBaseScore := -1.0
			bestBaseTravel := 0
			for i, c := range pool {
				if used[candidateKey(c)] {
					continue
				}
				travel := matrix.Minutes[current][i+1]
				if travel > 600 {
					continue
				}
				score := 0.6*interestScore(brief, c) + 0.4*qualityScore(c) - 0.001*float64(travel)
				if bestBase <= 0 || score > bestBaseScore || (score == bestBaseScore && travel < bestBaseTravel) {
					bestBase = i + 1
					bestBaseScore = score
					bestBaseTravel = travel
				}
			}
			if bestBase > 0 {
				c := pool[bestBase-1]
				dayTravel += bestBaseTravel
				totalTravel += bestBaseTravel
				current = bestBase
				dayPlan.Base = c.County
				dayPlan.Warnings = appendWarning(dayPlan.Warnings, Warning{
					Code: "TRANSFER_DAY", Severity: "caution",
					Message: "Travel day to the next overnight base; no visit is scheduled.",
					Params:  map[string]any{"base": c.County, "travelMinutes": bestBaseTravel},
				})
			}
		}

		if day == brief.Days && brief.ReturnToOrigin != nil && *brief.ReturnToOrigin {
			returnTravel := matrix.Minutes[current][0]
			dayTravel += returnTravel
			totalTravel += returnTravel
			if returnTravel > maxTravel {
				dayPlan.Warnings = appendWarning(dayPlan.Warnings, Warning{
					Code: "TRANSFER_DAY", Severity: "caution",
					Message: "The final return is a long transfer day.",
					Params:  map[string]any{"travelMinutes": returnTravel},
				})
			}
			if returnTravel > 480 {
				dayPlan.Warnings = appendWarning(dayPlan.Warnings, Warning{
					Code: "LONG_TRANSFER", Severity: "warning",
					Message: "The final return exceeds the recommended 8-hour transfer limit.",
					Params:  map[string]any{"travelMinutes": returnTravel},
				})
			}
		}

		if len(dayPlan.Stops) == 0 {
			dayPlan.Warnings = appendWarning(dayPlan.Warnings, Warning{
				Code: "REST_OR_TRANSFER_DAY", Severity: "info",
				Message: "No stops fit this day; it is left as a rest or transfer day.",
			})
		} else if len(dayPlan.Stops) < minStops {
			dayPlan.Warnings = appendWarning(dayPlan.Warnings, Warning{
				Code: "PARTIAL_DAY", Severity: "info",
				Message: "This day has fewer stops than the selected pace.",
				Params:  map[string]any{"stops": len(dayPlan.Stops), "pace": brief.Pace},
			})
		}
		dayPlan.TravelMinutes = dayTravel
		if len(dayPlan.Stops) > 0 {
			dayPlan.Base = dayPlan.Stops[len(dayPlan.Stops)-1].County
		} else if dayPlan.Base == "" {
			dayPlan.Base = "Transit"
		}
		plan.Days = append(plan.Days, dayPlan)
	}

	if len(mustRemaining) > 0 {
		plan.Status = "blocked"
		plan.Warnings = appendWarning(plan.Warnings, Warning{
			Code: "MUST_INCLUDE_UNREACHABLE", Severity: "warning",
			Message: "One or more must-include places could not fit the trip constraints.",
			Params:  map[string]any{"mustInclude": keys(mustRemaining)},
		})
	}
	if curatedAvailable && planCuratedCount(plan) == 0 {
		plan.Status = "blocked"
		plan.Warnings = appendWarning(plan.Warnings, Warning{
			Code: "CURATED_REQUIRED", Severity: "warning",
			Message: "Curated candidates exist but none fit this plan.",
		})
	}
	if totalStops == 0 {
		plan.Status = "blocked"
		plan.Warnings = appendWarning(plan.Warnings, Warning{
			Code: "NO_FEASIBLE_CANDIDATES", Severity: "warning",
			Message: "No candidate fits the trip constraints.",
		})
	}

	plan.TotalTravelMinutes = totalTravel
	plan.EstimatedBudget = estimateBudget(brief, plan, totalTravel)
	if brief.BudgetPerPersonKes != nil {
		limit := *brief.BudgetPerPersonKes * int64(brief.GroupSize)
		if plan.EstimatedBudget.TotalKes > limit {
			plan.Status = "blocked"
			plan.Warnings = appendWarning(plan.Warnings, Warning{
				Code: "BUDGET_CAP_EXCEEDED", Severity: "warning",
				Message: "The estimated trip cost exceeds the per-person budget cap.",
				Params: map[string]any{
					"estimatedKes": plan.EstimatedBudget.TotalKes,
					"limitKes":     limit,
				},
			})
		}
	}

	plan.Score = weights.total(
		planInterestCoverage(brief, plan),
		planTravelEfficiency(plan, maxTravel),
		planQuality(plan, pool),
		planBudgetFit(brief, plan.EstimatedBudget),
		planWeather(plan),
		planDiversity(plan),
		planEvent(plan, events),
	)
	plan.MatchLabel = matchLabel(plan.Score)
	plan.CuratedCount = planCuratedCount(plan)
	plan.UnverifiedCount = totalStops - plan.CuratedCount
	if plan.UnverifiedCount > 0 {
		plan.Warnings = appendWarning(plan.Warnings, Warning{
			Code: "UNVERIFIED_PLAN", Severity: "caution",
			Message: "This plan uses unverified Google Places candidates. Fees, hours, and accessibility are not program-verified.",
		})
	}
	plan.Coverage = coverageForPlans(coverage, []Suggestion{plan}, origin.Code)
	plan.Title = titleFor(brief, profile, origin)
	plan.Summary = summaryFor(plan)
	plan.Warnings = appendWarning(plan.Warnings, Warning{
		Code: "BUDGET_ESTIMATE", Severity: "info",
		Message: "Costs are composer estimates, not live quotes.",
	})
	for _, day := range plan.Days {
		for _, warning := range day.Warnings {
			plan.Warnings = appendWarning(plan.Warnings, warning)
		}
		for _, stop := range day.Stops {
			for _, warning := range stop.Warnings {
				plan.Warnings = appendWarning(plan.Warnings, warning)
			}
		}
	}
	if plan.Status == "ok" && planHasEmptyDay(plan) {
		plan.Status = "limited"
	}
	return plan
}

func betterChoice(score float64, c catalog.ComposerCandidate, travel int, bestScore float64, best, bestTravel int, pool []catalog.ComposerCandidate, seed string) bool {
	if best <= 0 {
		return true
	}
	if score != bestScore {
		return score > bestScore
	}
	bestCandidate := pool[best-1]
	if (c.Provenance == catalog.ProvenanceCurated) != (bestCandidate.Provenance == catalog.ProvenanceCurated) {
		return c.Provenance == catalog.ProvenanceCurated
	}
	if travel != bestTravel {
		return travel < bestTravel
	}
	return stableHash(seed+":"+candidateKey(c)) < stableHash(seed+":"+candidateKey(bestCandidate))
}

func makeStop(c catalog.ComposerCandidate, start, duration, travel int, estimated bool, weather *DayWeather, events []Event, day time.Time) Stop {
	feeKes, feeWarning := feeForStop(c)
	openStatus := "unverified"
	if c.OpeningHoursStatus != nil {
		openStatus = *c.OpeningHoursStatus
	}
	stop := Stop{
		DestinationID:             c.DestinationID,
		PlaceID:                   c.PlaceID,
		Name:                      c.Name,
		Provenance:                c.Provenance,
		Category:                  c.Category,
		Interests:                 c.Interests,
		CountyCode:                c.CountyCode,
		County:                    c.County,
		Latitude:                  c.Latitude,
		Longitude:                 c.Longitude,
		Arrive:                    formatClock(start),
		Depart:                    formatClock(start + duration),
		DurationMinutes:           duration,
		TravelFromPreviousMinutes: travel,
		EstimatedTravel:           estimated,
		FeeStatus:                 c.FeeStatus,
		FeeAmountKes:              feeKes,
		OpeningHoursStatus:        &openStatus,
		Rationale:                 make([]Reason, 0),
		Warnings:                  make([]Warning, 0),
	}
	if c.Provenance == catalog.ProvenanceCurated {
		stop.Rationale = append(stop.Rationale, Reason{
			Code: "CURATED_PROVENANCE", Text: "YTCI-verified destination.",
		})
		if feeKes != nil && (c.FeeStatus != nil && (*c.FeeStatus == "verified" || *c.FeeStatus == "free")) {
			stop.Rationale = append(stop.Rationale, Reason{
				Code: "FEE_VERIFIED", Text: "Entry fee is program data.",
				Params: map[string]any{"feeKes": *feeKes},
			})
		}
	} else {
		stop.Rationale = append(stop.Rationale, Reason{
			Code: "UNVERIFIED_PROVENANCE", Text: "Unverified Google Places candidate.",
		})
		stop.Warnings = appendWarning(stop.Warnings, Warning{
			Code: "UNVERIFIED_STOP", Severity: "caution",
			Message: "This stop is an unverified Google Places candidate.",
		})
	}
	if travel > 0 {
		stop.Rationale = append(stop.Rationale, Reason{
			Code: "TRAVEL_TIME", Text: "Travel time from the previous stop.",
			Params: map[string]any{"minutes": travel, "estimated": estimated},
		})
	}
	if feeWarning != nil {
		stop.Warnings = appendWarning(stop.Warnings, *feeWarning)
	}
	if openStatus != "verified" {
		stop.Warnings = appendWarning(stop.Warnings, Warning{
			Code: "HOURS_UNVERIFIED", Severity: "info",
			Message: "Opening hours are not verified; the stop is scheduled in daylight.",
		})
	} else {
		stop.Rationale = append(stop.Rationale, Reason{
			Code: "HOURS_VERIFIED", Text: "Opening hours are verified.",
		})
	}
	if interest := firstMatchedInterest(c); interest != "" {
		stop.Rationale = append(stop.Rationale, Reason{
			Code: "INTEREST_MATCH", Text: "Matches a trip interest.",
			Params: map[string]any{"interest": interest},
		})
	}
	if weather != nil {
		stop.Rationale = append(stop.Rationale, Reason{
			Code: "WEATHER", Text: "Weather signal for this day.",
			Params: map[string]any{
				"precipProbability": weather.PrecipProbability,
				"tempMaxC":          weather.TempMaxC,
				"source":            weather.Source,
			},
		})
	}
	for _, event := range events {
		if strings.EqualFold(event.County, c.County) && eventCoversDay(event, day) {
			stop.Rationale = append(stop.Rationale, Reason{
				Code: "EVENT", Text: "A scheduled program event is in this county.",
				Params: map[string]any{"title": event.Title, "date": event.Date},
			})
			break
		}
	}
	return stop
}

func feeForStop(c catalog.ComposerCandidate) (*int64, *Warning) {
	if c.Provenance != catalog.ProvenanceCurated {
		return nil, &Warning{
			Code: "FEE_UNVERIFIED", Severity: "info",
			Message: "Entry fee is not program-verified; the budget uses a category estimate.",
		}
	}
	if c.FeeStatus == nil {
		return nil, &Warning{
			Code: "FEE_UNVERIFIED", Severity: "info",
			Message: "Entry fee is unavailable; the budget uses a category estimate.",
		}
	}
	switch *c.FeeStatus {
	case "free":
		zero := int64(0)
		return &zero, nil
	case "verified":
		if c.FeeAmount == nil {
			return nil, &Warning{Code: "FEE_UNVERIFIED", Severity: "info", Message: "Entry fee amount is unavailable."}
		}
		amount := feeToKES(int64(*c.FeeAmount), c.FeeCurrency)
		return &amount, nil
	default:
		if c.FeeAmount != nil {
			amount := feeToKES(int64(*c.FeeAmount), c.FeeCurrency)
			return &amount, &Warning{
				Code: "FEE_REPORTED", Severity: "info",
				Message: "Entry fee is reported but not program-verified.",
			}
		}
		return nil, &Warning{Code: "FEE_UNVERIFIED", Severity: "info", Message: "Entry fee is not program-verified."}
	}
}

func feeToKES(amount int64, currency *string) int64 {
	if currency == nil {
		return amount
	}
	switch *currency {
	case "USD":
		return amount * 130
	case "EUR":
		return amount * 140
	case "GBP":
		return amount * 165
	default:
		return amount
	}
}

func visitMinutes(c catalog.ComposerCandidate) int {
	if c.Provenance != catalog.ProvenanceCurated || c.DurationMinMinutes == nil || c.DurationMaxMinutes == nil {
		return 60
	}
	mid := (*c.DurationMinMinutes + *c.DurationMaxMinutes) / 2
	if mid < 30 {
		mid = 30
	}
	if mid > 480 {
		mid = 480
	}
	return mid
}

func openingWindow(c catalog.ComposerCandidate) (int, int) {
	if c.OpeningHoursStatus == nil || *c.OpeningHoursStatus != "verified" {
		return 8 * 60, 17*60 + 30
	}
	if len(c.OpeningHours) > 0 {
		var hours struct {
			Daily        string `json:"daily"`
			MondaySunday string `json:"monday_sunday"`
		}
		if err := json.Unmarshal(c.OpeningHours, &hours); err == nil {
			window := hours.Daily
			if window == "" {
				window = hours.MondaySunday
			}
			if open, close, ok := parseWindow(window); ok {
				return open, close
			}
		}
	}
	return 8 * 60, 17*60 + 30
}

func parseWindow(window string) (int, int, bool) {
	parts := strings.Split(window, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	open, ok1 := parseClock(parts[0])
	close, ok2 := parseClock(parts[1])
	if !ok1 || !ok2 || close <= open {
		return 0, 0, false
	}
	return open, close, true
}

func parseClock(value string) (int, bool) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, false
	}
	var hour, minute int
	if _, err := fmt.Sscanf(parts[0], "%d", &hour); err != nil {
		return 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &minute); err != nil {
		return 0, false
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func formatClock(minutes int) string {
	if minutes < 0 {
		minutes = 0
	}
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

func candidateBudgetScore(brief Brief, c catalog.ComposerCandidate) float64 {
	feeKes, _ := feeForStop(c)
	var fee int64
	if feeKes != nil {
		fee = *feeKes
	}
	limit := bandDailyBudgetKES(brief.BudgetBand) * int64(brief.GroupSize)
	if limit <= 0 {
		return 0.5
	}
	return clamp01(1 - float64(fee)/float64(limit))
}

func diversityCandidate(c catalog.ComposerCandidate, categories, counties map[string]bool, stops int) float64 {
	if stops == 0 {
		return 1
	}
	score := 0.0
	if !categories[c.Category] {
		score += 0.6
	}
	if !counties[c.CountyCode] {
		score += 0.4
	}
	if score == 0 {
		return 0.2
	}
	return score
}

func isMustInclude(c catalog.ComposerCandidate, must map[string]bool) bool {
	if must[candidateKey(c)] || must[c.Name] {
		return true
	}
	return c.Slug != nil && must[*c.Slug]
}

func deleteMustInclude(c catalog.ComposerCandidate, must map[string]bool) {
	delete(must, candidateKey(c))
	delete(must, c.Name)
	if c.Slug != nil {
		delete(must, *c.Slug)
	}
}

func firstMatchedInterest(c catalog.ComposerCandidate) string {
	if len(c.Interests) > 0 {
		return c.Interests[0]
	}
	return c.Category
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func stableHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func titleFor(brief Brief, profile Profile, origin catalog.County) string {
	label := map[Profile]string{
		ProfileBudgetSmart:     "Budget-smart",
		ProfileBalanced:        "Balanced",
		ProfileExperienceFirst: "Experience-first",
	}[profile]
	return fmt.Sprintf("%s: %d days from %s", label, brief.Days, origin.Name)
}

func summaryFor(plan Suggestion) string {
	curated, unverified := plan.CuratedCount, plan.UnverifiedCount
	return fmt.Sprintf("%d stops across %d days, %dh %02dm travel, %d curated / %d unverified, estimated KES %d.",
		lenStops(plan), len(plan.Days), plan.TotalTravelMinutes/60, plan.TotalTravelMinutes%60,
		curated, unverified, plan.EstimatedBudget.TotalKes)
}

func lenStops(plan Suggestion) int {
	total := 0
	for _, day := range plan.Days {
		total += len(day.Stops)
	}
	return total
}

func planCuratedCount(plan Suggestion) int {
	count := 0
	for _, day := range plan.Days {
		for _, stop := range day.Stops {
			if stop.Provenance == catalog.ProvenanceCurated {
				count++
			}
		}
	}
	return count
}

func planHasEmptyDay(plan Suggestion) bool {
	for _, day := range plan.Days {
		if len(day.Stops) == 0 {
			return true
		}
	}
	return false
}

func planInterestCoverage(brief Brief, plan Suggestion) float64 {
	if len(brief.Interests) == 0 {
		return 0
	}
	covered := map[string]bool{}
	for _, day := range plan.Days {
		for _, stop := range day.Stops {
			for _, interest := range stop.Interests {
				covered[strings.ToLower(interest)] = true
			}
			covered[strings.ToLower(stop.Category)] = true
		}
	}
	matched := 0
	for _, interest := range brief.Interests {
		if covered[strings.ToLower(interest)] {
			matched++
		}
	}
	return float64(matched) / float64(len(brief.Interests))
}

func planTravelEfficiency(plan Suggestion, maxTravel int) float64 {
	if len(plan.Days) == 0 {
		return 0
	}
	totalPossible := maxTravel * len(plan.Days)
	if totalPossible <= 0 {
		return 0
	}
	return clamp01(1 - float64(plan.TotalTravelMinutes)/float64(totalPossible))
}

func planQuality(plan Suggestion, pool []catalog.ComposerCandidate) float64 {
	byKey := map[string]catalog.ComposerCandidate{}
	for _, c := range pool {
		byKey[candidateKey(c)] = c
	}
	total := 0.0
	count := 0
	for _, day := range plan.Days {
		for _, stop := range day.Stops {
			key := ""
			if stop.DestinationID != nil {
				key = *stop.DestinationID
			} else if stop.PlaceID != nil {
				key = *stop.PlaceID
			}
			if c, ok := byKey[key]; ok {
				total += qualityScore(c)
				count++
			}
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func planBudgetFit(brief Brief, budget BudgetEstimate) float64 {
	limit := bandDailyBudgetKES(brief.BudgetBand) * int64(brief.Days) * int64(brief.GroupSize)
	if brief.BudgetPerPersonKes != nil {
		limit = *brief.BudgetPerPersonKes * int64(brief.GroupSize)
	}
	if limit <= 0 {
		return 0.5
	}
	return clamp01(1 - float64(budget.TotalKes)/float64(limit))
}

func planWeather(plan Suggestion) float64 {
	total := 0.0
	count := 0
	for _, day := range plan.Days {
		if day.Weather != nil {
			total += weatherScore(day.Weather)
			count++
		}
	}
	if count == 0 {
		return 0.5
	}
	return total / float64(count)
}

func planDiversity(plan Suggestion) float64 {
	stops := 0
	categories := map[string]bool{}
	counties := map[string]bool{}
	for _, day := range plan.Days {
		for _, stop := range day.Stops {
			stops++
			categories[stop.Category] = true
			counties[stop.CountyCode] = true
		}
	}
	if stops == 0 {
		return 0
	}
	return 0.6*float64(len(categories))/float64(stops) + 0.4*float64(len(counties))/float64(stops)
}

func planEvent(plan Suggestion, events []Event) float64 {
	stops := 0
	matched := 0
	for _, day := range plan.Days {
		for _, stop := range day.Stops {
			stops++
			for _, event := range events {
				if strings.EqualFold(event.County, stop.County) && eventCoversDate(event, day.Date) {
					matched++
					break
				}
			}
		}
	}
	if stops == 0 {
		return 0
	}
	return float64(matched) / float64(stops)
}

func eventCoversDate(e Event, date string) bool {
	if e.Date == date {
		return true
	}
	return e.EndDate != nil && e.Date <= date && *e.EndDate >= date
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func originPoint(brief Brief, origin catalog.County, pool []catalog.ComposerCandidate) Point {
	if brief.Origin.Latitude != nil && brief.Origin.Longitude != nil {
		return Point{Latitude: *brief.Origin.Latitude, Longitude: *brief.Origin.Longitude}
	}
	for _, c := range pool {
		if c.CountyCode == origin.Code {
			return Point{Latitude: c.Latitude, Longitude: c.Longitude}
		}
	}
	for _, c := range pool {
		return Point{Latitude: c.Latitude, Longitude: c.Longitude}
	}
	return Point{}
}
