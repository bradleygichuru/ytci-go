package expo

import (
	"encoding/json"
	"net/http"
)

type CompanionFAQ struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

var companionFAQs = []CompanionFAQ{
	{Question: "Do I need a visa?", Answer: "Most visitors can get an eVisa online before travel."},
	{Question: "How do I get around?", Answer: "Matatus, domestic flights, and ride-hailing apps are the main options."},
	{Question: "When is the best time to visit?", Answer: "Peak season is June-October (dry)."},
	{Question: "Is it safe to travel?", Answer: "Kenya is generally safe. Stick to known destinations and follow local advice."},
	{Question: "What should I pack?", Answer: "Light clothing, a jacket, sunscreen, insect repellent, a hat, and walking shoes."},
}

func GetCompanion(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]any{
		"faqs": companionFAQs,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
