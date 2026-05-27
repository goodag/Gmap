package services

import (
	"fmt"
	"math/rand"
	"regexp"
	"time"

	"googleMap/models"

	"gorm.io/gorm"
)

type EmailTemplateService struct {
	db *gorm.DB
}

func NewEmailTemplateService(db *gorm.DB) *EmailTemplateService {
	return &EmailTemplateService{db: db}
}

// CreateTemplate 创建邮件模板
func (s *EmailTemplateService) CreateTemplate(template *models.EmailTemplate) error {
	return s.db.Create(template).Error
}

// UpdateTemplate 更新邮件模板
func (s *EmailTemplateService) UpdateTemplate(template *models.EmailTemplate) error {
	return s.db.Save(template).Error
}

// DeleteTemplate 删除邮件模板
func (s *EmailTemplateService) DeleteTemplate(id uint64) error {
	return s.db.Delete(&models.EmailTemplate{}, id).Error
}

// GetTemplate 获取单个模板
func (s *EmailTemplateService) GetTemplate(id uint64) (*models.EmailTemplate, error) {
	var template models.EmailTemplate
	err := s.db.First(&template, id).Error
	return &template, err
}

// ListTemplates 获取所有模板列表
func (s *EmailTemplateService) ListTemplates(activeOnly bool) ([]models.EmailTemplate, error) {
	var templates []models.EmailTemplate
	query := s.db
	if activeOnly {
		query = query.Where("is_active = ?", true)
	}
	err := query.Order("created_at DESC").Find(&templates).Error
	return templates, err
}

// ListTemplatesWithCategories 获取所有模板列表（包含关联的品类信息）
func (s *EmailTemplateService) ListTemplatesWithCategories(activeOnly bool) ([]map[string]interface{}, error) {
	var templates []models.EmailTemplate
	query := s.db
	if activeOnly {
		query = query.Where("is_active = ?", true)
	}
	if err := query.Order("created_at DESC").Find(&templates).Error; err != nil {
		return nil, err
	}

	// 获取品类服务
	categoryService := NewCategoryService(s.db)

	result := make([]map[string]interface{}, 0, len(templates))
	for _, t := range templates {
		categories, _ := categoryService.GetCategoriesByTemplate(t.ID)
		categoryNames := make([]string, 0, len(categories))
		for _, c := range categories {
			categoryNames = append(categoryNames, c.Name)
		}

		templateMap := map[string]interface{}{
			"id":             t.ID,
			"name":           t.Name,
			"subject":        t.Subject,
			"body":           t.Body,
			"signature":      t.Signature,
			"is_active":      t.IsActive,
			"category_names": categoryNames,
			"created_at":     t.CreatedAt,
			"updated_at":     t.UpdatedAt,
		}
		result = append(result, templateMap)
	}

	return result, nil
}

// GetRandomTemplate 随机获取一个启用的模板
func (s *EmailTemplateService) GetRandomTemplate() (*models.EmailTemplate, error) {
	var templates []models.EmailTemplate
	err := s.db.Where("is_active = ?", true).Find(&templates).Error
	if err != nil {
		return nil, err
	}

	if len(templates) == 0 {
		return nil, fmt.Errorf("没有可用的邮件模板")
	}

	// 随机选择一个
	rand.Seed(time.Now().UnixNano())
	index := rand.Intn(len(templates))
	return &templates[index], nil
}

// GetRandomTemplateByCategory 按品类随机获取一个启用的模板
func (s *EmailTemplateService) GetRandomTemplateByCategory(categoryID uint64) (*models.EmailTemplate, error) {
	var templates []models.EmailTemplate
	err := s.db.Joins("JOIN category_templates ON category_templates.template_id = email_templates.id").
		Where("category_templates.category_id = ?", categoryID).
		Where("email_templates.is_active = ?", true).
		Find(&templates).Error
	if err != nil {
		return nil, err
	}

	if len(templates) == 0 {
		return nil, fmt.Errorf("该品类没有可用的邮件模板")
	}

	// 随机选择一个
	rand.Seed(time.Now().UnixNano())
	index := rand.Intn(len(templates))
	return &templates[index], nil
}

// RenderTemplate 渲染模板（替换变量）
func (s *EmailTemplateService) RenderTemplate(template *models.EmailTemplate, company *models.Company, greeting string) (string, string) {
	subject := template.Subject
	body := template.Body

	// 替换公司名称
	subject = replaceVar(subject, "{company}", company.Name)
	body = replaceVar(body, "{company}", company.Name)

	// 替换问候语（仅当 greeting 不为空时替换）
	if greeting != "" {
		body = replaceVar(body, "{greeting}", greeting)
	}

	// 替换公司简介
	intro := company.CompanyIntro
	if intro == "" {
		intro = company.Description
	}
	if intro == "" {
		intro = company.GoogleSnippet
	}
	if intro == "" {
		intro = "专注精选零售产品"
	}
	body = replaceVar(body, "{intro}", intro)

	// 替换公司地址
	body = replaceVar(body, "{address}", company.FormattedAddress)

	// 替换公司电话
	body = replaceVar(body, "{phone}", company.Phone)

	// 替换公司网站
	body = replaceVar(body, "{website}", company.Website)

	// 如果有签名，追加到邮件末尾
	if template.Signature != "" {
		signature := template.Signature
		// 处理签名中的图片URL，转换为HTML img标签
		signature = processImages(signature)
		body += "\n\n---\n" + signature
	}

	return subject, body
}

// processImages 将文本中的图片URL转换为HTML img标签
// 支持格式：![alt text](image_url) 或直接的图片URL
func processImages(text string) string {
	// 匹配 ![alt](url) 格式
	text = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`).ReplaceAllString(text, `<img src="$2" alt="$1" style="max-width:300px;max-height:200px;">`)

	// 匹配直接的图片URL（以http/https开头，以图片扩展名结尾）
	text = regexp.MustCompile(`\bhttps?://[^\s]+\.(jpg|jpeg|png|gif|webp|svg)\b`).ReplaceAllString(text, `<img src="$0" style="max-width:300px;max-height:200px;">`)

	return text
}

// replaceVar 替换模板变量
func replaceVar(text, key, value string) string {
	result := text
	for {
		oldLen := len(result)
		result = replaceFirst(result, key, value)
		if len(result) == oldLen {
			break
		}
	}
	return result
}

// replaceFirst 替换第一个匹配的字符串
func replaceFirst(text, old, new string) string {
	if old == "" {
		return text
	}

	idx := findIndex(text, old)
	if idx == -1 {
		return text
	}

	return text[:idx] + new + text[idx+len(old):]
}

// findIndex 查找字符串位置
func findIndex(text, substr string) int {
	if len(substr) == 0 {
		return 0
	}

	for i := 0; i <= len(text)-len(substr); i++ {
		if text[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
