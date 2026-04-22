package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

type CityHandler struct{}

func NewCityHandler() *CityHandler {
	return &CityHandler{}
}

type Continent struct {
	Name      string    `json:"name"`
	NameEn    string    `json:"name_en"`
	Countries []Country `json:"countries"`
}

type Country struct {
	Name   string `json:"name"`
	NameEn string `json:"name_en"`
	Code   string `json:"code"`
	Cities []City `json:"cities"`
}

type City struct {
	Name   string  `json:"name"`
	NameEn string  `json:"name_en"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
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

	// 返回完整的洲、国家和城市数据
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": data.Continents})
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
	// 获取当前执行文件所在目录
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("Error getting executable path: %v", err)
	} else {
		log.Printf("Executable path: %s", exePath)
	}

	// 获取工作目录
	workDir, err := os.Getwd()
	if err != nil {
		log.Printf("Error getting working directory: %v", err)
	} else {
		log.Printf("Working directory: %s", workDir)
	}

	// 获取可执行文件所在目录
	var exeDir string
	if exePath != "" {
		exeDir = filepath.Dir(exePath)
		log.Printf("Executable directory: %s", exeDir)
	}

	// 尝试多个可能的路径（优先根目录下的 cities.json）
	paths := []string{
		"cities.json",
		"./cities.json",
		workDir + "/cities.json",
		exeDir + "/cities.json",
		"config/cities.json",
		"./config/cities.json",
		"../config/cities.json",
		"../../config/cities.json",
		workDir + "/config/cities.json",
		"/app/config/cities.json",
		"/opt/googleMap/config/cities.json",
	}

	var data CitiesData
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			log.Printf("Trying path: %s - not found", path)
			continue
		}
		defer f.Close()

		if err := json.NewDecoder(f).Decode(&data); err != nil {
			return nil, err
		}
		log.Printf("Successfully loaded cities data from: %s", path)
		return &data, nil
	}

	log.Println("All paths failed, cities.json not found")
	return nil, os.ErrNotExist
}
