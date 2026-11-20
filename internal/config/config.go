package config

import (
	"fmt"
	"strings"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	// Environment is the deployment tier. Insecure development fallbacks are
	// only allowed in the development tier; anything else must be configured
	// explicitly. Defaults to development so local runs need no extra setup.
	Environment        string `env:"APP_ENV" envDefault:"development"`
	Port               string `env:"PORT" envDefault:"8080"`
	DatabaseURL        string `env:"DATABASE_URL,required"`
	CORSOrigins        string `env:"CORS_ALLOWED_ORIGINS" envDefault:"*"`
	AdminJWKSURL       string `env:"ADMIN_JWKS_URL,required"`
	JWTExpectedIss     string `env:"JWT_EXPECTED_ISSUER" envDefault:""`
	JWTExpectedAud     string `env:"JWT_EXPECTED_AUDIENCE" envDefault:"ytci-api"`
	R2AccountID        string `env:"R2_ACCOUNT_ID"`
	R2AccessKeyID      string `env:"R2_ACCESS_KEY_ID"`
	R2SecretAccess     string `env:"R2_SECRET_ACCESS_KEY"`
	R2Bucket           string `env:"R2_BUCKET"`
	ExpoPushToken      string `env:"EXPO_PUSH_TOKEN"`
	LogLevel           string `env:"LOG_LEVEL" envDefault:"info"`
	JWKSCacheTTL       int    `env:"JWKS_CACHE_TTL_MINUTES" envDefault:"60"`
	GooglePlacesAPIKey string `env:"GOOGLE_PLACES_API_KEY,required"`

	// Recognition stays dark until the privacy enablement gate is signed.
	ThenNowRecognitionEnabled       bool `env:"THEN_NOW_RECOGNITION_ENABLED" envDefault:"false"`
	ThenNowPublicRecognitionEnabled bool `env:"THEN_NOW_PUBLIC_RECOGNITION_ENABLED" envDefault:"false"`
	ThenNowAnalyticsV2Enabled       bool `env:"THEN_NOW_ANALYTICS_V2_ENABLED" envDefault:"false"`

	// Published Then & Now media is served as short-lived signed R2 URLs.
	ThenNowMediaURLTTLMinutes int `env:"THEN_NOW_MEDIA_URL_TTL_MINUTES" envDefault:"15"`

	// MICE events. Tickets are signed with this secret; notifications stay
	// dark until the Program channel owners enable event delivery.
	MiceTicketSecret         string `env:"MICE_TICKET_SECRET"`
	MiceNotificationsEnabled bool   `env:"MICE_NOTIFICATIONS_ENABLED" envDefault:"false"`
	ORSAPIKey            string `env:"ORS_API_KEY"`
	GoogleVisionAPIKey   string `env:"GOOGLE_VISION_API_KEY"`
	GoogleVisionEndpoint string `env:"GOOGLE_VISION_ENDPOINT" envDefault:"https://eu-vision.googleapis.com"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}

// IsDevelopment reports whether the process runs in the local development
// tier, where documented insecure fallbacks are allowed.
func (c *Config) IsDevelopment() bool {
	switch strings.ToLower(strings.TrimSpace(c.Environment)) {
	case "", "development", "dev", "local":
		return true
	default:
		return false
	}
}

// Validate enforces startup invariants that environment decoding cannot
// express. It is called once at process start, before any handler is built, so
// a misconfigured deployment fails immediately instead of running with a
// development fallback.
func (c *Config) Validate() error {
	if !c.IsDevelopment() && strings.TrimSpace(c.MiceTicketSecret) == "" {
		return fmt.Errorf(
			"config: MICE_TICKET_SECRET is required when APP_ENV=%q; refusing to sign MICE tickets with the development key",
			c.Environment)
	}
	return nil
}
