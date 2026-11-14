package expo

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/composer"
	"github.com/bradleygichuru/ytci-go/internal/discovery"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/places"
)

type ComposerHandler struct {
	engine *composer.Engine
}

func NewComposerHandler(pool *pgxpool.Pool, googleKey, orsKey string) *ComposerHandler {
	catalogProvider := &composer.DBCatalogProvider{Pool: pool}
	resolver := &composer.DBCountyResolver{Pool: pool}
	events := &composer.DBEventProvider{Pool: pool}

	var router composer.Router = composer.StraightLineRouter{}
	if orsKey != "" {
		router = &composer.FallbackRouter{
			Primary: &composer.ORSQuotaRouter{
				Router:     composer.NewORSRouter(orsKey),
				Pool:       pool,
				DailyLimit: 500,
			},
			Fallback: composer.StraightLineRouter{},
		}
	}
	router = &composer.CachedRouter{Router: router, Pool: pool}

	var discoveryProvider composer.DiscoveryProvider
	if googleKey != "" {
		adapter := discovery.NewAdapter(pool, places.NewClient(googleKey), discovery.DailyRequestBudgetFromEnv())
		discoveryProvider = composer.DiscoveryAdapter{Adapter: adapter}
	}

	engine := composer.NewEngine(
		catalogProvider,
		catalogProvider,
		resolver,
		router,
		composer.NewOpenMeteoWeather(),
		events,
		discoveryProvider,
		&composer.DBSuggestionCache{Pool: pool},
		&composer.DBUsageStore{Pool: pool},
	)
	return &ComposerHandler{engine: engine}
}

func NewComposerHandlerWithEngine(engine *composer.Engine) *ComposerHandler {
	return &ComposerHandler{engine: engine}
}

func (h *ComposerHandler) Suggest(w http.ResponseWriter, r *http.Request) {
	var brief composer.Brief
	if err := json.NewDecoder(r.Body).Decode(&brief); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	response, err := h.engine.Suggest(r.Context(), userID, brief)
	if err != nil {
		var validation *composer.ValidationError
		if errors.As(err, &validation) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "INVALID_BRIEF",
					"message": "The trip brief has invalid fields.",
					"fields":  validation.Fields,
				},
			})
			return
		}
		var rate *composer.RateLimitError
		if errors.As(err, &rate) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "RATE_LIMITED",
					"message": "The daily suggestion limit has been reached.",
					"limit":   rate.Limit,
					"used":    rate.Used,
				},
				"retryAfter": rate.RetryAfterSeconds,
			})
			return
		}
		handler.WriteServerError(w, r, "COMPOSER_ERROR", "failed to suggest itineraries", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
