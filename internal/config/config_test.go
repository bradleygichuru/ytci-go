package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
)

func TestValidateRequiresMiceTicketSecretOutsideDevelopment(t *testing.T) {
	for _, env := range []string{"development", "dev", "local", ""} {
		cfg := &config.Config{Environment: env}
		assert.NoError(t, cfg.Validate(), "environment %q allows the development fallback", env)
	}

	for _, env := range []string{"production", "staging", "preview"} {
		cfg := &config.Config{Environment: env}
		err := cfg.Validate()
		require.Error(t, err, "environment %q must not run without a ticket secret", env)
		assert.Contains(t, err.Error(), "MICE_TICKET_SECRET")

		cfg.MiceTicketSecret = "a-real-secret"
		assert.NoError(t, cfg.Validate())
	}
}

func TestIsDevelopment(t *testing.T) {
	assert.True(t, (&config.Config{}).IsDevelopment())
	assert.True(t, (&config.Config{Environment: " Development "}).IsDevelopment())
	assert.False(t, (&config.Config{Environment: "production"}).IsDevelopment())
}
