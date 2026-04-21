package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"googleMap/config"
	"googleMap/models"
)

type GoogleService struct {
	apiKey     string
	httpClient *http.Client
}

func NewGoogleService() *GoogleService {
	return &GoogleService{
		apiKey: config.Get().Google.APIKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NearbySearchRequest 附近搜索请求
type NearbySearchRequest struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Radius    int     `json:"radius"`  // 米
	Keyword   string  `json:"keyword"` // 搜索关键词
	PageToken string  `json:"page_token,omitempty"`
}

// PlacesNearbyResponse Google Places API 响应
type PlacesNearbyResponse struct {
	Results       []PlaceResult `json:"results"`
	NextPageToken string        `json:"next_page_token"`
	Status        string        `json:"status"`
	ErrorMessage  string        `json:"error_message"`
}

type PlaceResult struct {
	PlaceID          string       `json:"place_id"`
	Name             string       `json:"name"`
	FormattedAddress string       `json:"vicinity"`
	Geometry         Geometry     `json:"geometry"`
	Rating           float64      `json:"rating"`
	UserRatingsTotal int          `json:"user_ratings_total"`
	Types            []string     `json:"types"`
	BusinessStatus   string       `json:"business_status"`
	Photos           []PlacePhoto `json:"photos"`
	OpeningHours     *OpeningHrs  `json:"opening_hours"`
}

type Geometry struct {
	Location LatLng `json:"location"`
}

type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type PlacePhoto struct {
	PhotoReference string `json:"photo_reference"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
}

type OpeningHrs struct {
	OpenNow bool `json:"open_now"`
}

// PlaceDetailResponse 商家详情响应
type PlaceDetailResponse struct {
	Result PlaceDetail `json:"result"`
	Status string      `json:"status"`
}

type PlaceDetail struct {
	PlaceID              string          `json:"place_id"`
	Name                 string          `json:"name"`
	FormattedAddress     string          `json:"formatted_address"`
	FormattedPhoneNumber string          `json:"formatted_phone_number"`
	InternationalPhone   string          `json:"international_phone_number"`
	Website              string          `json:"website"`
	Rating               float64         `json:"rating"`
	UserRatingsTotal     int             `json:"user_ratings_total"`
	Types                []string        `json:"types"`
	Geometry             Geometry        `json:"geometry"`
	BusinessStatus       string          `json:"business_status"`
	OpeningHours         *DetailOpenHrs  `json:"opening_hours"`
	Photos               []PlacePhoto    `json:"photos"`
}

type DetailOpenHrs struct {
	OpenNow     bool     `json:"open_now"`
	WeekdayText []string `json:"weekday_text"`
}

// NearbySearch 附近搜索
func (s *GoogleService) NearbySearch(req NearbySearchRequest) (*PlacesNearbyResponse, error) {
	params := url.Values{}
	params.Set("location", fmt.Sprintf("%f,%f", req.Latitude, req.Longitude))
	params.Set("radius", fmt.Sprintf("%d", req.Radius))
	params.Set("key", s.apiKey)
	if req.Keyword != "" {
		params.Set("keyword", req.Keyword)
	}
	if req.PageToken != "" {
		params.Set("pagetoken", req.PageToken)
	}

	apiURL := "https://maps.googleapis.com/maps/api/place/nearbysearch/json?" + params.Encode()
	resp, err := s.httpClient.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("请求Google API失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var result PlacesNearbyResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if result.Status != "OK" && result.Status != "ZERO_RESULTS" {
		return nil, fmt.Errorf("Google API错误: %s - %s", result.Status, result.ErrorMessage)
	}

	return &result, nil
}

// GetPlaceDetail 获取商家详情（电话、邮箱、网站等）
func (s *GoogleService) GetPlaceDetail(placeID string) (*PlaceDetail, error) {
	params := url.Values{}
	params.Set("place_id", placeID)
	params.Set("fields", "place_id,name,formatted_address,formatted_phone_number,international_phone_number,website,rating,user_ratings_total,types,geometry,business_status,opening_hours,photos")
	params.Set("key", s.apiKey)

	apiURL := "https://maps.googleapis.com/maps/api/place/details/json?" + params.Encode()
	resp, err := s.httpClient.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("请求商家详情失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var result PlaceDetailResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if result.Status != "OK" {
		return nil, fmt.Errorf("Google API错误: %s", result.Status)
	}

	return &result.Result, nil
}

// convertDetailToCompany 将 PlaceDetail 转为 Company model
func convertDetailToCompany(detail *PlaceDetail, source string) models.Company {
	var openHoursJSON string
	if detail.OpeningHours != nil {
		b, _ := json.Marshal(detail.OpeningHours.WeekdayText)
		openHoursJSON = string(b)
	}

	var photosJSON string
	if len(detail.Photos) > 0 {
		refs := make([]string, 0, len(detail.Photos))
		for _, p := range detail.Photos {
			refs = append(refs, p.PhotoReference)
		}
		b, _ := json.Marshal(refs)
		photosJSON = string(b)
	}

	domain := ""
	if detail.Website != "" {
		domain = extractDomainFromURL(detail.Website)
	}

	return models.Company{
		Source:             source,
		PlaceID:            detail.PlaceID,
		Name:               detail.Name,
		FormattedAddress:   detail.FormattedAddress,
		Phone:              detail.FormattedPhoneNumber,
		InternationalPhone: detail.InternationalPhone,
		Website:            detail.Website,
		Domain:             domain,
		Rating:             detail.Rating,
		UserRatingsTotal:   detail.UserRatingsTotal,
		Types:              strings.Join(detail.Types, ","),
		Latitude:           detail.Geometry.Location.Lat,
		Longitude:          detail.Geometry.Location.Lng,
		BusinessStatus:     detail.BusinessStatus,
		OpeningHours:       openHoursJSON,
		Photos:             photosJSON,
	}
}

// convertBasicToCompany 从 NearbySearch 结果创建基础 Company（无详情）
func convertBasicToCompany(place *PlaceResult, source string) models.Company {
	return models.Company{
		Source:           source,
		PlaceID:          place.PlaceID,
		Name:             place.Name,
		FormattedAddress: place.FormattedAddress,
		Rating:           place.Rating,
		UserRatingsTotal: place.UserRatingsTotal,
		Types:            strings.Join(place.Types, ","),
		Latitude:         place.Geometry.Location.Lat,
		Longitude:        place.Geometry.Location.Lng,
		BusinessStatus:   place.BusinessStatus,
	}
}

// ConvertDetailToCompany 公开版本供 handler 调用
func ConvertDetailToCompany(detail *PlaceDetail, source string) models.Company {
	return convertDetailToCompany(detail, source)
}

// ConvertBasicToCompany 公开版本供 handler 调用
func ConvertBasicToCompany(place *PlaceResult, source string) models.Company {
	return convertBasicToCompany(place, source)
}

func extractDomainFromURL(rawURL string) string {
	rawURL = strings.TrimPrefix(rawURL, "https://")
	rawURL = strings.TrimPrefix(rawURL, "http://")
	rawURL = strings.TrimPrefix(rawURL, "www.")
	if idx := strings.Index(rawURL, "/"); idx > 0 {
		rawURL = rawURL[:idx]
	}
	return rawURL
}

// GetPhotoURL 获取照片URL
func (s *GoogleService) GetPhotoURL(photoReference string, maxWidth int) string {
	return fmt.Sprintf("https://maps.googleapis.com/maps/api/place/photo?maxwidth=%d&photo_reference=%s&key=%s",
		maxWidth, photoReference, s.apiKey)
}
