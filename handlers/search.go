package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"googleMap/models"
	"googleMap/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type SearchHandler struct {
	db            *gorm.DB
	googleService *services.GoogleService
	rodService    *services.RodMapsService
	scraper       *services.ScraperService
	doubao        *services.DoubaoService
	emailService  *services.EmailService
}

func NewSearchHandler(db *gorm.DB) *SearchHandler {
	return &SearchHandler{
		db:            db,
		googleService: services.NewGoogleService(),
		rodService:    services.NewRodMapsService(),
		scraper:       services.NewScraperService(),
		doubao:        services.NewDoubaoService(),
		emailService:  services.NewEmailService(db),
	}
}

// scrapeCompanyWebsites 对有网站的公司进行爬取，提取邮箱/电话/社交链接（公用方法）
func (h *SearchHandler) scrapeCompanyWebsites(companies []models.Company) []models.Company {
	for i := range companies {
		if companies[i].Website == "" || companies[i].ScrapeSuccess {
			continue
		}
		log.Printf("[Scrape] 爬取网站 %d/%d: %s -> %s", i+1, len(companies), companies[i].Name, companies[i].Website)
		page := h.scraper.ScrapeWebsite(companies[i].Website)
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

		// 如果公司已经入库，则同步更新数据库；未入库时仅更新内存对象
		if companies[i].ID > 0 {
			updates := map[string]interface{}{
				"scraped_emails": string(emailsJSON),
				"scraped_phones": string(phonesJSON),
				"social_links":   string(socialJSON),
				"page_title":     page.Title,
				"description":    page.Description,
				"body_text":      page.BodyText,
				"scrape_success": page.Success,
				"scrape_error":   page.Error,
			}
			if len(page.Emails) > 0 && companies[i].Email != "" {
				updates["email"] = companies[i].Email
			}
			if len(page.Phones) > 0 {
				updates["phone"] = companies[i].Phone
			}
			h.db.Model(&models.Company{}).Where("id = ?", companies[i].ID).Updates(updates)

			// 邮箱一个一条记录写入 company_emails
			h.saveCompanyEmails(companies[i].ID, page.Emails, "scrape")
		}

		log.Printf("[Scrape] %s: 邮箱=%v, 电话=%v, 社交=%d", companies[i].Name, page.Emails, page.Phones, len(page.SocialLinks))
	}
	return companies
}

// saveCompanyEmails 保存公司邮箱（一个邮箱一条记录，去重）
func (h *SearchHandler) saveCompanyEmails(companyID uint64, emails []string, source string) {
	for _, email := range emails {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}
		ce := models.CompanyEmail{
			CompanyID: companyID,
			Email:     email,
			Source:    source,
		}
		// 用 companyID + email 去重
		h.db.Where("company_id = ? AND email = ?", companyID, email).FirstOrCreate(&ce)
	}
}

// analyzeCompaniesWithAI 用豆包AI分析公司网页内容，低于阈值或不符合要求的公司会被过滤
func (h *SearchHandler) analyzeCompaniesWithAI(companies []models.Company, requirement string, scoreThreshold int) []models.Company {
	requirement = strings.TrimSpace(requirement)
	if requirement == "" {
		return companies
	}
	if scoreThreshold <= 0 || scoreThreshold > 100 {
		scoreThreshold = 60
	}

	if !h.doubao.IsEnabled() {
		log.Printf("[AI] 豆包未启用，跳过AI过滤")
		return companies
	}

	passed := make([]models.Company, 0, len(companies))
	for i := range companies {
		co := companies[i]
		if strings.TrimSpace(co.Website) == "" {
			log.Printf("[AI] 跳过无网站公司: %s", co.Name)
			continue
		}

		analysis, err := h.doubao.AnalyzeCompany(
			co.Name,
			co.Website,
			co.PageTitle,
			co.Description,
			co.BodyText,
			requirement,
		)
		if err != nil {
			log.Printf("[AI] 分析失败: %s, err=%v", co.Name, err)
			continue
		}

		analysisJSON, _ := json.Marshal(analysis)
		co.CompanyIntro = analysis.CompanyIntro
		co.AIAnalysis = string(analysisJSON)
		co.AIScore = analysis.Score
		co.AIAnalyzed = true

		if analysis.IsRelevant && analysis.Score >= scoreThreshold {
			co.Filtered = false
			co.FilterReason = ""
			passed = append(passed, co)
			log.Printf("[AI] 通过: %s, score=%d", co.Name, analysis.Score)
			continue
		}

		co.Filtered = true
		co.FilterReason = analysis.Reason
		log.Printf("[AI] 过滤: %s, score=%d, reason=%s", co.Name, analysis.Score, analysis.Reason)
	}

	return passed
}

// SearchRequest 搜索请求
type SearchRequest struct {
	Latitude         float64  `json:"latitude"`
	Longitude        float64  `json:"longitude"`
	Radius           int      `json:"radius"`
	Keyword          string   `json:"keyword"`
	City             string   `json:"city"`   // 城市名（Rod模式下可直接输入城市名搜索）
	Cities           []string `json:"cities"` // 多个城市（Rod并发模式）
	Address          string   `json:"address"`
	Mode             string   `json:"mode"` // "api" / "rod" / "rod_concurrent"
	MaxCount         int      `json:"max_count"`
	Concurrent       int      `json:"concurrent"`         // 并发数（rod_concurrent模式，默认2）
	AIRequirement    string   `json:"ai_requirement"`     // AI分析要求
	AIScoreThreshold int      `json:"ai_score_threshold"` // AI评分阈值(0-100)
}

// Search 搜索附近商家（支持 API 和 Rod 两种模式）
func (h *SearchHandler) Search(c *gin.Context) {
	var req SearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	if req.Radius <= 0 {
		req.Radius = 50000
	}
	if req.Radius > 50000 {
		req.Radius = 50000
	}
	if req.Mode == "" {
		req.Mode = "api"
	}

	if req.Mode == "rod_concurrent" {
		h.searchByRodConcurrent(c, req)
		return
	}

	if req.Mode == "rod" {
		h.searchByRod(c, req)
		return
	}

	h.searchByAPI(c, req)
}

// searchByAPI 使用 Google API 搜索
func (h *SearchHandler) searchByAPI(c *gin.Context, req SearchRequest) {
	googleReq := services.NearbySearchRequest{
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		Radius:    req.Radius,
		Keyword:   req.Keyword,
	}
	result, err := h.googleService.NearbySearch(googleReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	record := models.SearchRecord{
		Source:       "map",
		Keyword:      req.Keyword,
		Latitude:     req.Latitude,
		Longitude:    req.Longitude,
		Radius:       req.Radius,
		Address:      req.Address,
		TotalResults: len(result.Results),
		Status:       1,
	}
	if err := h.db.Create(&record).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "保存搜索记录失败"})
		return
	}

	companies := make([]models.Company, 0, len(result.Results))
	for _, place := range result.Results {
		detail, err := h.googleService.GetPlaceDetail(place.PlaceID)
		if err != nil {
			company := services.ConvertBasicToCompany(&place, "map")
			if upsertErr := h.upsertCompany(&company); upsertErr != nil {
				log.Printf("[API] 写库失败: name=%s place_id=%s err=%v", company.Name, company.PlaceID, upsertErr)
			}
			companies = append(companies, company)
			continue
		}
		company := services.ConvertDetailToCompany(detail, "map")
		if upsertErr := h.upsertCompany(&company); upsertErr != nil {
			log.Printf("[API] 写库失败: name=%s place_id=%s err=%v", company.Name, company.PlaceID, upsertErr)
		}
		companies = append(companies, company)
	}

	record.TotalResults = len(companies)
	h.db.Save(&record)

	// 自动爬取有网站的商家
	companies = h.scrapeCompanyWebsites(companies)

	// AI分析公司网页
	if req.AIRequirement != "" {
		companies = h.analyzeCompaniesWithAI(companies, req.AIRequirement, req.AIScoreThreshold)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": gin.H{
			"record":          record,
			"companies":       companies,
			"next_page_token": result.NextPageToken,
			"total":           len(companies),
			"mode":            "api",
		},
	})
}

// searchByRod 使用 Rod 无头浏览器直接爬取 Google Maps
func (h *SearchHandler) searchByRod(c *gin.Context, req SearchRequest) {
	// 捕获 Rod 的 panic（Rod 内部 Must* 方法使用 panic）
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Rod] panic recovered: %v", r)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Rod异常: %v", r)})
		}
	}()

	if req.MaxCount <= 0 {
		req.MaxCount = 60
	}
	if req.AIRequirement != "" && !h.doubao.IsEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "豆包AI未启用，请先在config.json开启 doubao.enabled 并配置可用API Key"})
		return
	}

	// 创建搜索记录（状态：进行中）
	record := models.SearchRecord{
		Source:    "rod",
		Keyword:   req.Keyword,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		Radius:    req.Radius,
		Address:   req.Address,
		Status:    0, // 进行中
	}
	if req.City != "" && req.Address == "" {
		record.Address = req.City
	}

	// Rod模式下如果提供了城市名，优先使用城市名搜索，忽略坐标（因为很多商家没有精确坐标，但有城市信息）
	if req.City != "" {
		req.Longitude = 0
		req.Latitude = 0
	}
	if err := h.db.Create(&record).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "保存搜索记录失败"})
		return
	}

	// 调用 Rod 搜索
	rodReq := services.RodSearchRequest{
		Keyword:   req.Keyword,
		City:      req.City,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		Radius:    req.Radius,
		MaxCount:  req.MaxCount,
	}
	companies, err := h.rodService.SearchNearby(rodReq)
	if err != nil {
		record.Status = 2
		record.ErrorMsg = err.Error()
		h.db.Save(&record)
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Rod搜索失败: " + err.Error()})
		return
	}

	// 自动爬取有网站的商家，提取邮箱/电话/社交链接
	companies = h.scrapeCompanyWebsites(companies)

	// AI分析公司网页
	if req.AIRequirement != "" {
		companies = h.analyzeCompaniesWithAI(companies, req.AIRequirement, req.AIScoreThreshold)
	}

	// 通过筛选后再入库
	savedCompanies := make([]models.Company, 0, len(companies))
	for i := range companies {
		companies[i].SearchRecordID = record.ID
		if err := h.upsertCompany(&companies[i]); err != nil {
			log.Printf("[Rod] 写库失败: name=%s place_id=%s err=%v", companies[i].Name, companies[i].PlaceID, err)
			continue
		}
		savedCompanies = append(savedCompanies, companies[i])
	}

	record.TotalResults = len(savedCompanies)
	record.Status = 1
	h.db.Save(&record)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": gin.H{
			"record":    record,
			"companies": savedCompanies,
			"total":     len(savedCompanies),
			"mode":      "rod",
		},
	})
}

// searchByRodConcurrent 并发搜索多个城市
func (h *SearchHandler) searchByRodConcurrent(c *gin.Context, req SearchRequest) {
	// 捕获 Rod 的 panic
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Rod-并发] panic recovered: %v", r)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Rod并发异常: %v", r)})
		}
	}()

	cities := req.Cities
	// 兼容：如果 cities 为空但 city 有值，用逗号分隔
	if len(cities) == 0 && req.City != "" {
		for _, c := range strings.Split(req.City, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				cities = append(cities, c)
			}
		}
	}
	if len(cities) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "请输入至少一个城市"})
		return
	}
	if len(cities) > 10 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "最多同时搜索10个城市"})
		return
	}

	if req.MaxCount <= 0 {
		req.MaxCount = 30
	}
	if req.Concurrent <= 0 {
		req.Concurrent = 1
	}
	if req.AIRequirement != "" && !h.doubao.IsEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "豆包AI未启用，请先在config.json开启 doubao.enabled 并配置可用API Key"})
		return
	}

	// 先创建搜索记录，确保写库时 search_record_id 可用
	record := models.SearchRecord{
		Source:       "rod_concurrent",
		Keyword:      req.Keyword,
		Address:      strings.Join(cities, ", "),
		Status:       0,
		TotalCities:  len(cities),
		CurrentCity:  cities[0],
		CurrentIndex: 0,
		FetchedCount: 0,
	}
	if err := h.db.Create(&record).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "保存搜索记录失败: " + err.Error()})
		return
	}

	log.Printf("[Rod-并发] 开始搜索 %d 个城市, 关键词: %s, 并发数: %d", len(cities), req.Keyword, req.Concurrent)

	// 调用并发搜索（带进度回调）
	rodResults := h.rodService.ConcurrentSearch(services.RodConcurrentRequest{
		Keyword:    req.Keyword,
		Cities:     cities,
		MaxCount:   req.MaxCount,
		Concurrent: req.Concurrent,
		OnProgress: func(city string, index, total, fetchedCount int) {
			h.updateProgress(record.ID, city, index+1, total, fetchedCount)
		},
	})

	// 汇总所有公司
	allCompanies := make([]models.Company, 0)
	cityResults := make([]gin.H, 0, len(rodResults))

	for _, r := range rodResults {
		cityResult := gin.H{
			"city":     r.City,
			"total":    len(r.Companies),
			"duration": r.Duration,
			"error":    r.Error,
		}
		cityResults = append(cityResults, cityResult)

		if r.Error != "" {
			continue
		}

		allCompanies = append(allCompanies, r.Companies...)
	}

	// 自动爬取有网站的商家
	allCompanies = h.scrapeCompanyWebsites(allCompanies)

	// AI分析
	if req.AIRequirement != "" {
		allCompanies = h.analyzeCompaniesWithAI(allCompanies, req.AIRequirement, req.AIScoreThreshold)
	}

	// 通过筛选后再入库
	savedCompanies := make([]models.Company, 0, len(allCompanies))
	for i := range allCompanies {
		allCompanies[i].SearchRecordID = record.ID
		if err := h.upsertCompany(&allCompanies[i]); err != nil {
			log.Printf("[Rod-并发] 写库失败: city=%s name=%s place_id=%s err=%v", record.Address, allCompanies[i].Name, allCompanies[i].PlaceID, err)
			continue
		}
		savedCompanies = append(savedCompanies, allCompanies[i])
	}

	record.TotalResults = len(savedCompanies)
	record.Status = 1
	h.db.Save(&record)

	log.Printf("[Rod-并发] 全部完成: %d 个城市, 抓取 %d 家, 入库 %d 家", len(cities), len(allCompanies), len(savedCompanies))

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": gin.H{
			"record":        record,
			"companies":     savedCompanies,
			"total":         len(savedCompanies),
			"crawled_total": len(allCompanies),
			"city_results":  cityResults,
			"mode":          "rod_concurrent",
		},
	})
}

// StopSearch 停止所有正在进行的搜索任务
func (h *SearchHandler) StopSearch(c *gin.Context) {
	if h.rodService != nil {
		h.rodService.Stop()
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "已发送停止信号"})
}

// GetTaskProgress 获取任务进度
func (h *SearchHandler) GetTaskProgress(c *gin.Context) {
	// 查询最近的进行中的任务
	var record models.SearchRecord
	err := h.db.Where("status = ? AND source IN (?, ?, ?)", 0, "rod", "rod_concurrent", "map").
		Order("created_at DESC").First(&record).Error

	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"running": false,
				"message": "没有正在进行的任务",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"running":       true,
			"record_id":     record.ID,
			"keyword":       record.Keyword,
			"address":       record.Address,
			"current_city":  record.CurrentCity,
			"current_index": record.CurrentIndex,
			"total_cities":  record.TotalCities,
			"fetched_count": record.FetchedCount,
			"status":        record.Status,
			"source":        record.Source,
		},
	})
}

// updateProgress 更新任务进度
func (h *SearchHandler) updateProgress(recordID uint64, currentCity string, currentIndex, totalCities, fetchedCount int) {
	h.db.Model(&models.SearchRecord{}).Where("id = ?", recordID).Updates(map[string]interface{}{
		"current_city":  currentCity,
		"current_index": currentIndex,
		"total_cities":  totalCities,
		"fetched_count": fetchedCount,
	})
}

func (h *SearchHandler) SearchNextPage(c *gin.Context) {
	var req struct {
		PageToken string `json:"page_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误"})
		return
	}

	searchReq := services.NearbySearchRequest{PageToken: req.PageToken}
	result, err := h.googleService.NearbySearch(searchReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	companies := make([]models.Company, 0, len(result.Results))
	for _, place := range result.Results {
		detail, err := h.googleService.GetPlaceDetail(place.PlaceID)
		if err != nil {
			company := services.ConvertBasicToCompany(&place, "map")
			if upsertErr := h.upsertCompany(&company); upsertErr != nil {
				log.Printf("[API] 写库失败: name=%s place_id=%s err=%v", company.Name, company.PlaceID, upsertErr)
			}
			companies = append(companies, company)
			continue
		}
		company := services.ConvertDetailToCompany(detail, "map")
		if upsertErr := h.upsertCompany(&company); upsertErr != nil {
			log.Printf("[API] 写库失败: name=%s place_id=%s err=%v", company.Name, company.PlaceID, upsertErr)
		}
		companies = append(companies, company)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"companies":       companies,
			"next_page_token": result.NextPageToken,
			"total":           len(companies),
		},
	})
}

// GetPlaceDetail 获取单个商家详情
func (h *SearchHandler) GetPlaceDetail(c *gin.Context) {
	placeID := c.Query("place_id")
	if placeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "place_id 不能为空"})
		return
	}

	detail, err := h.googleService.GetPlaceDetail(placeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": detail})
}

// GetRecords 获取搜索记录列表
func (h *SearchHandler) GetRecords(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var total int64
	h.db.Model(&models.SearchRecord{}).Count(&total)

	var records []models.SearchRecord
	h.db.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&records)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"list":      records,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetRecordDetail 获取搜索记录详情（含商家列表）
func (h *SearchHandler) GetRecordDetail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
		return
	}

	var record models.SearchRecord
	if err := h.db.First(&record, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "记录不存在"})
		return
	}

	// 查询该搜索记录关联的所有公司
	var companies []models.Company
	h.db.Where("search_record_id = ?", id).Order("created_at DESC").Find(&companies)

	// 查询每个公司的所有邮箱
	for i := range companies {
		var emails []models.CompanyEmail
		h.db.Where("company_id = ?", companies[i].ID).Find(&emails)
		if len(emails) > 0 {
			emailList := make([]string, 0, len(emails))
			for _, e := range emails {
				emailList = append(emailList, e.Email)
			}
			companies[i].ScrapedEmails = strings.Join(emailList, ",")
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"record":    record,
			"companies": companies,
			"total":     len(companies),
		},
	})
}

// GetCompanies 获取公司列表（支持来源过滤）
func (h *SearchHandler) GetCompanies(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	source := c.DefaultQuery("source", "")
	keyword := c.DefaultQuery("keyword", "")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := h.db.Model(&models.Company{})
	if source != "" {
		query = query.Where("source = ?", source)
	}
	if keyword != "" {
		query = query.Where("name LIKE ? OR email LIKE ? OR domain LIKE ?",
			"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}

	var total int64
	query.Count(&total)

	var companies []models.Company
	query.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&companies)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"list":      companies,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// DeleteRecord 删除搜索记录
func (h *SearchHandler) DeleteRecord(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
		return
	}

	if err := h.db.Delete(&models.SearchRecord{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "删除失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "删除成功"})
}

// upsertCompany 按 place_id 去重插入或更新公司
func (h *SearchHandler) upsertCompany(company *models.Company) error {
	if company.PlaceID == "" {
		if err := h.db.Create(company).Error; err != nil {
			return err
		}
		if company.Email != "" && company.ID > 0 {
			h.saveCompanyEmails(company.ID, []string{company.Email}, "google")
		}
		h.emailService.TrySendMarketingEmail(company)
		return nil
	}
	var existing models.Company
	err := h.db.Where("place_id = ?", company.PlaceID).First(&existing).Error
	if err != nil {
		if createErr := h.db.Create(company).Error; createErr != nil {
			return createErr
		}
	} else {
		company.ID = existing.ID
		if updateErr := h.db.Model(&existing).Updates(company).Error; updateErr != nil {
			return updateErr
		}
	}
	// 保存Google Maps上的邮箱到company_emails
	if company.Email != "" && company.ID > 0 {
		h.saveCompanyEmails(company.ID, []string{company.Email}, "google")
	}
	h.emailService.TrySendMarketingEmail(company)
	return nil
}

// AIAnalyze 手动触发AI分析（已禁用）
func (h *SearchHandler) AIAnalyze(c *gin.Context) {
	// AI智能分析模块已禁用
	c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "AI智能分析模块已禁用"})
}

// GetCompanyEmails 获取公司的所有邮箱
func (h *SearchHandler) GetCompanyEmails(c *gin.Context) {
	companyID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
		return
	}

	var emails []models.CompanyEmail
	h.db.Where("company_id = ?", companyID).Find(&emails)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": emails})
}

// GetAllEmails 获取所有邮箱（去重）
func (h *SearchHandler) GetAllEmails(c *gin.Context) {
	source := c.DefaultQuery("source", "")
	query := h.db
	if source != "" {
		query = query.Joins("JOIN companies ON companies.id = company_emails.company_id").
			Where("companies.source = ?", source)
	}
	var emails []models.CompanyEmail
	query.Distinct("email").Preload("Company").Find(&emails)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": emails, "total": len(emails)})
}
