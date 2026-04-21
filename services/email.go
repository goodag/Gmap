package services

import (
	"fmt"
	"time"

	"googleMap/config"
	"googleMap/models"

	"gopkg.in/gomail.v2"
	"gorm.io/gorm"
)

type EmailService struct {
	db   *gorm.DB
	conf config.EmailConfig
}

func NewEmailService(db *gorm.DB) *EmailService {
	return &EmailService{
		db:   db,
		conf: config.Get().Email,
	}
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
