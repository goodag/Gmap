package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"googleMap/models"
	"googleMap/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ScraperHandler struct {
	db      *gorm.DB
	scraper *services.ScraperService
	filter  *services.ContentFilter
}

func NewScraperHandler(db *gorm.DB) *ScraperHandler {
	return &ScraperHandler{
		db:      db,
		scraper: services.NewScraperService(),
		filter:  services.NewContentFilter(),
	}
}

// ScrapeSingle 爬取单个公司网站
func (h *ScraperHandler) ScrapeSingle(c *gin.Context) {
	var req struct {
		CompanyID uint64                 `json:"company_id" binding:"required"`
		URL       string                 `json:"url"`
		Filter    *services.FilterConfig `json:"filter"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	url := req.URL
	if url == "" {
		var company models.Company
		if err := h.db.First(&company, req.CompanyID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "公司不存在"})
			return
		}
		url = company.Website
	}

	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "公司无网站信息"})
		return
	}

	// 爬取
	page := h.scraper.ScrapeWebsite(url)

	// 过滤
	filtered := false
	filterReason := ""
	if req.Filter != nil {
		result := h.filter.FilterScrapedContent(page, req.Filter)
		if !result.Passed {
			filtered = true
			filterReason = result.Reason
		}
	}

	// 更新公司的爬取信息
	emailsJSON, _ := json.Marshal(page.Emails)
	phonesJSON, _ := json.Marshal(page.Phones)
	socialJSON, _ := json.Marshal(page.SocialLinks)

	updates := map[string]interface{}{
		"scraped_emails": string(emailsJSON),
		"scraped_phones": string(phonesJSON),
		"social_links":   string(socialJSON),
		"page_title":     page.Title,
		"description":    page.Description,
		"body_text":      page.BodyText,
		"scrape_success": page.Success,
		"scrape_error":   page.Error,
		"filtered":       filtered,
		"filter_reason":  filterReason,
	}
	if len(page.Emails) > 0 {
		updates["email"] = page.Emails[0]
	}
	if len(page.Phones) > 0 {
		var company models.Company
		if h.db.First(&company, req.CompanyID).Error == nil && company.Phone == "" {
			updates["phone"] = page.Phones[0]
		}
	}

	h.db.Model(&models.Company{}).Where("id = ?", req.CompanyID).Updates(updates)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"page":          page,
			"filtered":      filtered,
			"filter_reason": filterReason,
		},
	})
}

// ScrapeBatch 批量爬取公司网站
func (h *ScraperHandler) ScrapeBatch(c *gin.Context) {
	var req struct {
		CompanyIDs []uint64               `json:"company_ids" binding:"required"`
		Filter     *services.FilterConfig `json:"filter"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	var companies []models.Company
	if err := h.db.Where("id IN ? AND website != ''", req.CompanyIDs).Find(&companies).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "查询公司失败"})
		return
	}

	type ResultItem struct {
		CompanyID    uint64                `json:"company_id"`
		CompanyName  string                `json:"company_name"`
		Page         *services.ScrapedPage `json:"page"`
		Filtered     bool                  `json:"filtered"`
		FilterReason string                `json:"filter_reason"`
	}

	results := make([]ResultItem, 0, len(companies))
	scraped, filteredCount := 0, 0

	for _, co := range companies {
		if req.Filter != nil {
			bizFilter := h.filter.FilterBusiness(
				co.Name, co.Email, co.Phone, co.Website, co.Types,
				co.Rating, co.UserRatingsTotal, req.Filter,
			)
			if !bizFilter.Passed {
				filteredCount++
				results = append(results, ResultItem{
					CompanyID:    co.ID,
					CompanyName:  co.Name,
					Filtered:     true,
					FilterReason: "基础过滤: " + bizFilter.Reason,
				})
				continue
			}
		}

		page := h.scraper.ScrapeWebsite(co.Website)
		scraped++

		filtered := false
		filterReason := ""
		if req.Filter != nil {
			contentFilter := h.filter.FilterScrapedContent(page, req.Filter)
			if !contentFilter.Passed {
				filtered = true
				filterReason = "内容过滤: " + contentFilter.Reason
				filteredCount++
			}
		}

		// 更新公司信息
		emailsJSON, _ := json.Marshal(page.Emails)
		phonesJSON, _ := json.Marshal(page.Phones)
		socialJSON, _ := json.Marshal(page.SocialLinks)

		companyUpdates := map[string]interface{}{
			"scraped_emails": string(emailsJSON),
			"scraped_phones": string(phonesJSON),
			"social_links":   string(socialJSON),
			"page_title":     page.Title,
			"description":    page.Description,
			"body_text":      page.BodyText,
			"scrape_success": page.Success,
			"scrape_error":   page.Error,
			"filtered":       filtered,
			"filter_reason":  filterReason,
		}
		if len(page.Emails) > 0 && co.Email == "" {
			companyUpdates["email"] = page.Emails[0]
		}
		h.db.Model(&models.Company{}).Where("id = ?", co.ID).Updates(companyUpdates)

		results = append(results, ResultItem{
			CompanyID:    co.ID,
			CompanyName:  co.Name,
			Page:         page,
			Filtered:     filtered,
			FilterReason: filterReason,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"results":        results,
			"total":          len(companies),
			"scraped":        scraped,
			"filtered_count": filteredCount,
		},
	})
}

// GetScrapeResults 获取已爬取的公司列表
func (h *ScraperHandler) GetScrapeResults(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}

	query := h.db.Where("scrape_success = ?", true)
	if c.Query("not_filtered") == "true" {
		query = query.Where("filtered = ?", false)
	}

	var total int64
	query.Model(&models.Company{}).Count(&total)

	var companies []models.Company
	query.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&companies)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"list":  companies,
			"total": total,
		},
	})
}
