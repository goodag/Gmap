package handlers

import (
	"net/http"
	"strconv"

	"googleMap/models"
	"googleMap/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CronHandler struct {
	db        *gorm.DB
	scheduler *services.SchedulerService
}

func NewCronHandler(db *gorm.DB, scheduler *services.SchedulerService) *CronHandler {
	return &CronHandler{db: db, scheduler: scheduler}
}

// CreateTask 创建定时任务
func (h *CronHandler) CreateTask(c *gin.Context) {
	var task models.CronTask
	if err := c.ShouldBindJSON(&task); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	if err := h.scheduler.AddTask(task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "创建失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "创建成功", "data": task})
}

// UpdateTask 更新定时任务
func (h *CronHandler) UpdateTask(c *gin.Context) {
	var task models.CronTask
	if err := c.ShouldBindJSON(&task); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	if err := h.scheduler.UpdateTask(task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "更新失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "更新成功"})
}

// DeleteTask 删除定时任务
func (h *CronHandler) DeleteTask(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
		return
	}

	if err := h.scheduler.RemoveTask(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "删除失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "删除成功"})
}

// ToggleTask 启用/禁用任务
func (h *CronHandler) ToggleTask(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误"})
		return
	}

	if err := h.scheduler.ToggleTask(id, req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "操作成功"})
}

// RunOnce 立即执行一次
func (h *CronHandler) RunOnce(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
		return
	}

	if err := h.scheduler.RunOnce(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "已触发执行"})
}

// GetTasks 获取所有定时任务
func (h *CronHandler) GetTasks(c *gin.Context) {
	var tasks []models.CronTask
	h.db.Order("id DESC").Find(&tasks)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": tasks})
}
