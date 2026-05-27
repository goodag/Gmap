package handlers

import (
	"net/http"
	"strconv"

	"googleMap/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CategoryHandler struct {
	db      *gorm.DB
	service *services.CategoryService
}

func NewCategoryHandler(db *gorm.DB) *CategoryHandler {
	return &CategoryHandler{
		db:      db,
		service: services.NewCategoryService(db),
	}
}

// CreateCategory 创建品类
func (h *CategoryHandler) CreateCategory(c *gin.Context) {
	var req struct {
		Name       string   `json:"name" binding:"required,max=100"`
		TemplateIDs []uint64 `json:"template_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	if err := h.service.CreateCategory(req.Name, req.TemplateIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "创建品类失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "创建成功"})
}

// UpdateCategory 更新品类
func (h *CategoryHandler) UpdateCategory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "ID格式错误"})
		return
	}

	var req struct {
		Name       string   `json:"name" binding:"max=100"`
		TemplateIDs []uint64 `json:"template_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误: " + err.Error()})
		return
	}

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "品类名称不能为空"})
		return
	}

	if err := h.service.UpdateCategory(id, req.Name, req.TemplateIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "更新品类失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "更新成功"})
}

// DeleteCategory 删除品类
func (h *CategoryHandler) DeleteCategory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "ID格式错误"})
		return
	}

	if err := h.service.DeleteCategory(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "删除失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "删除成功"})
}

// GetCategory 获取单个品类
func (h *CategoryHandler) GetCategory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "ID格式错误"})
		return
	}

	category, err := h.service.GetCategory(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "品类不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": category})
}

// ListCategories 获取品类列表
func (h *CategoryHandler) ListCategories(c *gin.Context) {
	categories, err := h.service.ListCategories()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "查询失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": categories})
}