package services

import (
	"fmt"
	"log"
	"strings"
	"time"

	"googleMap/config"
	"googleMap/models"

	"gopkg.in/gomail.v2"
	"gorm.io/gorm"
)

type EmailService struct {
	db      *gorm.DB
	conf    config.EmailConfig
	doubao  *DoubaoService
	weather *WeatherService
}

func NewEmailService(db *gorm.DB) *EmailService {
	return &EmailService{
		db:      db,
		conf:    config.Get().Email,
		doubao:  NewDoubaoService(),
		weather: NewWeatherService(),
	}
}

// TrySendMarketingEmail 自动发送营销邮件（由配置开关控制）
func (s *EmailService) TrySendMarketingEmail(company *models.Company) {
	if company == nil {
		return
	}
	if !s.conf.AutoSendEnabled {
		return
	}

	recipient := strings.TrimSpace(company.Email)
	if s.conf.TestMode {
		testRecipient := strings.TrimSpace(s.conf.TestRecipient)
		if testRecipient == "" {
			log.Printf("[EmailAuto] 测试模式已开启但未配置 test_recipient，跳过: %s", company.Name)
			return
		}
		recipient = testRecipient
	}

	if recipient == "" {
		log.Printf("[EmailAuto] 无可用收件人，跳过: %s", company.Name)
		return
	}

	subject := strings.TrimSpace(s.conf.MarketingSubject)
	if subject == "" {
		subject = "Summer Resort Hats for Your Store"
	}

	body := s.buildMarketingBody(company)
	if body == "" {
		log.Printf("[EmailAuto] 邮件内容为空，跳过: %s", company.Name)
		return
	}

	if err := s.SendEmail(company.ID, recipient, subject, body, s.conf.CooldownMinutes); err != nil {
		log.Printf("[EmailAuto] 发送失败 company=%s recipient=%s err=%v", company.Name, recipient, err)
		return
	}

	log.Printf("[EmailAuto] 发送成功 company=%s recipient=%s", company.Name, recipient)
}

func (s *EmailService) buildMarketingBody(company *models.Company) string {
	storeName := strings.TrimSpace(company.Name)
	if storeName == "" {
		storeName = "Store"
	}

	intro := strings.TrimSpace(company.CompanyIntro)
	if intro == "" {
		intro = strings.TrimSpace(company.Description)
	}
	if intro == "" {
		intro = strings.TrimSpace(company.GoogleSnippet)
	}
	if intro == "" {
		intro = "专注精选零售产品"
	}

	weatherSummary := "当地天气信息暂不可用"
	if s.weather != nil {
		if w, err := s.weather.CurrentByLatLng(company.Latitude, company.Longitude); err == nil && w != nil && strings.TrimSpace(w.Summary) != "" {
			weatherSummary = w.Summary
		}
	}

	greeting := s.buildFallbackGreeting(weatherSummary)
	if s.doubao != nil && s.doubao.IsEnabled() {
		if text, err := s.doubao.GenerateMailGreeting(storeName, intro, weatherSummary, 30); err == nil && strings.TrimSpace(text) != "" {
			greeting = text
		}
	}

	lines := []string{
		fmt.Sprintf("尊敬的 %s：", storeName),
		fmt.Sprintf("您好！%s", greeting),
		"My name is April from Hatspick. We are a wholesale platform that works directly with high-quality manufacturers to provide reliable, market-proven headwear for independent boutiques and small resellers worldwide。",
		fmt.Sprintf("I’ve been following %s and love your curation. Given your store's aesthetic, I believe our latest Paper Straw and Seagrass collection would be a perfect fit for your summer sun-protection or resort lineup.", storeName),
		"We understand the challenges of traditional sourcing, so we’ve tailored our service to support independent businesses like yours:",
		"- Low MOQs: Start with just 6 pieces per order with mixed styles and colors allowed, significantly reducing your initial purchasing and inventory risk.",
		"- First-order trial support: Your first order of 6 sample hats ships free, helping you test new styles with minimal risk.",
		"- Factory-Direct Value: We offer sustainable, factory-direct pricing by working directly with the source.",
		"- Exclusive Inventory: To ensure your store remains unique, we focus on limited runs for each style, helping you avoid the \"mass-market\" look.",
		"- Ready-to-Use Visuals: All our images are real photos of actual products, enabling you to make faster decisions and market directly on social media.",
		"👉 Quick question: Are you still looking to add more sun or resort hats for your summer collection?",
		"If so, I’d be happy to send over a few of our best-selling styles for you to preview. I’ve also attached our company profile, which includes details about our audited manufacturing standards (such as Sedex, ISO, and Walmart FCCA).",
		"Thank you for your time, and I look forward to hearing from you.",
		"Best regards,",
		"April Zhang Overseas Manager | Hatspick www.hatspick.com | WhatsApp: +86 13026558037",
	}

	body := strings.Join(lines, "<br>")
	return body
}

func (s *EmailService) buildFallbackGreeting(weatherSummary string) string {
	text := "结合" + weatherSummary + "，愿您生意兴隆"
	runes := []rune(text)
	if len(runes) > 30 {
		return string(runes[:30])
	}
	return text
}

// CheckCooldown 检查邮箱冷却时间
// cooldownMinutes: 冷却时间（分钟），0 表示不限制
// 返回：是否可以发送, 距离下次可发送的剩余秒数
func (s *EmailService) CheckCooldown(toEmail string, cooldownMinutes int) (bool, int) {
	if cooldownMinutes <= 0 {
		return true, 0
	}

	var lastLog models.EmailLog
	err := s.db.Where("to_email = ? AND status = 1", toEmail).
		Order("sent_at DESC").
		First(&lastLog).Error

	if err != nil {
		// 没有发送记录，可以发送
		return true, 0
	}

	if lastLog.SentAt == nil {
		return true, 0
	}

	cooldownDuration := time.Duration(cooldownMinutes) * time.Minute
	nextAllowed := lastLog.SentAt.Add(cooldownDuration)
	now := time.Now()

	if now.After(nextAllowed) {
		return true, 0
	}

	remaining := int(nextAllowed.Sub(now).Seconds())
	return false, remaining
}

// SendEmail 发送邮件并记录日志
// cooldownMinutes: 冷却时间（分钟），0=不限制
func (s *EmailService) SendEmail(companyID uint64, toEmail, subject, body string, cooldownMinutes int) error {
	// 检查冷却时间
	canSend, remaining := s.CheckCooldown(toEmail, cooldownMinutes)
	if !canSend {
		return fmt.Errorf("该邮箱在冷却中，还需等待 %d 秒（约 %d 分钟）", remaining, remaining/60+1)
	}

	log := models.EmailLog{
		CompanyID: companyID,
		ToEmail:    toEmail,
		Subject:    subject,
		Body:       body,
		Status:     0,
	}

	if err := s.db.Create(&log).Error; err != nil {
		return fmt.Errorf("创建邮件记录失败: %w", err)
	}

	m := gomail.NewMessage()
	m.SetHeader("From", m.FormatAddress(s.conf.Username, s.conf.FromName))
	m.SetHeader("To", toEmail)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", body)

	d := gomail.NewDialer(s.conf.SMTPHost, s.conf.SMTPPort, s.conf.Username, s.conf.Password)

	if err := d.DialAndSend(m); err != nil {
		log.Status = 2
		log.ErrorMsg = err.Error()
		s.db.Save(&log)
		return fmt.Errorf("发送邮件失败: %w", err)
	}

	now := time.Now()
	log.Status = 1
	log.SentAt = &now
	s.db.Save(&log)

	return nil
}

// BatchSendEmail 批量发送邮件
// cooldownMinutes: 冷却时间（分钟），0=不限制
func (s *EmailService) BatchSendEmail(companyIDs []uint64, subject, body string, cooldownMinutes int) (sent int, failed int, skipped int, err error) {
	var companies []models.Company
	if err = s.db.Where("id IN ? AND email != ''", companyIDs).Find(&companies).Error; err != nil {
		return 0, 0, 0, fmt.Errorf("查询公司失败: %w", err)
	}

	for _, co := range companies {
		// 先检查冷却
		canSend, _ := s.CheckCooldown(co.Email, cooldownMinutes)
		if !canSend {
			skipped++
			continue
		}
		if sendErr := s.SendEmail(co.ID, co.Email, subject, body, cooldownMinutes); sendErr != nil {
			failed++
		} else {
			sent++
		}
	}
	return sent, failed, skipped, nil
}
