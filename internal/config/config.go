package config

import (
	"fmt"
	"github.com/caarlos0/env/v11"
)

type Config struct {
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
}

func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}
