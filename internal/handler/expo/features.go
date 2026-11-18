package expo

import (
	"encoding/json"
	"net/http"

	"github.com/bradleygichuru/ytci-go/internal/config"
)

// FeaturesHandler exposes only the public feature switches the mobile app
// needs to hide dark-launched surfaces. It never exposes admin flags.
type FeaturesHandler struct {
	recognition       bool
	publicRecognition bool
	analyticsV2       bool
}

func NewFeaturesHandler(cfg *config.Config) *FeaturesHandler {
	return &FeaturesHandler{
		recognition:       cfg.ThenNowRecognitionEnabled,
		publicRecognition: cfg.ThenNowPublicRecognitionEnabled,
		analyticsV2:       cfg.ThenNowAnalyticsV2Enabled,
	}
}

func (h *FeaturesHandler) ThenNowConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{
		"recognitionEnabled":       h.recognition,
		"publicRecognitionEnabled": h.publicRecognition,
		"analyticsV2Enabled":       h.analyticsV2,
	})
}
