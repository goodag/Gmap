package handlers

import (
	"net/http"
	"strconv"

	"googleMap/models"
	"googleMap/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type EmailHandler struct {
	db           *gorm.DB
	emailService *services.EmailService
}

func NewEmailHandler(db *gorm.DB) *EmailHandler {
	return &EmailHandler{
		db:           db,
		emailService: services.NewEmailService(db),
	}
}

// SendEmail 发送邮件给单个商家
func (h *EmailHandler) SendEmail(c *gin.Context) {
	var req struct {
		CompanyID       uint64 `json:"company_id" binding:"required"`
		ToEmail         string `json:"to_email" binding:"required,email"`
		Subject         string `json:"subject" binding:"required"`
		Body            string `json:"body" binding:"required"`
		CooldownMinutes int    `json:"cooldown_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	if err := h.emailService.SendEmail(req.CompanyID, req.ToEmail, req.Subject, req.Body, req.CooldownMinutes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "邮件发送成功"})
}

// BatchSendEmail 批量发送邮件
func (h *EmailHandler) BatchSendEmail(c *gin.Context) {
	var req struct {
		CompanyIDs      []uint64 `json:"company_ids" binding:"required"`
		Subject         string   `json:"subject" binding:"required"`
		Body            string   `json:"body" binding:"required"`
		CooldownMinutes int      `json:"cooldown_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	sent, failed, skipped, err := h.emailService.BatchSendEmail(req.CompanyIDs, req.Subject, req.Body, req.CooldownMinutes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "批量发送完成",
		"data": gin.H{"sent": sent, "failed": failed, "skipped": skipped},
	})
}

// CheckCooldown 检查邮箱冷却状态
func (h *EmailHandler) CheckCooldown(c *gin.Context) {
	email := c.Query("email")
	cooldown := 60 // 默认60分钟
	if v := c.Query("cooldown_minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cooldown = n
		}
	}

	canSend, remaining := h.emailService.CheckCooldown(email, cooldown)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"can_send":          canSend,
			"remaining_seconds": remaining,
		},
	})
}

// GetEmailLogs 获取邮件日志
func (h *EmailHandler) GetEmailLogs(c *gin.Context) {
	var logs []models.EmailLog
	h.db.Preload("Company").Order("created_at DESC").Limit(100).Find(&logs)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": logs})
}
