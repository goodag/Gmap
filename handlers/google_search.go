package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"googleMap/config"
	"googleMap/models"
	"googleMap/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type GoogleSearchHandler struct {
	db     *gorm.DB
	svc    *services.GoogleSearchService
	filter *services.ContentFilter
}

func NewGoogleSearchHandler(db *gorm.DB) *GoogleSearchHandler {
	cfg := config.Get()
	return &GoogleSearchHandler{
		db:     db,
		svc:    services.NewGoogleSearchService(cfg.Google.APIKey, cfg.Google.CustomSearchID),
		filter: services.NewContentFilter(),
	}
}

// Search 启动一次谷歌搜索+爬取任务
func (h *GoogleSearchHandler) Search(c *gin.Context) {
	var req services.SearchAndScrapeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}
	if req.Pages == 0 {
		req.Pages = 1
	}

	// 创建搜索记录
	record := models.SearchRecord{
		Source:  "google",
		Keyword: req.Query,
		Pages:   req.Pages,
		Status:  0,
	}
	if err := h.db.Create(&record).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "创建记录失败"})
		return
	}

	// 执行搜索+爬取
	results, err := h.svc.RunSearchAndScrape(&req, nil)
	if err != nil {
		h.db.Model(&record).Updates(map[string]interface{}{
			"status":    2,
			"error_msg": err.Error(),
		})
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "搜索失败: " + err.Error(), "record_id": record.ID})
		return
	}

	// 保存结果到 companies 表
	successCount := 0
	for _, r := range results {
		if r.Filtered {
			continue
		}
		company := models.Company{
			Source:        "google",
			PlaceID:       r.Domain,
			Name:          r.GoogleTitle,
			Website:       r.URL,
			Domain:        r.Domain,
			GoogleTitle:   r.GoogleTitle,
			GoogleSnippet: r.GoogleSnippet,
			Filtered:      r.Filtered,
			FilterReason:  r.FilterReason,
		}
		if r.Page != nil {
			company.PageTitle = r.Page.Title
			company.Description = r.Page.Description
			company.ScrapeSuccess = r.Page.Success
			company.ScrapeError = r.Page.Error
			if len(r.Page.Emails) > 0 {
				emailsJSON, _ := json.Marshal(r.Page.Emails)
				company.ScrapedEmails = string(emailsJSON)
				company.Email = r.Page.Emails[0]
			}
			if len(r.Page.Phones) > 0 {
				phonesJSON, _ := json.Marshal(r.Page.Phones)
				company.ScrapedPhones = string(phonesJSON)
				company.Phone = r.Page.Phones[0]
			}
			if len(r.Page.SocialLinks) > 0 {
				socialJSON, _ := json.Marshal(r.Page.SocialLinks)
				company.SocialLinks = string(socialJSON)
			}
			company.BodyText = r.Page.BodyText
			if r.Page.Success {
				successCount++
			}
		}
		h.upsertCompany(&company)
	}

	// 更新搜索记录状态
	h.db.Model(&record).Updates(map[string]interface{}{
		"status":        1,
		"total_results": successCount,
	})

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"record_id":     record.ID,
			"total_found":   len(results),
			"total_scraped": successCount,
			"results":       results,
		},
	})
}

// GetTasks 获取搜索记录列表（分页）
func (h *GoogleSearchHandler) GetTasks(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var total int64
	var records []models.SearchRecord
	h.db.Model(&models.SearchRecord{}).Where("source = ?", "google").Count(&total)
	h.db.Where("source = ?", "google").Order("id DESC").Offset((page-1)*pageSize).Limit(pageSize).Find(&records)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"total": total,
			"list":  records,
		},
	})
}

// GetTaskResults 获取谷歌搜索来源的公司列表
func (h *GoogleSearchHandler) GetTaskResults(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	onlyEmail := c.Query("only_email") == "1"
	onlySuccess := c.Query("only_success") == "1"
	excludeFiltered := c.Query("exclude_filtered") == "1"

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}

	query := h.db.Where("source = ?", "google")
	if onlyEmail {
		query = query.Where("email != '' OR scraped_emails != '[]'")
	}
	if onlySuccess {
		query = query.Where("scrape_success = 1")
	}
	if excludeFiltered {
		query = query.Where("filtered = 0")
	}

	var total int64
	query.Model(&models.Company{}).Count(&total)

	var companies []models.Company
	query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&companies)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"total": total,
			"list":  companies,
		},
	})
}

// DeleteTask 删除搜索记录
func (h *GoogleSearchHandler) DeleteTask(c *gin.Context) {
	taskID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "记录ID无效"})
		return
	}

	h.db.Delete(&models.SearchRecord{}, taskID)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "删除成功"})
}

// ExportEmails 导出谷歌搜索来源下所有邮箱（去重）
func (h *GoogleSearchHandler) ExportEmails(c *gin.Context) {
	var companies []models.Company
	h.db.Where("source = ? AND (email != '' OR scraped_emails != '[]') AND filtered = 0", "google").
		Select("email, scraped_emails, website, name").Find(&companies)

	emailSet := make(map[string]bool)
	type EmailEntry struct {
		Email  string `json:"email"`
		Source string `json:"source"`
		Title  string `json:"title"`
	}
	entries := make([]EmailEntry, 0)

	for _, co := range companies {
		// 先检查主邮箱
		if co.Email != "" && !emailSet[co.Email] {
			emailSet[co.Email] = true
			entries = append(entries, EmailEntry{Email: co.Email, Source: co.Website, Title: co.Name})
		}
		// 再检查爬取到的邮箱列表
		if co.ScrapedEmails != "" && co.ScrapedEmails != "[]" {
			var emails []string
			if err := json.Unmarshal([]byte(co.ScrapedEmails), &emails); err == nil {
				for _, email := range emails {
					if !emailSet[email] {
						emailSet[email] = true
						entries = append(entries, EmailEntry{Email: email, Source: co.Website, Title: co.Name})
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"total":  len(entries),
			"emails": entries,
		},
	})
}

// upsertCompany 按 place_id 去重插入或更新
func (h *GoogleSearchHandler) upsertCompany(company *models.Company) {
	if company.PlaceID == "" {
		h.db.Create(company)
		return
	}
	var existing models.Company
	err := h.db.Where("place_id = ?", company.PlaceID).First(&existing).Error
	if err != nil {
		h.db.Create(company)
	} else {
		company.ID = existing.ID
		h.db.Model(&existing).Updates(company)
	}
}
