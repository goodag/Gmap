package models

import (
	"time"
)

// EmailTemplate 邮件模板模型
type EmailTemplate struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:100;not null" json:"name"`    // 模板名称
	Subject   string    `gorm:"size:200;not null" json:"subject"` // 邮件主题
	Body      string    `gorm:"type:text;not null" json:"body"`   // 邮件正文
	Signature string    `gorm:"type:text" json:"signature"`       // 邮件签名（支持文字、图片URL、换行格式）
	IsActive  bool      `gorm:"default:true" json:"is_active"`    // 是否启用
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
