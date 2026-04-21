package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

type CityHandler struct{}

func NewCityHandler() *CityHandler {
	return &CityHandler{}
}

type Continent struct {
	Name      string     `json:"name"`
	NameEn    string     `json:"name_en"`
	Countries []Country `json:"countries"`
}

type Country struct {
	Name    string  `json:"name"`
	NameEn  string  `json:"name_en"`
	Code    string  `json:"code"`
	Cities  []City  `json:"cities"`
}

type City struct {
	Name    string  `json:"name"`
	NameEn  string  `json:"name_en"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

type CitiesData struct {
	Continents []Continent `json:"continents"`
}

func (h *CityHandler) GetContinents(c *gin.Context) {
	data, err := loadCitiesData()
	if err != nil {
		log.Printf("Error loading cities data: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "加载城市数据失败: " + err.Error()})
		return
	}

	if len(data.Continents) == 0 {
		log.Println("Cities data loaded but empty")
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": []interface{}{}})
		return
	}

	log.Printf("Successfully loaded %d continents", len(data.Continents))

	// 返回仅包含洲和国家的数据（不带城市详情）
	result := make([]gin.H, 0)
	for _, continent := range data.Continents {
		countries := make([]gin.H, 0)
		for _, country := range continent.Countries {
			countries = append(countries, gin.H{
				"name":    country.Name,
				"name_en": country.NameEn,
				"code":    country.Code,
			})
		}
		result = append(result, gin.H{
			"name":      continent.Name,
			"name_en":   continent.NameEn,
			"countries": countries,
		})
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": result})
}

func (h *CityHandler) GetCitiesByCountry(c *gin.Context) {
	countryCode := c.Query("country_code")
	if countryCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "缺少 country_code 参数"})
		return
	}

	data, err := loadCitiesData()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "加载城市数据失败"})
		return
	}

	for _, continent := range data.Continents {
		for _, country := range continent.Countries {
			if country.Code == countryCode {
				c.JSON(http.StatusOK, gin.H{"code": 0, "data": country.Cities})
				return
			}
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "未找到指定国家的城市"})
}

func loadCitiesData() (*CitiesData, error) {
	// 尝试多个可能的路径
	paths := []string{
		"config/cities.json",
		"./config/cities.json",
		"../config/cities.json",
	}

	var data CitiesData
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()

		if err := json.NewDecoder(f).Decode(&data); err != nil {
			return nil, err
		}
		return &data, nil
	}

	return nil, os.ErrNotExist
}
