package config

import (
	"encoding/json"
	"os"
	"sync"
)

type Config struct {
	Server   ServerConfig   `json:"server"`
	Database DatabaseConfig `json:"database"`
	Google   GoogleConfig   `json:"google"`
	Email    EmailConfig    `json:"email"`
	Proxy    ProxyConfig    `json:"proxy"`
	Doubao   DoubaoConfig   `json:"doubao"`
}

type DoubaoConfig struct {
	APIKey  string `json:"api_key"`
	ModelID string `json:"model_id"` // ep-xxx 或 Doubao-Seed-2.0-lite
	BaseURL string `json:"base_url"`
	Enabled bool   `json:"enabled"`
}

type ProxyConfig struct {
	Enabled    bool   `json:"enabled"`
	Address    string `json:"address"`     // socks5://127.0.0.1:1080 or http://127.0.0.1:7890
	ChromePath string `json:"chrome_path"` // 自定义 Chrome/Edge 路径（可选）
}

type ServerConfig struct {
	Port string `json:"port"`
}

type DatabaseConfig struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
}

func (d DatabaseConfig) DSN() string {
	return d.User + ":" + d.Password + "@tcp(" + d.Host + ":" + d.Port + ")/" + d.DBName + "?charset=utf8mb4&parseTime=True&loc=Local"
}

type GoogleConfig struct {
	APIKey         string `json:"api_key"`
	CustomSearchID string `json:"custom_search_id"` // Google Custom Search Engine ID
}

// EmailAccount 单个邮箱账户配置
type EmailAccount struct {
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	Username string `json:"username"`
	Password string `json:"password"`
	FromName string `json:"from_name"`
}

type EmailConfig struct {
	Accounts         []EmailAccount `json:"accounts"` // 多个发件邮箱配置
	AutoSendEnabled  bool           `json:"auto_send_enabled"`
	TestMode         bool           `json:"test_mode"`
	TestRecipient    string         `json:"test_recipient"`
	CooldownMinutes  int            `json:"cooldown_minutes"`
	MarketingSubject string         `json:"marketing_subject"`
}

var (
	cfg  *Config
	once sync.Once
)

func Get() *Config {
	once.Do(func() {
		cfg = &Config{
			Server:   ServerConfig{Port: "8088"},
			Database: DatabaseConfig{Host: "127.0.0.1", Port: "3306", User: "root", Password: "", DBName: "google_map_search"},
			Google:   GoogleConfig{APIKey: "", CustomSearchID: ""},
			Email:    EmailConfig{Accounts: []EmailAccount{{SMTPHost: "smtp.gmail.com", SMTPPort: 587, Username: "", Password: "", FromName: "Google Map Search"}}, AutoSendEnabled: false, TestMode: true, TestRecipient: "", CooldownMinutes: 1440, MarketingSubject: "Summer Resort Hats for Your Store"},
			Proxy:    ProxyConfig{Enabled: false, Address: "", ChromePath: ""},
			Doubao:   DoubaoConfig{APIKey: "", ModelID: "Doubao-Seed-2.0-lite", BaseURL: "https://ark.cn-beijing.volces.com/api/v3", Enabled: false},
		}
		f, err := os.Open("config.json")
		if err != nil {
			return
		}
		defer f.Close()
		json.NewDecoder(f).Decode(cfg)
	})
	return cfg
}
