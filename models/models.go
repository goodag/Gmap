package models

import "time"

// SearchRecord 搜索记录（地图搜索 / 谷歌搜索 共用）
type SearchRecord struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Source       string    `gorm:"size:20;not null;default:'map'" json:"source"` // map=地图搜索, google=谷歌搜索
	Keyword      string    `gorm:"size:255;not null;default:''" json:"keyword"`
	Latitude     float64   `gorm:"type:decimal(10,7);not null;default:0" json:"latitude"`
	Longitude    float64   `gorm:"type:decimal(10,7);not null;default:0" json:"longitude"`
	Radius       int       `gorm:"not null;default:5000" json:"radius"`
	Address      string    `gorm:"size:500;not null;default:''" json:"address"`
	Location     string    `gorm:"size:255;not null;default:''" json:"location"` // 谷歌搜索的地区限制
	Language     string    `gorm:"size:20;not null;default:''" json:"language"`
	Pages        int       `gorm:"not null;default:1" json:"pages"`
	TotalResults int       `gorm:"not null;default:0" json:"total_results"`
	Status       int8      `gorm:"not null;default:1" json:"status"` // 0=进行中 1=完成 2=失败
	ErrorMsg     string    `gorm:"size:1000;not null;default:''" json:"error_msg"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (SearchRecord) TableName() string { return "search_records" }

// Company 统一公司/商家表（地图搜索和谷歌搜索共用）
type Company struct {
	ID                 uint64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Source             string  `gorm:"size:20;not null;default:'map';index" json:"source"` // map / google
	PlaceID            string  `gorm:"size:500;not null;default:'';uniqueIndex:uk_place_id" json:"place_id"`
	Name               string  `gorm:"size:500;not null;default:''" json:"name"`
	FormattedAddress   string  `gorm:"size:1000;not null;default:''" json:"formatted_address"`
	Phone              string  `gorm:"size:100;not null;default:''" json:"phone"`
	InternationalPhone string  `gorm:"size:100;not null;default:''" json:"international_phone"`
	Email              string  `gorm:"size:255;not null;default:''" json:"email"`
	Website            string  `gorm:"size:1000;not null;default:''" json:"website"`
	Domain             string  `gorm:"size:255;not null;default:''" json:"domain"`
	Rating             float64 `gorm:"type:decimal(2,1);not null;default:0" json:"rating"`
	UserRatingsTotal   int     `gorm:"not null;default:0" json:"user_ratings_total"`
	Types              string  `gorm:"size:500;not null;default:''" json:"types"`
	Latitude           float64 `gorm:"type:decimal(10,7);not null;default:0" json:"latitude"`
	Longitude          float64 `gorm:"type:decimal(10,7);not null;default:0" json:"longitude"`
	BusinessStatus     string  `gorm:"size:50;not null;default:''" json:"business_status"`
	OpeningHours       string  `gorm:"type:text" json:"opening_hours,omitempty"`
	Photos             string  `gorm:"type:text" json:"photos,omitempty"`
	// 谷歌搜索专用字段
	GoogleTitle   string `gorm:"size:500;not null;default:''" json:"google_title"`
	GoogleSnippet string `gorm:"size:2000;not null;default:''" json:"google_snippet"`
	PageTitle     string `gorm:"size:500;not null;default:''" json:"page_title"`
	Description   string `gorm:"size:2000;not null;default:''" json:"description"`
	// 爬取信息
	ScrapedEmails string `gorm:"size:2000;not null;default:''" json:"scraped_emails"` // JSON数组
	ScrapedPhones string `gorm:"size:2000;not null;default:''" json:"scraped_phones"` // JSON数组
	SocialLinks   string `gorm:"type:text" json:"social_links"`                       // JSON数组
	BodyText      string `gorm:"type:longtext" json:"body_text"`
	ScrapeSuccess bool   `gorm:"not null;default:false" json:"scrape_success"`
	ScrapeError   string `gorm:"size:1000;not null;default:''" json:"scrape_error"`
	Filtered      bool   `gorm:"not null;default:false" json:"filtered"`
	FilterReason  string `gorm:"size:500;not null;default:''" json:"filter_reason"`
	// AI 分析
	CompanyIntro  string `gorm:"type:text" json:"company_intro"`                        // 公司简介（AI生成）
	AIAnalysis    string `gorm:"type:text" json:"ai_analysis"`                           // AI分析结果JSON
	AIScore       int    `gorm:"not null;default:0" json:"ai_score"`                     // AI匹配评分(0-100)
	AIAnalyzed    bool   `gorm:"not null;default:false" json:"ai_analyzed"`              // 是否已AI分析
	// 关联搜索记录
	SearchRecordID uint64 `gorm:"not null;default:0;index:idx_search_record" json:"search_record_id"`
	// 时间
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (Company) TableName() string { return "companies" }

// CompanyEmail 公司邮箱表（一个邮箱一条记录）
type CompanyEmail struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	CompanyID uint64    `gorm:"not null;index:idx_company_email,unique,priority:1" json:"company_id"`
	Email     string    `gorm:"size:255;not null;index:idx_company_email,unique,priority:2;index:idx_email" json:"email"`
	Source    string    `gorm:"size:50;not null;default:'scrape'" json:"source"` // scrape=爬取, google=Google, manual=手动
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	Company   *Company  `gorm:"foreignKey:CompanyID" json:"company,omitempty"`
}

func (CompanyEmail) TableName() string { return "company_emails" }

// EmailLog 邮件发送记录
type EmailLog struct {
	ID        uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CompanyID uint64     `gorm:"not null;index" json:"company_id"`
	ToEmail   string     `gorm:"size:255;not null" json:"to_email"`
	Subject   string     `gorm:"size:500;not null;default:''" json:"subject"`
	Body      string     `gorm:"type:text;not null" json:"body"`
	Status    int8       `gorm:"not null;default:0" json:"status"` // 0=待发送 1=已发送 2=失败
	ErrorMsg  string     `gorm:"size:1000;not null;default:''" json:"error_msg"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	CreatedAt time.Time  `gorm:"autoCreateTime" json:"created_at"`
	Company   *Company   `gorm:"foreignKey:CompanyID" json:"company,omitempty"`
}

func (EmailLog) TableName() string { return "email_logs" }

// CronTask 定时任务配置
type CronTask struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	Name     string `gorm:"size:255;not null" json:"name"`
	TaskType string `gorm:"size:20;not null" json:"task_type"` // map / google
	CronExpr string `gorm:"size:100;not null" json:"cron_expr"`
	Enabled  bool   `gorm:"not null;default:true" json:"enabled"`
	// 搜索参数（JSON）
	Params     string     `gorm:"type:text;not null" json:"params"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	LastStatus int8       `gorm:"not null;default:0" json:"last_status"` // 0=未运行 1=成功 2=失败
	LastError  string     `gorm:"size:1000;not null;default:''" json:"last_error"`
	RunCount   int        `gorm:"not null;default:0" json:"run_count"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (CronTask) TableName() string { return "cron_tasks" }
