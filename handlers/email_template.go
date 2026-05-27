package handlers

import (
	"io"
	"net/http"
	"strconv"

	"googleMap/models"
	"googleMap/services"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

type EmailTemplateHandler struct {
	db        *gorm.DB
	service   *services.EmailTemplateService
	emailServ *services.EmailService
}

func NewEmailTemplateHandler(db *gorm.DB) *EmailTemplateHandler {
	return &EmailTemplateHandler{
		db:        db,
		service:   services.NewEmailTemplateService(db),
		emailServ: services.NewEmailService(db),
	}
}

// CreateTemplate 创建邮件模板
func (h *EmailTemplateHandler) CreateTemplate(c *gin.Context) {
	var req struct {
		Name      string `json:"name" binding:"required,max=100"`
		Subject   string `json:"subject" binding:"required,max=200"`
		Body      string `json:"body" binding:"required"`
		Signature string `json:"signature"`
		IsActive  bool   `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	template := &models.EmailTemplate{
		Name:      req.Name,
		Subject:   req.Subject,
		Body:      req.Body,
		Signature: req.Signature,
		IsActive:  req.IsActive,
	}

	if err := h.service.CreateTemplate(template); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "创建模板失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "创建成功", "data": template})
}

// UpdateTemplate 更新邮件模板
func (h *EmailTemplateHandler) UpdateTemplate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "ID格式错误"})
		return
	}

	var req struct {
		Name      string `json:"name" binding:"max=100"`
		Subject   string `json:"subject" binding:"max=200"`
		Body      string `json:"body"`
		Signature string `json:"signature"`
		IsActive  bool   `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	template, err := h.service.GetTemplate(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "模板不存在"})
		return
	}

	if req.Name != "" {
		template.Name = req.Name
	}
	if req.Subject != "" {
		template.Subject = req.Subject
	}
	if req.Body != "" {
		template.Body = req.Body
	}
	template.Signature = req.Signature
	template.IsActive = req.IsActive

	if err := h.service.UpdateTemplate(template); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "更新模板失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "更新成功", "data": template})
}

// DeleteTemplate 删除邮件模板
func (h *EmailTemplateHandler) DeleteTemplate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "ID格式错误"})
		return
	}

	if err := h.service.DeleteTemplate(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "删除失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "删除成功"})
}

// GetTemplate 获取单个模板
func (h *EmailTemplateHandler) GetTemplate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "ID格式错误"})
		return
	}

	template, err := h.service.GetTemplate(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "模板不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": template})
}

// ListTemplates 获取模板列表
func (h *EmailTemplateHandler) ListTemplates(c *gin.Context) {
	activeOnly := c.Query("active_only") == "true"
	templates, err := h.service.ListTemplates(activeOnly)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "查询失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": templates})
}

// ListTemplatesWithCategories 获取模板列表（包含品类信息）
func (h *EmailTemplateHandler) ListTemplatesWithCategories(c *gin.Context) {
	activeOnly := c.Query("active_only") == "true"
	templates, err := h.service.ListTemplatesWithCategories(activeOnly)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "查询失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": templates})
}

// SendWithRandomTemplate 使用随机模板发送邮件给记录下的所有公司
func (h *EmailTemplateHandler) SendWithRandomTemplate(c *gin.Context) {
	recordID, err := strconv.ParseUint(c.Param("record_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "记录ID格式错误"})
		return
	}

	total, sent, failed, skipped, err := h.emailServ.SendMarketingByRecordWithRandomTemplate(recordID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "发送完成",
		"data": gin.H{"total": total, "sent": sent, "failed": failed, "skipped": skipped},
	})
}

// SendWithCategory 使用指定品类的随机模板发送邮件给记录下的所有公司
func (h *EmailTemplateHandler) SendWithCategory(c *gin.Context) {
	recordID, err := strconv.ParseUint(c.Param("record_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "记录ID格式错误"})
		return
	}

	var req struct {
		CategoryID uint64 `json:"category_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	total, sent, failed, skipped, err := h.emailServ.SendMarketingByRecordWithCategory(recordID, req.CategoryID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "发送完成",
		"data": gin.H{"total": total, "sent": sent, "failed": failed, "skipped": skipped},
	})
}

// DownloadEmailTemplate 下载Excel模板文件
func (h *EmailTemplateHandler) DownloadEmailTemplate(c *gin.Context) {
	// 创建Excel文件
	f := excelize.NewFile()
	// 创建工作表
	index, _ := f.NewSheet("邮箱列表")
	// 设置标题行
	f.SetCellValue("邮箱列表", "A1", "邮箱")
	f.SetCellValue("邮箱列表", "B1", "备注（可选）")
	// 设置默认工作表
	f.SetActiveSheet(index)

	// 设置响应头
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=email_template.xlsx")
	c.Header("Access-Control-Expose-Headers", "Content-Disposition")

	// 写入响应
	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "生成模板失败: " + err.Error()})
	}
}

// SendTemplateToEmails 使用指定模板向Excel中的邮箱列表发送邮件
func (h *EmailTemplateHandler) SendTemplateToEmails(c *gin.Context) {
	templateID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "模板ID格式错误"})
		return
	}

	// 解析上传的文件
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "请上传Excel文件"})
		return
	}

	// 限制文件大小（10MB）
	if file.Size > 10*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "文件大小不能超过10MB"})
		return
	}

	// 读取文件内容
	fileData, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "读取文件失败: " + err.Error()})
		return
	}
	defer fileData.Close()

	data, err := io.ReadAll(fileData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "读取文件失败: " + err.Error()})
		return
	}

	// 解析邮箱列表
	emails, err := services.ParseEmailsFromExcel(data)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "解析Excel文件失败: " + err.Error()})
		return
	}

	if len(emails) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Excel文件中没有有效的邮箱地址"})
		return
	}

	// 限制单次发送数量
	if len(emails) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "单次最多发送1000封邮件"})
		return
	}

	// 发送邮件
	sent, failed, err := h.emailServ.SendToEmails(templateID, emails)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "发送邮件失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "发送完成",
		"data": gin.H{
			"total":  len(emails),
			"sent":   sent,
			"failed": failed,
		},
	})
}
