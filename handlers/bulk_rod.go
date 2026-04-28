package handlers

import (
	"net/http"
	"strconv"

	"googleMap/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// BulkRodHandler 批量 Rod 搜索处理器
type BulkRodHandler struct {
	db      *gorm.DB
	bulkSvc *services.BulkRodService
}

func NewBulkRodHandler(db *gorm.DB, bulkSvc *services.BulkRodService) *BulkRodHandler {
	return &BulkRodHandler{db: db, bulkSvc: bulkSvc}
}

// CreateAndStart 创建并启动批量任务
func (h *BulkRodHandler) CreateAndStart(c *gin.Context) {
	var req struct {
		Keywords    string `json:"keywords"`
		Countries   string `json:"countries"`
		IntervalSec int    `json:"interval_sec"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	if req.Keywords == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "关键词不能为空"})
		return
	}
	if req.Countries == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "国家列表不能为空"})
		return
	}

	progress, err := h.bulkSvc.CreateTask(req.Keywords, req.Countries, req.IntervalSec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	if err := h.bulkSvc.Start(progress.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "启动失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "任务已创建并启动", "data": progress})
}

// Resume 恢复已暂停的任务
func (h *BulkRodHandler) Resume(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
		return
	}

	if err := h.bulkSvc.Start(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "任务已恢复"})
}

// Stop 停止运行中的任务
func (h *BulkRodHandler) Stop(c *gin.Context) {
	h.bulkSvc.Stop()
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "已发送停止信号"})
}

// GetStatus 获取任务状态
func (h *BulkRodHandler) GetStatus(c *gin.Context) {
	idStr := c.Query("id")
	var progress interface{}
	var err error

	if idStr != "" {
		id, parseErr := strconv.ParseUint(idStr, 10, 64)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
			return
		}
		progress, err = h.bulkSvc.GetProgress(id)
	} else {
		progress, err = h.bulkSvc.GetLatestProgress()
	}

	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": nil, "running": h.bulkSvc.IsRunning()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": progress, "running": h.bulkSvc.IsRunning()})
}

// Reset 重置任务进度
func (h *BulkRodHandler) Reset(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
		return
	}

	if err := h.bulkSvc.ResetProgress(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "进度已重置"})
}

// RetryFailed 重试失败的任务（从断点继续）
func (h *BulkRodHandler) RetryFailed(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
		return
	}
	if err := h.bulkSvc.RetryFailed(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "重试任务已启动"})
}

// GetAllTasks 获取所有批量任务列表
func (h *BulkRodHandler) GetAllTasks(c *gin.Context) {
	var progresses []struct {
		ID            uint64 `json:"id"`
		TotalKeywords int    `json:"total_keywords"`
		TotalCities   int    `json:"total_cities"`
		TotalCombos   int    `json:"total_combos"`
		Completed     int    `json:"completed"`
		SuccessCount  int    `json:"success_count"`
		ErrorCount    int    `json:"error_count"`
		TotalFound    int    `json:"total_found"`
		Status        int8   `json:"status"`
		LastKeyword   string `json:"last_keyword"`
		LastCity      string `json:"last_city"`
		LastCountry   string `json:"last_country"`
		IntervalSec   int    `json:"interval_sec"`
	}
	h.db.Table("bulk_rod_progress").Order("id DESC").Limit(20).Find(&progresses)

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": progresses, "running": h.bulkSvc.IsRunning()})
}
