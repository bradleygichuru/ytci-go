package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

type CompanionHandler struct {
	pool *pgxpool.Pool
}

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

func NewCompanionHandler(pool *pgxpool.Pool) *CompanionHandler {
	return &CompanionHandler{pool: pool}
}

func (h *CompanionHandler) GetCompanion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var destinations json.RawMessage
	err := h.pool.QueryRow(ctx,
		`SELECT COALESCE(json_agg(sub), '[]'::json) FROM (
			SELECT d.id, d.name, d.slug, d.short_description, d.county, d.category, d.updated_at,
				COALESCE(med.media, '[]'::json) AS media
			FROM destinations d
			LEFT JOIN LATERAL (
				SELECT COALESCE(json_agg(json_build_object(
					'objectKey', ma.object_key,
					'thumbnailKey', ma.thumbnail_key,
					'type', ma.type,
					'altText', ma.alt_text
				) ORDER BY ma.display_order) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
				FROM media_assets ma
				WHERE ma.entity_type = 'destination' AND ma.entity_id = d.id::text
			) med ON true
			WHERE d.status = 'published'
			ORDER BY d.updated_at DESC LIMIT 5
		) sub`,
	).Scan(&destinations)
	if err != nil {
		destinations = json.RawMessage(`[]`)
	}

	resp := map[string]any{
		"curatedDestinations": destinations,
		"faqs":                companionFAQs,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
