package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WeatherSnapshot 简化天气信息
// Summary 用于邮件文案，尽量短句。
type WeatherSnapshot struct {
	Summary string
}

type WeatherService struct {
	client *http.Client
}

func NewWeatherService() *WeatherService {
	return &WeatherService{
		client: &http.Client{Timeout: 8 * time.Second},
	}
}

type openMeteoResp struct {
	Current struct {
		Temperature float64 `json:"temperature_2m"`
		WeatherCode int     `json:"weather_code"`
	} `json:"current"`
}

func (s *WeatherService) CurrentByLatLng(lat, lng float64) (*WeatherSnapshot, error) {
	if lat == 0 && lng == 0 {
		return nil, fmt.Errorf("missing coordinates")
	}

	url := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%f&longitude=%f&current=temperature_2m,weather_code&timezone=auto", lat, lng)
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather api status: %d", resp.StatusCode)
	}

	var data openMeteoResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	desc := weatherCodeToCN(data.Current.WeatherCode)
	summary := fmt.Sprintf("当地%s，约%.0f°C", desc, data.Current.Temperature)
	return &WeatherSnapshot{Summary: summary}, nil
}

func weatherCodeToCN(code int) string {
	switch code {
	case 0:
		return "晴朗"
	case 1, 2:
		return "多云"
	case 3:
		return "阴天"
	case 45, 48:
		return "有雾"
	case 51, 53, 55, 56, 57:
		return "毛毛雨"
	case 61, 63, 65, 66, 67, 80, 81, 82:
		return "降雨"
	case 71, 73, 75, 77, 85, 86:
		return "降雪"
	case 95, 96, 99:
		return "雷雨"
	default:
		return "天气变化"
	}
}
