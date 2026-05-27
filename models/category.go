package models

import (
	"time"
)

// Category 品类模型
type Category struct {
	ID        uint64          `gorm:"primaryKey" json:"id"`
	Name      string          `gorm:"size:100;not null;unique" json:"name"`                                                                                  // 品类名称
	Templates []EmailTemplate `gorm:"many2many:category_templates;foreignKey:ID;joinForeignKey:CategoryID;References:ID;joinReferences:TemplateID" json:"-"` // 关联的模板
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// CategoryTemplate 品类-模板关联模型（用于手动操作关联表）
type CategoryTemplate struct {
	CategoryID uint64 `gorm:"primaryKey"`
	TemplateID uint64 `gorm:"primaryKey"`
}

// CategoryWithTemplates 带模板列表的品类结构
type CategoryWithTemplates struct {
	ID            uint64    `json:"id"`
	Name          string    `json:"name"`
	TemplateIDs   []uint64  `json:"template_ids"`
	TemplateNames []string  `json:"template_names"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
