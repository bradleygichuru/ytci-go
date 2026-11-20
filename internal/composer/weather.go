package composer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

var ErrWeatherOutOfRange = errors.New("composer: weather horizon is outside the 15-day window")

// OpenMeteoWeather calls the free Open-Meteo forecast API pinned to ECMWF IFS.
// Climatology/ERA5 normals are deliberately a later layer; when the date is
// beyond 15 days the engine omits weather instead of guessing.
type OpenMeteoWeather struct {
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

func NewOpenMeteoWeather() *OpenMeteoWeather {
	return &OpenMeteoWeather{
		BaseURL: "https://api.open-meteo.com/v1/forecast",
		HTTP:    &http.Client{Timeout: 5 * time.Second},
		Now:     time.Now,
	}
}

func (w *OpenMeteoWeather) Daily(ctx context.Context, point Point, start, end time.Time) ([]DayWeather, error) {
	now := w.Now().UTC()
	if start.After(now.AddDate(0, 0, 15)) || end.Before(now.AddDate(0, 0, -1)) {
		return nil, ErrWeatherOutOfRange
	}
	base := w.BaseURL
	if base == "" {
		base = "https://api.open-meteo.com/v1/forecast"
	}
	query := url.Values{}
	query.Set("latitude", fmt.Sprintf("%.4f", point.Latitude))
	query.Set("longitude", fmt.Sprintf("%.4f", point.Longitude))
	query.Set("daily", "temperature_2m_max,precipitation_probability_max,weathercode")
	query.Set("timezone", "Africa/Nairobi")
	query.Set("start_date", start.Format("2006-01-02"))
	query.Set("end_date", end.Format("2006-01-02"))
	query.Set("models", "ecmwf_ifs")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("composer: weather request: %w", err)
	}
	httpClient := w.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("composer: weather: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("composer: weather returned %d", resp.StatusCode)
	}

	var out struct {
		Daily struct {
			Time        []string   `json:"time"`
			TempMax     []*float64 `json:"temperature_2m_max"`
			Precip      []*int     `json:"precipitation_probability_max"`
			WeatherCode []*int     `json:"weathercode"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("composer: weather decode: %w", err)
	}

	result := make([]DayWeather, 0, len(out.Daily.Time))
	for i, date := range out.Daily.Time {
		day := DayWeather{
			Date:    date,
			Horizon: horizonFor(date, now),
			Source:  "Open-Meteo ECMWF IFS",
		}
		if i < len(out.Daily.TempMax) {
			day.TempMaxC = out.Daily.TempMax[i]
		}
		if i < len(out.Daily.Precip) {
			day.PrecipProbability = out.Daily.Precip[i]
		}
		if i < len(out.Daily.WeatherCode) {
			day.WeatherCode = out.Daily.WeatherCode[i]
		}
		result = append(result, day)
	}
	return result, nil
}

func horizonFor(date string, now time.Time) string {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "forecast"
	}
	if int(d.Sub(startOfDay(now)).Hours()/24) <= 7 {
		return "forecast"
	}
	return "outlook"
}
