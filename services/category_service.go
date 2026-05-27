package services

import (
	"googleMap/models"

	"gorm.io/gorm"
)

type CategoryService struct {
	db *gorm.DB
}

func NewCategoryService(db *gorm.DB) *CategoryService {
	return &CategoryService{db: db}
}

// CreateCategory 创建品类
func (s *CategoryService) CreateCategory(name string, templateIDs []uint64) error {
	category := &models.Category{Name: name}
	
	// 创建品类
	if err := s.db.Create(category).Error; err != nil {
		return err
	}

	// 关联模板
	if len(templateIDs) > 0 {
		var templates []models.EmailTemplate
		if err := s.db.Where("id IN ?", templateIDs).Find(&templates).Error; err != nil {
			return err
		}
		if err := s.db.Model(category).Association("Templates").Append(templates); err != nil {
			return err
		}
	}

	return nil
}

// UpdateCategory 更新品类
func (s *CategoryService) UpdateCategory(id uint64, name string, templateIDs []uint64) error {
	var category models.Category
	if err := s.db.First(&category, id).Error; err != nil {
		return err
	}

	// 更新名称
	category.Name = name
	if err := s.db.Save(&category).Error; err != nil {
		return err
	}

	// 先清除所有关联
	if err := s.db.Model(&category).Association("Templates").Clear(); err != nil {
		return err
	}

	// 重新关联模板
	if len(templateIDs) > 0 {
		var templates []models.EmailTemplate
		if err := s.db.Where("id IN ?", templateIDs).Find(&templates).Error; err != nil {
			return err
		}
		if err := s.db.Model(&category).Association("Templates").Append(templates); err != nil {
			return err
		}
	}

	return nil
}

// DeleteCategory 删除品类
func (s *CategoryService) DeleteCategory(id uint64) error {
	return s.db.Delete(&models.Category{}, id).Error
}

// GetCategory 获取单个品类
func (s *CategoryService) GetCategory(id uint64) (*models.CategoryWithTemplates, error) {
	var category models.Category
	if err := s.db.Preload("Templates").First(&category, id).Error; err != nil {
		return nil, err
	}

	return s.convertToWithTemplates(&category), nil
}

// ListCategories 获取所有品类
func (s *CategoryService) ListCategories() ([]models.CategoryWithTemplates, error) {
	var categories []models.Category
	if err := s.db.Preload("Templates").Find(&categories).Error; err != nil {
		return nil, err
	}

	result := make([]models.CategoryWithTemplates, 0, len(categories))
	for _, c := range categories {
		result = append(result, *s.convertToWithTemplates(&c))
	}

	return result, nil
}

// convertToWithTemplates 转换为带模板信息的结构
func (s *CategoryService) convertToWithTemplates(category *models.Category) *models.CategoryWithTemplates {
	templateIDs := make([]uint64, 0, len(category.Templates))
	templateNames := make([]string, 0, len(category.Templates))
	for _, t := range category.Templates {
		templateIDs = append(templateIDs, t.ID)
		templateNames = append(templateNames, t.Name)
	}

	return &models.CategoryWithTemplates{
		ID:            category.ID,
		Name:          category.Name,
		TemplateIDs:   templateIDs,
		TemplateNames: templateNames,
		CreatedAt:     category.CreatedAt,
		UpdatedAt:     category.UpdatedAt,
	}
}

// GetCategoriesByTemplate 获取模板关联的品类列表
func (s *CategoryService) GetCategoriesByTemplate(templateID uint64) ([]models.Category, error) {
	var categories []models.Category
	err := s.db.Joins("JOIN category_templates ON category_templates.category_id = categories.id").
		Where("category_templates.template_id = ?", templateID).
		Find(&categories).Error
	return categories, err
}

// GetTemplatesByCategory 获取品类关联的模板列表
func (s *CategoryService) GetTemplatesByCategory(categoryID uint64) ([]models.EmailTemplate, error) {
	var templates []models.EmailTemplate
	err := s.db.Joins("JOIN category_templates ON category_templates.template_id = email_templates.id").
		Where("category_templates.category_id = ?", categoryID).
		Where("email_templates.is_active = ?", true).
		Find(&templates).Error
	return templates, err
}