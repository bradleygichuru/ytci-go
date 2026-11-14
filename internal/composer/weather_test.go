package composer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenMeteoDaily(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "ecmwf_ifs", r.URL.Query().Get("models"))
		assert.Contains(t, r.URL.Query().Get("daily"), "precipitation_probability_max")
		assert.Equal(t, "2026-11-20", r.URL.Query().Get("start_date"))
		assert.Equal(t, "2026-11-21", r.URL.Query().Get("end_date"))
		json.NewEncoder(w).Encode(map[string]any{
			"daily": map[string]any{
				"time":                          []string{"2026-11-20", "2026-11-21"},
				"temperature_2m_max":            []any{26.5, 25.0},
				"precipitation_probability_max": []any{10, 40},
				"weathercode":                   []any{1, 61},
			},
		})
	}))
	defer ts.Close()

	client := &OpenMeteoWeather{
		BaseURL: ts.URL,
		HTTP:    ts.Client(),
		Now: func() time.Time {
			return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
		},
	}
	start := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	days, err := client.Daily(context.Background(), Point{Latitude: -1.29, Longitude: 36.82}, start, end)
	require.NoError(t, err)
	require.Len(t, days, 2)
	assert.Equal(t, "2026-11-20", days[0].Date)
	assert.Equal(t, "forecast", days[0].Horizon)
	assert.Equal(t, 10, *days[0].PrecipProbability)
	assert.Equal(t, 26.5, *days[0].TempMaxC)
	assert.Equal(t, 1, *days[0].WeatherCode)
	assert.Equal(t, "Open-Meteo ECMWF IFS", days[0].Source)
}

func TestOpenMeteoOutOfRange(t *testing.T) {
	client := &OpenMeteoWeather{
		Now: func() time.Time {
			return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
		},
	}
	start := time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)
	_, err := client.Daily(context.Background(), Point{}, start, start)
	require.True(t, errors.Is(err, ErrWeatherOutOfRange))
}

func TestOpenMeteoError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	client := &OpenMeteoWeather{BaseURL: ts.URL, HTTP: ts.Client(), Now: time.Now}
	_, err := client.Daily(context.Background(), Point{}, time.Now(), time.Now())
	require.Error(t, err)
}
