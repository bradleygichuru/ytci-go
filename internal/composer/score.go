package composer

import (
	"math"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
)

type scoreWeights struct {
	Interest  float64
	Travel    float64
	Quality   float64
	Budget    float64
	Weather   float64
	Diversity float64
	Event     float64
}

var profileWeights = map[Profile]scoreWeights{
	ProfileBudgetSmart: {
		Interest: 0.20, Travel: 0.25, Quality: 0.10, Budget: 0.30,
		Weather: 0.08, Diversity: 0.04, Event: 0.03,
	},
	ProfileBalanced: {
		Interest: 0.30, Travel: 0.20, Quality: 0.15, Budget: 0.15,
		Weather: 0.10, Diversity: 0.05, Event: 0.05,
	},
	ProfileExperienceFirst: {
		Interest: 0.35, Travel: 0.15, Quality: 0.20, Budget: 0.05,
		Weather: 0.10, Diversity: 0.08, Event: 0.07,
	},
}

func (w scoreWeights) total(interest, travel, quality, budget, weather, diversity, event float64) float64 {
	return w.Interest*clamp01(interest) +
		w.Travel*clamp01(travel) +
		w.Quality*clamp01(quality) +
		w.Budget*clamp01(budget) +
		w.Weather*clamp01(weather) +
		w.Diversity*clamp01(diversity) +
		w.Event*clamp01(event)
}

func qualityScore(c catalog.ComposerCandidate) float64 {
	if c.Provenance == catalog.ProvenanceCurated {
		return 1.0
	}
	if c.Rating == nil {
		return 0.40
	}
	reviews := 0
	if c.ReviewCount != nil {
		reviews = *c.ReviewCount
	}
	switch {
	case *c.Rating >= 4.5 && reviews >= 100:
		return 0.70
	case *c.Rating >= 4.0:
		return 0.55
	default:
		return 0.45
	}
}

func interestScore(brief Brief, c catalog.ComposerCandidate) float64 {
	if len(brief.Interests) == 0 {
		return 0
	}
	matched := 0
	for _, want := range brief.Interests {
		for _, tag := range c.Interests {
			if strings.EqualFold(want, tag) {
				matched++
				break
			}
		}
	}
	score := float64(matched) / float64(len(brief.Interests))
	// Primary category matching the brief is a stronger signal than a
	// secondary tag.
	for _, want := range brief.Interests {
		if strings.EqualFold(want, c.Category) {
			score = math.Min(1, score+0.15)
			break
		}
	}
	return score
}

func matchLabel(score float64) string {
	switch {
	case score >= 0.75:
		return "Strong"
	case score >= 0.55:
		return "Good"
	default:
		return "Stretch"
	}
}

func bandDailyBudgetKES(band string) int64 {
	switch band {
	case "budget":
		return 7000
	case "luxury":
		return 45000
	default:
		return 18000
	}
}

func bandNightlyKES(band string) int64 {
	switch band {
	case "budget":
		return 3000
	case "luxury":
		return 25000
	default:
		return 10000
	}
}

func bandFoodKES(band string) int64 {
	switch band {
	case "budget":
		return 1500
	case "luxury":
		return 9000
	default:
		return 4000
	}
}

func transportRateKES(mode string) (perKm int64, perDay int64) {
	switch mode {
	case "public":
		return 15, 500
	case "tour_van":
		return 70, 2000
	default:
		return 55, 1000
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func paceLimits(pace string) (minStops, maxStops, maxTravelMinutes int) {
	switch pace {
	case "relaxed":
		return 2, 3, 180
	case "packed":
		return 4, 5, 360
	default:
		return 3, 4, 270
	}
}

func weatherScore(w *DayWeather) float64 {
	if w == nil {
		return 0.5
	}
	score := 1.0
	if w.PrecipProbability != nil {
		score -= float64(*w.PrecipProbability) / 150.0
	}
	if w.TempMaxC != nil {
		if *w.TempMaxC > 34 {
			score -= 0.15
		}
		if *w.TempMaxC < 12 {
			score -= 0.15
		}
	}
	return clamp01(score)
}

func eventScoreFor(c catalog.ComposerCandidate, events []Event, day time.Time) float64 {
	for _, e := range events {
		if !strings.EqualFold(e.County, c.County) {
			continue
		}
		if !eventCoversDay(e, day) {
			continue
		}
		return 1.0
	}
	return 0.0
}

func eventCoversDay(e Event, day time.Time) bool {
	date := day.Format("2006-01-02")
	if e.Date == date {
		return true
	}
	if e.EndDate != nil && e.Date <= date && *e.EndDate >= date {
		return true
	}
	return false
}
