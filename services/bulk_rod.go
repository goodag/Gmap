package services

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"googleMap/models"

	"gorm.io/gorm"
)

// BulkRodCity 平铺城市（JSON 序列化用）
type BulkRodCity struct {
	CityNameEn  string  `json:"city_name_en"`
	CityName    string  `json:"city_name"`
	CountryName string  `json:"country_name"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
}

// BulkRodService 批量 Rod 搜索服务
type BulkRodService struct {
	db         *gorm.DB
	rodSvc     *RodMapsService
	scraperSvc *ScraperService
	stopCh     chan struct{}
	mu         sync.Mutex
	running    bool
}

func NewBulkRodService(db *gorm.DB) *BulkRodService {
	return &BulkRodService{
		db:         db,
		rodSvc:     NewRodMapsService(),
		scraperSvc: NewScraperService(),
	}
}

func (s *BulkRodService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// CreateTask 创建批量任务：解析关键词+国家→城市平铺存 JSON 字段，创建唯一 search_record
func (s *BulkRodService) CreateTask(keywords, countries string, intervalSec int) (*models.BulkRodProgress, error) {
	if intervalSec <= 0 {
		intervalSec = 30
	}

	kws := parseKeywordList(keywords)
	if len(kws) == 0 {
		return nil, fmt.Errorf("关键词列表为空")
	}

	cities, err := loadCitiesByCountries(parseCountryList(countries))
	if err != nil {
		return nil, fmt.Errorf("加载城市数据失败: %w", err)
	}
	if len(cities) == 0 {
		return nil, fmt.Errorf("未找到匹配的城市，请检查国家名称")
	}

	citiesJSON, _ := json.Marshal(cities)

	progress := &models.BulkRodProgress{
		Keywords:      keywords,
		Countries:     countries,
		TotalKeywords: len(kws),
		TotalCities:   len(cities),
		TotalCombos:   len(kws) * len(cities),
		IntervalSec:   intervalSec,
		Status:        0,
		CitiesFlat:    string(citiesJSON),
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(progress).Error; err != nil {
			return fmt.Errorf("创建任务失败: %w", err)
		}

		// 创建唯一的 search_record（等同于前台点了一次搜索）
		record := models.SearchRecord{
			Source:  "rod_bulk",
			Keyword: fmt.Sprintf("定时任务-%d", progress.ID),
			Address: fmt.Sprintf("%s | %s", keywords, countries),
			Status:  0,
		}
		if err := tx.Create(&record).Error; err != nil {
			return fmt.Errorf("创建 search_record 失败: %w", err)
		}
		if err := tx.Model(progress).Update("search_record_id", record.ID).Error; err != nil {
			return fmt.Errorf("回写 search_record_id 失败: %w", err)
		}

		progress.SearchRecordID = record.ID
		return nil
	}); err != nil {
		return nil, err
	}

	log.Printf("[BulkRod] 创建任务 #%d: %d 关键词 × %d 城市 = %d 组合, search_record=#%d",
		progress.ID, len(kws), len(cities), progress.TotalCombos, progress.SearchRecordID)

	return progress, nil
}

// Start 启动/恢复批量任务
func (s *BulkRodService) Start(progressID uint64) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("已有任务在运行中，请先停止")
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	go s.run(progressID)
	return nil
}

// Stop 停止运行中的任务
func (s *BulkRodService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running && s.stopCh != nil {
		close(s.stopCh)
		s.running = false
		log.Println("[BulkRod] 已发送停止信号")
	}
}

func (s *BulkRodService) GetLatestProgress() (*models.BulkRodProgress, error) {
	var p models.BulkRodProgress
	err := s.db.Order("id DESC").First(&p).Error
	return &p, err
}

func (s *BulkRodService) GetProgress(id uint64) (*models.BulkRodProgress, error) {
	var p models.BulkRodProgress
	err := s.db.First(&p, id).Error
	return &p, err
}

// ResetProgress 重置进度（从头开始）
func (s *BulkRodService) ResetProgress(id uint64) error {
	if s.IsRunning() {
		return fmt.Errorf("任务运行中，请先停止")
	}
	return s.db.Model(&models.BulkRodProgress{}).Where("id = ?", id).Updates(map[string]interface{}{
		"keyword_index": 0,
		"city_index":    0,
		"completed":     0,
		"success_count": 0,
		"error_count":   0,
		"total_found":   0,
		"status":        0,
		"last_keyword":  "",
		"last_city":     "",
		"last_country":  "",
		"last_error":    "",
	}).Error
}

// RetryFailed 从当前 keyword_index + city_index 继续
func (s *BulkRodService) RetryFailed(progressID uint64) error {
	if s.IsRunning() {
		return fmt.Errorf("已有任务在运行中，请先停止")
	}
	var p models.BulkRodProgress
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&p, progressID).Error; err != nil {
			return fmt.Errorf("任务不存在: %w", err)
		}
		if err := tx.Model(&p).Updates(map[string]interface{}{
			"status":     0,
			"last_error": "",
		}).Error; err != nil {
			return fmt.Errorf("重置任务状态失败: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	log.Printf("[BulkRod] 重试任务 #%d，从 keyword[%d] city[%d] 开始",
		progressID, p.KeywordIndex, p.CityIndex)
	return s.Start(progressID)
}

// ========== 主循环 ==========

func (s *BulkRodService) run(progressID uint64) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[BulkRod] panic recovered: %v", r)
		}
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	var progress models.BulkRodProgress
	if err := s.db.First(&progress, progressID).Error; err != nil {
		log.Printf("[BulkRod] 加载进度失败: %v", err)
		return
	}

	keywords := parseKeywordList(progress.Keywords)
	if len(keywords) == 0 {
		log.Println("[BulkRod] 关键词列表为空")
		return
	}

	// 从 cities_flat JSON 字段解析城市
	var cities []BulkRodCity
	if err := json.Unmarshal([]byte(progress.CitiesFlat), &cities); err != nil {
		log.Printf("[BulkRod] 解析城市列表失败: %v", err)
		return
	}
	if len(cities) == 0 {
		log.Println("[BulkRod] 城市列表为空")
		return
	}

	progress.Status = 1
	s.db.Save(&progress)

	log.Printf("[BulkRod] ▶️ 任务 #%d 启动: %d 关键词 × %d 城市 = %d 组合，从 keyword[%d] city[%d] 继续, search_record=#%d",
		progress.ID, len(keywords), len(cities), progress.TotalCombos,
		progress.KeywordIndex, progress.CityIndex, progress.SearchRecordID)

	interval := time.Duration(progress.IntervalSec) * time.Second
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}

	for ki := progress.KeywordIndex; ki < len(keywords); ki++ {
		startCi := 0
		if ki == progress.KeywordIndex {
			startCi = progress.CityIndex
		}

		for ci := startCi; ci < len(cities); ci++ {
			select {
			case <-s.stopCh:
				progress.Status = 2
				progress.KeywordIndex = ki
				progress.CityIndex = ci
				s.db.Save(&progress)
				log.Printf("[BulkRod] ⏸️ 已暂停: keyword[%d]=%s, city[%d]=%s(%s)",
					ki, keywords[ki], ci, cities[ci].CityNameEn, cities[ci].CountryName)
				return
			default:
			}

			keyword := keywords[ki]
			city := cities[ci]
			cityQuery := city.CityNameEn + ", " + city.CountryName

			log.Printf("[BulkRod] 🔍 [%d/%d] \"%s\" in %s",
				progress.Completed+1, progress.TotalCombos, keyword, cityQuery)

			startTime := time.Now()
			found, savedCount, searchErr := s.executeOneSearch(keyword, cityQuery, progress.SearchRecordID)
			duration := time.Since(startTime).Round(time.Millisecond).String()

			if searchErr != nil {
				progress.ErrorCount++
				progress.LastError = searchErr.Error()
				log.Printf("[BulkRod] ❌ 失败(%s): %v", duration, searchErr)
			} else {
				progress.SuccessCount++
				progress.TotalFound += found
				progress.LastError = ""
				log.Printf("[BulkRod] ✅ 找到 %d 家，入库 %d 家 (%s)", found, savedCount, duration)
			}

			progress.Completed++
			progress.LastKeyword = keyword
			progress.LastCity = city.CityNameEn
			progress.LastCountry = city.CountryName

			// 更新下一个位置
			if ci+1 < len(cities) {
				progress.KeywordIndex = ki
				progress.CityIndex = ci + 1
			} else {
				progress.KeywordIndex = ki + 1
				progress.CityIndex = 0
			}

			// 更新 search_record 的 total_results
			if err := s.db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&models.SearchRecord{}).Where("id = ?", progress.SearchRecordID).
					Update("total_results", gorm.Expr("total_results + ?", found)).Error; err != nil {
					return fmt.Errorf("更新 search_record total_results 失败: %w", err)
				}
				if err := tx.Save(&progress).Error; err != nil {
					return fmt.Errorf("保存任务进度失败: %w", err)
				}
				return nil
			}); err != nil {
				progress.ErrorCount++
				progress.LastError = err.Error()
				log.Printf("[BulkRod] ❌ 持久化失败: %v", err)
			}

			// 等待间隔
			select {
			case <-s.stopCh:
				progress.Status = 2
				s.db.Save(&progress)
				log.Printf("[BulkRod] ⏸️ 已暂停(间隔中)")
				return
			case <-time.After(interval):
			}
		}
	}

	// 完成：更新 search_record 状态
	progress.Status = 3
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&progress).Error; err != nil {
			return fmt.Errorf("保存完成状态失败: %w", err)
		}
		if err := tx.Model(&models.SearchRecord{}).Where("id = ?", progress.SearchRecordID).
			Updates(map[string]interface{}{"status": 1, "total_results": progress.TotalFound}).Error; err != nil {
			return fmt.Errorf("更新 search_record 完成状态失败: %w", err)
		}
		return nil
	}); err != nil {
		log.Printf("[BulkRod] ❌ 完成态持久化失败: %v", err)
	}

	log.Printf("[BulkRod] 🎉 任务 #%d 完成! 成功=%d 失败=%d 累计商家=%d",
		progress.ID, progress.SuccessCount, progress.ErrorCount, progress.TotalFound)
}

// executeOneSearch 执行一次 keyword+city 搜索，不创建新 search_record，只 upsert companies
func (s *BulkRodService) executeOneSearch(keyword, cityQuery string, searchRecordID uint64) (int, int, error) {
	companies, err := s.rodSvc.SearchNearby(RodSearchRequest{
		Keyword:  keyword,
		City:     cityQuery,
		MaxCount: 1000,
		Radius:   50000, // 50km
	})
	if err != nil {
		return 0, 0, err
	}
	if len(companies) == 0 {
		return 0, 0, nil
	}

	// 爬取网站
	for i := range companies {
		if companies[i].Website == "" || companies[i].ScrapeSuccess {
			continue
		}
		page := s.scraperSvc.ScrapeWebsite(companies[i].Website)
		if page == nil {
			continue
		}
		emailsJSON, _ := json.Marshal(page.Emails)
		phonesJSON, _ := json.Marshal(page.Phones)
		socialJSON, _ := json.Marshal(page.SocialLinks)

		companies[i].ScrapedEmails = string(emailsJSON)
		companies[i].ScrapedPhones = string(phonesJSON)
		companies[i].SocialLinks = string(socialJSON)
		companies[i].PageTitle = page.Title
		companies[i].Description = page.Description
		companies[i].BodyText = page.BodyText
		companies[i].ScrapeSuccess = page.Success
		companies[i].ScrapeError = page.Error
		if len(page.Emails) > 0 && companies[i].Email == "" {
			companies[i].Email = page.Emails[0]
		}
		if len(page.Phones) > 0 && companies[i].Phone == "" {
			companies[i].Phone = page.Phones[0]
		}
	}

	// 入库（去重），关联同一条 search_record
	savedCount := 0
	for i := range companies {
		companies[i].SearchRecordID = searchRecordID
		s.upsertCompany(&companies[i])
		if companies[i].ID > 0 {
			savedCount++
			if companies[i].ScrapedEmails != "" && companies[i].ScrapedEmails != "[]" {
				s.saveCompanyEmails(companies[i].ID, companies[i].ScrapedEmails)
			}
		}
	}

	return len(companies), savedCount, nil
}

// upsertCompany 按 place_id 去重
func (s *BulkRodService) upsertCompany(company *models.Company) {
	if company.PlaceID == "" {
		s.db.Create(company)
		return
	}
	var existing models.Company
	if err := s.db.Where("place_id = ?", company.PlaceID).First(&existing).Error; err != nil {
		s.db.Create(company)
	} else {
		company.ID = existing.ID
		s.db.Model(&existing).Updates(company)
	}
}

func (s *BulkRodService) saveCompanyEmails(companyID uint64, scrapedEmailsJSON string) {
	var emails []string
	if err := json.Unmarshal([]byte(scrapedEmailsJSON), &emails); err != nil {
		return
	}
	for _, email := range emails {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}
		ce := models.CompanyEmail{CompanyID: companyID, Email: email, Source: "scrape"}
		s.db.Where("company_id = ? AND email = ?", companyID, email).FirstOrCreate(&ce)
	}
}

// ========== 工具函数 ==========

func parseKeywordList(raw string) []string {
	parts := strings.Split(raw, ",")
	seen := make(map[string]bool)
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		lower := strings.ToLower(p)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		result = append(result, p)
	}
	return result
}

func parseCountryList(raw string) []string {
	parts := strings.Split(raw, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// cities.json 解析结构
type citiesFileData struct {
	Continents []citiesContinent `json:"continents"`
}
type citiesContinent struct {
	Name      string          `json:"name"`
	NameEn    string          `json:"name_en"`
	Countries []citiesCountry `json:"countries"`
}
type citiesCountry struct {
	Name   string       `json:"name"`
	NameEn string       `json:"name_en"`
	Code   string       `json:"code"`
	Cities []citiesCity `json:"cities"`
}
type citiesCity struct {
	Name   string  `json:"name"`
	NameEn string  `json:"name_en"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
}

func loadCitiesByCountries(countryNames []string) ([]BulkRodCity, error) {
	data, err := loadCitiesJSON()
	if err != nil {
		return nil, err
	}

	want := make(map[string]bool)
	for _, name := range countryNames {
		want[strings.ToLower(strings.TrimSpace(name))] = true
	}

	var cities []BulkRodCity
	for _, continent := range data.Continents {
		for _, country := range continent.Countries {
			nameEnLower := strings.ToLower(country.NameEn)
			nameLower := strings.ToLower(country.Name)

			matched := want[nameEnLower] || want[nameLower]
			if !matched {
				for w := range want {
					if strings.Contains(nameEnLower, w) || strings.Contains(w, nameEnLower) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}

			for _, city := range country.Cities {
				cities = append(cities, BulkRodCity{
					CityNameEn:  city.NameEn,
					CityName:    city.Name,
					CountryName: country.NameEn,
					Lat:         city.Lat,
					Lng:         city.Lng,
				})
			}
			log.Printf("[BulkRod] 匹配国家: %s (%s), %d 个城市", country.NameEn, country.Name, len(country.Cities))
		}
	}
	return cities, nil
}

func loadCitiesJSON() (*citiesFileData, error) {
	workDir, _ := os.Getwd()
	exePath, _ := os.Executable()
	exeDir := ""
	if exePath != "" {
		exeDir = filepath.Dir(exePath)
	}
	for _, path := range []string{"cities.json", "./cities.json", workDir + "/cities.json", exeDir + "/cities.json"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()
		var data citiesFileData
		if err := json.NewDecoder(f).Decode(&data); err != nil {
			return nil, fmt.Errorf("解析 cities.json 失败: %w", err)
		}
		return &data, nil
	}
	return nil, fmt.Errorf("cities.json 文件未找到")
}
