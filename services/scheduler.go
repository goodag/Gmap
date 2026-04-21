package services

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"googleMap/config"
	"googleMap/models"

	"gorm.io/gorm"
)

// SchedulerService 定时任务调度器
type SchedulerService struct {
	db            *gorm.DB
	googleSvc     *GoogleService
	googleSearch  *GoogleSearchService
	scraperSvc    *ScraperService
	filterSvc     *ContentFilter
	rodSvc        *RodMapsService
	stopChans     map[uint64]chan struct{} // taskID -> stopChan
	mu            sync.Mutex
	running       bool
}

// MapTaskParams 地图搜索任务参数
type MapTaskParams struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Radius    int     `json:"radius"`
	Keyword   string  `json:"keyword"`
	Address   string  `json:"address"`
}

// GoogleTaskParams 谷歌搜索任务参数
type GoogleTaskParams struct {
	Query    string        `json:"query"`
	Location string        `json:"location"`
	Language string        `json:"language"`
	Pages    int           `json:"pages"`
	Filter   *FilterConfig `json:"filter,omitempty"`
}

func NewSchedulerService(db *gorm.DB) *SchedulerService {
	cfg := config.Get()
	return &SchedulerService{
		db:           db,
		googleSvc:    NewGoogleService(),
		googleSearch: NewGoogleSearchService(cfg.Google.APIKey, cfg.Google.CustomSearchID),
		scraperSvc:   NewScraperService(),
		filterSvc:    NewContentFilter(),
		rodSvc:       NewRodMapsService(),
		stopChans:    make(map[uint64]chan struct{}),
	}
}

// Start 启动调度器，加载所有已启用的任务
func (s *SchedulerService) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	var tasks []models.CronTask
	s.db.Where("enabled = ?", true).Find(&tasks)

	for _, task := range tasks {
		s.startTask(task)
	}
	log.Printf("⏰ 定时任务调度器已启动，加载了 %d 个任务", len(tasks))
}

// Stop 停止所有任务
func (s *SchedulerService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.stopChans {
		close(ch)
		delete(s.stopChans, id)
	}
	s.running = false
	log.Println("⏰ 定时任务调度器已停止")
}

// AddTask 添加并启动一个新任务
func (s *SchedulerService) AddTask(task models.CronTask) error {
	if err := s.db.Create(&task).Error; err != nil {
		return err
	}
	if task.Enabled {
		s.startTask(task)
	}
	return nil
}

// UpdateTask 更新任务配置
func (s *SchedulerService) UpdateTask(task models.CronTask) error {
	// 先停止旧的
	s.stopTask(task.ID)
	if err := s.db.Save(&task).Error; err != nil {
		return err
	}
	if task.Enabled {
		s.startTask(task)
	}
	return nil
}

// RemoveTask 删除任务
func (s *SchedulerService) RemoveTask(taskID uint64) error {
	s.stopTask(taskID)
	return s.db.Delete(&models.CronTask{}, taskID).Error
}

// ToggleTask 启用/禁用任务
func (s *SchedulerService) ToggleTask(taskID uint64, enabled bool) error {
	if enabled {
		var task models.CronTask
		if err := s.db.First(&task, taskID).Error; err != nil {
			return err
		}
		task.Enabled = true
		s.db.Save(&task)
		s.startTask(task)
	} else {
		s.stopTask(taskID)
		s.db.Model(&models.CronTask{}).Where("id = ?", taskID).Update("enabled", false)
	}
	return nil
}

// RunOnce 立即执行一次任务
func (s *SchedulerService) RunOnce(taskID uint64) error {
	var task models.CronTask
	if err := s.db.First(&task, taskID).Error; err != nil {
		return err
	}
	go s.executeTask(task)
	return nil
}

// startTask 启动单个定时任务的循环
func (s *SchedulerService) startTask(task models.CronTask) {
	interval, err := parseCronExpr(task.CronExpr)
	if err != nil {
		log.Printf("❌ 任务[%d] %s 的 cron 表达式无效: %s", task.ID, task.Name, err.Error())
		return
	}

	s.mu.Lock()
	// 如果已在运行，先停止
	if ch, ok := s.stopChans[task.ID]; ok {
		close(ch)
	}
	stopCh := make(chan struct{})
	s.stopChans[task.ID] = stopCh
	s.mu.Unlock()

	go func() {
		log.Printf("⏰ 任务[%d] %s 已启动，间隔: %v", task.ID, task.Name, interval)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				log.Printf("⏰ 任务[%d] %s 已停止", task.ID, task.Name)
				return
			case <-ticker.C:
				s.executeTask(task)
			}
		}
	}()
}

// stopTask 停止单个任务
func (s *SchedulerService) stopTask(taskID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.stopChans[taskID]; ok {
		close(ch)
		delete(s.stopChans, taskID)
	}
}

// executeTask 执行一次任务
func (s *SchedulerService) executeTask(task models.CronTask) {
	log.Printf("▶️ 执行任务[%d] %s (类型: %s)", task.ID, task.Name, task.TaskType)

	now := time.Now()
	var errMsg string

	switch task.TaskType {
	case "map":
		errMsg = s.executeMapTask(task)
	case "google":
		errMsg = s.executeGoogleTask(task)
	case "rod":
		errMsg = s.executeRodTask(task)
	default:
		errMsg = fmt.Sprintf("未知任务类型: %s", task.TaskType)
	}

	// 更新任务状态
	updates := map[string]interface{}{
		"last_run_at": now,
		"run_count":   gorm.Expr("run_count + 1"),
	}
	if errMsg == "" {
		updates["last_status"] = 1
		updates["last_error"] = ""
	} else {
		updates["last_status"] = 2
		updates["last_error"] = errMsg
		log.Printf("❌ 任务[%d] 执行失败: %s", task.ID, errMsg)
	}
	s.db.Model(&models.CronTask{}).Where("id = ?", task.ID).Updates(updates)
}

// executeMapTask 执行地图搜索任务
func (s *SchedulerService) executeMapTask(task models.CronTask) string {
	var params MapTaskParams
	if err := json.Unmarshal([]byte(task.Params), &params); err != nil {
		return fmt.Sprintf("解析参数失败: %v", err)
	}

	// 调用 Google Places API
	result, err := s.googleSvc.NearbySearch(NearbySearchRequest{
		Latitude:  params.Latitude,
		Longitude: params.Longitude,
		Radius:    params.Radius,
		Keyword:   params.Keyword,
	})
	if err != nil {
		return err.Error()
	}

	// 创建搜索记录
	record := models.SearchRecord{
		Source:       "map",
		Keyword:      params.Keyword,
		Latitude:     params.Latitude,
		Longitude:    params.Longitude,
		Radius:       params.Radius,
		Address:      params.Address,
		TotalResults: len(result.Results),
		Status:       1,
	}
	s.db.Create(&record)

	// 保存商家（去重写入companies）
	for _, place := range result.Results {
		detail, err := s.googleSvc.GetPlaceDetail(place.PlaceID)
		if err != nil {
			// 用基本信息
			company := convertBasicToCompany(&place, "map")
			s.upsertCompany(&company)
			continue
		}
		company := convertDetailToCompany(detail, "map")
		s.upsertCompany(&company)
	}

	log.Printf("✅ 地图任务[%d] 完成，找到 %d 家商家", task.ID, len(result.Results))
	return ""
}

// executeGoogleTask 执行谷歌搜索任务
func (s *SchedulerService) executeGoogleTask(task models.CronTask) string {
	var params GoogleTaskParams
	if err := json.Unmarshal([]byte(task.Params), &params); err != nil {
		return fmt.Sprintf("解析参数失败: %v", err)
	}

	// 创建搜索记录
	record := models.SearchRecord{
		Source:   "google",
		Keyword:  params.Query,
		Location: params.Location,
		Language: params.Language,
		Pages:    params.Pages,
		Status:   0,
	}
	s.db.Create(&record)

	// 执行搜索+爬取
	req := &SearchAndScrapeRequest{
		Query:    params.Query,
		Location: params.Location,
		Language: params.Language,
		Pages:    params.Pages,
		Filter:   params.Filter,
	}
	results, err := s.googleSearch.RunSearchAndScrape(req, nil)
	if err != nil {
		s.db.Model(&record).Updates(map[string]interface{}{"status": 2, "error_msg": err.Error()})
		return err.Error()
	}

	// 保存到 companies 表
	successCount := 0
	for _, r := range results {
		if r.Filtered {
			continue
		}
		company := models.Company{
			Source:        "google",
			PlaceID:       r.Domain, // 用域名做唯一键
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
		s.upsertCompany(&company)
	}

	s.db.Model(&record).Updates(map[string]interface{}{
		"status":        1,
		"total_results": successCount,
	})

	log.Printf("✅ 谷歌任务[%d] 完成，找到 %d 个结果，成功爬取 %d 个", task.ID, len(results), successCount)
	return ""
}

// RodTaskParams Rod爬取任务参数
type RodTaskParams struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Radius    int     `json:"radius"`
	Keyword   string  `json:"keyword"`
	Address   string  `json:"address"`
	MaxCount  int     `json:"max_count"`
}

// executeRodTask 执行 Rod 爬取任务
func (s *SchedulerService) executeRodTask(task models.CronTask) string {
	var params RodTaskParams
	if err := json.Unmarshal([]byte(task.Params), &params); err != nil {
		return fmt.Sprintf("解析参数失败: %v", err)
	}

	if params.MaxCount <= 0 {
		params.MaxCount = 60
	}

	// 创建搜索记录
	record := models.SearchRecord{
		Source:    "rod",
		Keyword:   params.Keyword,
		Latitude:  params.Latitude,
		Longitude: params.Longitude,
		Radius:    params.Radius,
		Address:   params.Address,
		Status:    0,
	}
	s.db.Create(&record)

	companies, err := s.rodSvc.SearchNearby(RodSearchRequest{
		Keyword:   params.Keyword,
		Latitude:  params.Latitude,
		Longitude: params.Longitude,
		Radius:    params.Radius,
		MaxCount:  params.MaxCount,
	})
	if err != nil {
		s.db.Model(&record).Updates(map[string]interface{}{"status": 2, "error_msg": err.Error()})
		return err.Error()
	}

	for i := range companies {
		s.upsertCompany(&companies[i])
	}

	s.db.Model(&record).Updates(map[string]interface{}{
		"status":        1,
		"total_results": len(companies),
	})

	log.Printf("✅ Rod任务[%d] 完成，找到 %d 家商家", task.ID, len(companies))
	return ""
}

// upsertCompany 按 place_id 去重插入或更新公司
func (s *SchedulerService) upsertCompany(company *models.Company) {
	if company.PlaceID == "" {
		s.db.Create(company)
		return
	}
	var existing models.Company
	err := s.db.Where("place_id = ?", company.PlaceID).First(&existing).Error
	if err != nil {
		// 不存在，新建
		s.db.Create(company)
	} else {
		// 存在，更新非空字段
		company.ID = existing.ID
		s.db.Model(&existing).Updates(company)
	}
}

// parseCronExpr 解析简化的 cron 表达式为 time.Duration
// 支持格式: "30m" (30分钟), "2h" (2小时), "1d" (1天), "30s" (30秒)
func parseCronExpr(expr string) (time.Duration, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, fmt.Errorf("空表达式")
	}

	// 获取最后一个字符作为单位
	unit := expr[len(expr)-1]
	numStr := expr[:len(expr)-1]

	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("无效数字: %s", numStr)
	}
	if num <= 0 {
		return 0, fmt.Errorf("间隔必须大于0")
	}

	switch unit {
	case 's':
		return time.Duration(num) * time.Second, nil
	case 'm':
		return time.Duration(num) * time.Minute, nil
	case 'h':
		return time.Duration(num) * time.Hour, nil
	case 'd':
		return time.Duration(num) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("未知时间单位: %c（支持 s/m/h/d）", unit)
	}
}
