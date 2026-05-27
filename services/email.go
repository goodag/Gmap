package services

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"googleMap/config"
	"googleMap/models"

	"github.com/xuri/excelize/v2"
	"gopkg.in/gomail.v2"
	"gorm.io/gorm"
)

type EmailService struct {
	db           *gorm.DB
	conf         config.EmailConfig
	doubao       *DoubaoService
	weather      *WeatherService
	currentIndex int // 当前使用的邮箱索引，用于循环发送
}

func NewEmailService(db *gorm.DB) *EmailService {
	return &EmailService{
		db:      db,
		conf:    config.Get().Email,
		doubao:  NewDoubaoService(),
		weather: NewWeatherService(),
	}
}

// SendMarketingByRecord 按搜索记录批量发送营销邮件（手动触发）
func (s *EmailService) SendMarketingByRecord(recordID uint64) (total int, sent int, failed int, skipped int, err error) {
	var companies []models.Company
	if err = s.db.Where("search_record_id = ?", recordID).Find(&companies).Error; err != nil {
		return 0, 0, 0, 0, fmt.Errorf("查询记录下公司失败: %w", err)
	}

	total = len(companies)
	for i := range companies {
		ok, skipReason, sendErr := s.dispatchMarketingEmail(&companies[i])
		if skipReason != "" {
			skipped++
			log.Printf("[EmailManual] 跳过 company=%s reason=%s", companies[i].Name, skipReason)
			continue
		}
		if sendErr != nil {
			failed++
			log.Printf("[EmailManual] 发送失败 company=%s err=%v", companies[i].Name, sendErr)
			continue
		}
		if ok {
			sent++
		}
	}

	return total, sent, failed, skipped, nil
}

// SendMarketingByRecordWithRandomTemplate 按搜索记录批量发送营销邮件（使用随机模板）
func (s *EmailService) SendMarketingByRecordWithRandomTemplate(recordID uint64) (total int, sent int, failed int, skipped int, err error) {
	var companies []models.Company
	if err = s.db.Where("search_record_id = ?", recordID).Find(&companies).Error; err != nil {
		return 0, 0, 0, 0, fmt.Errorf("查询记录下公司失败: %w", err)
	}

	// 获取模板服务
	templateService := NewEmailTemplateService(s.db)

	total = len(companies)
	for i := range companies {
		ok, skipReason, sendErr := s.dispatchMarketingEmailWithRandomTemplate(&companies[i], templateService)
		if skipReason != "" {
			skipped++
			log.Printf("[EmailManual] 跳过 company=%s reason=%s", companies[i].Name, skipReason)
			continue
		}
		if sendErr != nil {
			failed++
			log.Printf("[EmailManual] 发送失败 company=%s err=%v", companies[i].Name, sendErr)
			continue
		}
		if ok {
			sent++
		}
	}

	return total, sent, failed, skipped, nil
}

// SendMarketingByRecordWithCategory 按搜索记录批量发送营销邮件（使用指定品类的随机模板）
func (s *EmailService) SendMarketingByRecordWithCategory(recordID uint64, categoryID uint64) (total int, sent int, failed int, skipped int, err error) {
	var companies []models.Company
	if err = s.db.Where("search_record_id = ?", recordID).Find(&companies).Error; err != nil {
		return 0, 0, 0, 0, fmt.Errorf("查询记录下公司失败: %w", err)
	}

	// 获取模板服务
	templateService := NewEmailTemplateService(s.db)

	total = len(companies)
	for i := range companies {
		ok, skipReason, sendErr := s.dispatchMarketingEmailWithCategory(&companies[i], templateService, categoryID)
		if skipReason != "" {
			skipped++
			log.Printf("[EmailManual] 跳过 company=%s reason=%s", companies[i].Name, skipReason)
			continue
		}
		if sendErr != nil {
			failed++
			log.Printf("[EmailManual] 发送失败 company=%s err=%v", companies[i].Name, sendErr)
			continue
		}
		if ok {
			sent++
		}
	}

	return total, sent, failed, skipped, nil
}

// dispatchMarketingEmailWithRandomTemplate 使用随机模板发送营销邮件
func (s *EmailService) dispatchMarketingEmailWithRandomTemplate(company *models.Company, templateService *EmailTemplateService) (ok bool, skipReason string, err error) {
	if company == nil {
		return false, "company为空", nil
	}

	// 获取随机模板
	template, err := templateService.GetRandomTemplate()
	if err != nil {
		return false, "", fmt.Errorf("获取随机模板失败: %w", err)
	}

	// 仅当模板包含 {greeting} 占位符时才生成问候语
	var greeting string
	if strings.Contains(template.Body, "{greeting}") {
		greeting = s.buildGreeting(company)
	}

	// 渲染模板
	subject, body := templateService.RenderTemplate(template, company, greeting)

	// 处理收件人
	recipient := strings.TrimSpace(company.Email)
	if s.conf.TestMode {
		testRecipient := strings.TrimSpace(s.conf.TestRecipient)
		if testRecipient == "" {
			return false, "测试模式未配置test_recipient", nil
		}
		recipient = testRecipient
	}

	if recipient == "" {
		return false, "无可用收件人", nil
	}

	if err := s.SendEmail(company.ID, recipient, subject, body, s.conf.CooldownMinutes); err != nil {
		return false, "", err
	}

	return true, "", nil
}

// dispatchMarketingEmailWithCategory 使用指定品类的随机模板发送营销邮件
func (s *EmailService) dispatchMarketingEmailWithCategory(company *models.Company, templateService *EmailTemplateService, categoryID uint64) (ok bool, skipReason string, err error) {
	if company == nil {
		return false, "company为空", nil
	}

	// 获取该品类下的随机模板
	template, err := templateService.GetRandomTemplateByCategory(categoryID)
	if err != nil {
		return false, "", fmt.Errorf("获取随机模板失败: %w", err)
	}

	// 仅当模板包含 {greeting} 占位符时才生成问候语
	var greeting string
	if strings.Contains(template.Body, "{greeting}") {
		greeting = s.buildGreeting(company)
	}

	// 渲染模板
	subject, body := templateService.RenderTemplate(template, company, greeting)

	// 处理收件人
	recipient := strings.TrimSpace(company.Email)
	if s.conf.TestMode {
		testRecipient := strings.TrimSpace(s.conf.TestRecipient)
		if testRecipient == "" {
			return false, "测试模式未配置test_recipient", nil
		}
		recipient = testRecipient
	}

	if recipient == "" {
		return false, "无可用收件人", nil
	}

	if err := s.SendEmail(company.ID, recipient, subject, body, s.conf.CooldownMinutes); err != nil {
		return false, "", err
	}

	return true, "", nil
}

// buildGreeting 构建个性化问候语
func (s *EmailService) buildGreeting(company *models.Company) string {
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
		if text, err := s.doubao.GenerateMailGreeting(company.Name, intro, weatherSummary, 30); err == nil && strings.TrimSpace(text) != "" {
			greeting = text
		}
	}

	return greeting
}

// TrySendMarketingEmail 自动发送营销邮件（由配置开关控制）
func (s *EmailService) TrySendMarketingEmail(company *models.Company) {
	if company == nil {
		return
	}
	if !s.conf.AutoSendEnabled {
		return
	}

	ok, skipReason, err := s.dispatchMarketingEmail(company)
	if skipReason != "" {
		log.Printf("[EmailAuto] 跳过 company=%s reason=%s", company.Name, skipReason)
		return
	}
	if err != nil {
		log.Printf("[EmailAuto] 发送失败 company=%s err=%v", company.Name, err)
		return
	}
	if ok {
		log.Printf("[EmailAuto] 发送成功 company=%s", company.Name)
	}
}

func (s *EmailService) dispatchMarketingEmail(company *models.Company) (ok bool, skipReason string, err error) {
	if company == nil {
		return false, "company为空", nil
	}

	recipient := strings.TrimSpace(company.Email)
	if s.conf.TestMode {
		testRecipient := strings.TrimSpace(s.conf.TestRecipient)
		if testRecipient == "" {
			return false, "测试模式未配置test_recipient", nil
		}
		recipient = testRecipient
	}

	if recipient == "" {
		return false, "无可用收件人", nil
	}

	subject := strings.TrimSpace(s.conf.MarketingSubject)
	if subject == "" {
		subject = "Summer Resort Hats for Your Store"
	}

	body := s.buildMarketingBody(company)
	if body == "" {
		return false, "邮件内容为空", nil
	}

	if err := s.SendEmail(company.ID, recipient, subject, body, s.conf.CooldownMinutes); err != nil {
		return false, "", err
	}

	return true, "", nil
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
// getNextAccount 获取下一个要使用的邮箱账户（循环使用）
func (s *EmailService) getNextAccount() config.EmailAccount {
	if len(s.conf.Accounts) == 0 {
		return config.EmailAccount{}
	}

	// 如果只有一个账户，直接返回
	if len(s.conf.Accounts) == 1 {
		return s.conf.Accounts[0]
	}

	// 循环获取下一个账户
	account := s.conf.Accounts[s.currentIndex]
	s.currentIndex = (s.currentIndex + 1) % len(s.conf.Accounts)

	return account
}

func (s *EmailService) SendEmail(companyID uint64, toEmail, subject, body string, cooldownMinutes int) error {
	// 检查冷却时间
	canSend, remaining := s.CheckCooldown(toEmail, cooldownMinutes)
	if !canSend {
		return fmt.Errorf("该邮箱在冷却中，还需等待 %d 秒（约 %d 分钟）", remaining, remaining/60+1)
	}

	emailLog := models.EmailLog{
		CompanyID: companyID,
		ToEmail:   toEmail,
		Subject:   subject,
		Body:      body,
		Status:    0,
	}

	if err := s.db.Create(&emailLog).Error; err != nil {
		return fmt.Errorf("创建邮件记录失败: %w", err)
	}

	// 获取下一个要使用的邮箱账户
	account := s.getNextAccount()
	if account.Username == "" {
		return fmt.Errorf("没有配置可用的发件邮箱")
	}

	m := gomail.NewMessage()
	m.SetHeader("From", m.FormatAddress(account.Username, account.FromName))
	m.SetHeader("To", toEmail)
	m.SetHeader("Subject", subject)

	// 处理图片：下载并嵌入邮件，返回替换后的HTML
	htmlBody, err := s.processAndEmbedImages(m, body)
	if err != nil {
		fmt.Printf("[Email] 处理图片失败: %v\n", err)
		// 继续发送，只是图片可能无法显示
		htmlBody = textToHtml(body)
	}

	m.SetBody("text/html", htmlBody)

	d := gomail.NewDialer(account.SMTPHost, account.SMTPPort, account.Username, account.Password)

	if err := d.DialAndSend(m); err != nil {
		emailLog.Status = 2
		emailLog.ErrorMsg = err.Error()
		s.db.Save(&emailLog)
		return fmt.Errorf("发送邮件失败: %w", err)
	}

	now := time.Now()
	emailLog.Status = 1
	emailLog.SentAt = &now
	s.db.Save(&emailLog)

	return nil
}

// processAndEmbedImages 处理HTML中的图片URL，下载并嵌入邮件
func (s *EmailService) processAndEmbedImages(m *gomail.Message, body string) (string, error) {
	// 先处理图片：从原始文本中提取图片URL（支持markdown格式和直接URL）
	imageURLs := extractImageURLsFromText(body)
	if len(imageURLs) == 0 {
		// 没有图片，直接转换为HTML
		return textToHtml(body), nil
	}

	// 下载并嵌入每个图片，记录URL到CID的映射
	urlToCID := make(map[string]string)
	cidCounter := 0
	for _, imgURL := range imageURLs {
		// 下载图片
		imgData, err := downloadImage(imgURL)
		if err != nil {
			fmt.Printf("[Email] 下载图片失败 %s: %v\n", imgURL, err)
			continue
		}

		// 生成唯一的CID
		cidCounter++
		cid := fmt.Sprintf("image%d@mail.example.com", cidCounter)
		urlToCID[imgURL] = cid

		// 获取文件扩展名
		ext := getFileExtension(imgURL)
		if ext == "" {
			ext = "png"
		}

		// 嵌入图片到邮件（使用Attach方法并设置Content-ID）
		m.Attach(fmt.Sprintf("image%d.%s", cidCounter, ext), gomail.SetCopyFunc(func(w io.Writer) error {
			_, err := w.Write(imgData)
			return err
		}), gomail.SetHeader(map[string][]string{
			"Content-ID":          {"<" + cid + ">"},
			"Content-Disposition": {"inline; filename=\"image" + fmt.Sprintf("%d.%s", cidCounter, ext) + "\""},
		}))
	}

	// 先将原始文本中的图片URL替换为CID引用
	processedBody := body
	for imgURL, cid := range urlToCID {
		// 替换markdown格式的图片引用
		processedBody = strings.ReplaceAll(processedBody, "!["+imgURL+"]", "![cid:"+cid+"]")
		processedBody = strings.ReplaceAll(processedBody, "("+imgURL+")", "(cid:"+cid+")")
		// 替换直接的图片URL
		processedBody = strings.ReplaceAll(processedBody, imgURL, "cid:"+cid)
	}

	// 最后转换为HTML
	htmlBody := textToHtml(processedBody)

	return htmlBody, nil
}

// extractImageURLsFromText 从文本中提取所有图片URL（支持markdown和直接URL）
func extractImageURLsFromText(text string) []string {
	var urls []string

	// 匹配 markdown 格式: ![alt](url)
	markdownRegex := regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	markdownMatches := markdownRegex.FindAllStringSubmatch(text, -1)
	for _, match := range markdownMatches {
		if len(match) > 2 {
			urls = append(urls, match[2])
		}
	}

	// 匹配直接的图片URL（以http/https开头，以图片扩展名结尾）
	urlRegex := regexp.MustCompile(`\bhttps?://[^\s]+\.(jpg|jpeg|png|gif|webp|svg)\b`)
	urlMatches := urlRegex.FindAllString(text, -1)
	for _, match := range urlMatches {
		// 避免重复添加
		found := false
		for _, u := range urls {
			if u == match {
				found = true
				break
			}
		}
		if !found {
			urls = append(urls, match)
		}
	}

	return urls
}

// extractImageURLs 从HTML中提取所有图片URL
func extractImageURLs(html string) []string {
	var urls []string

	// 匹配 <img src="URL"> 格式
	re := regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	matches := re.FindAllStringSubmatch(html, -1)
	for _, match := range matches {
		if len(match) > 1 {
			urls = append(urls, match[1])
		}
	}

	return urls
}

// downloadImage 从URL下载图片数据
func downloadImage(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// getFileExtension 从URL获取文件扩展名
func getFileExtension(url string) string {
	parts := strings.Split(url, ".")
	if len(parts) > 1 {
		ext := parts[len(parts)-1]
		// 处理URL参数
		ext = strings.Split(ext, "?")[0]
		ext = strings.ToLower(ext)
		// 验证是否为图片格式
		validExts := map[string]bool{"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "svg": true}
		if validExts[ext] {
			return ext
		}
	}
	return ""
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

// textToHtml 将纯文本格式转换为HTML格式
// 保留换行和空格，支持段落分隔
// 支持 Markdown 图片格式转换
func textToHtml(text string) string {
	if text == "" {
		return ""
	}

	// 先将 Markdown 图片格式转换为 HTML
	// 格式: ![alt](url) → <img src="url" alt="alt">
	markdownImgRegex := regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	text = markdownImgRegex.ReplaceAllString(text, `<img src="$2" alt="$1" style="max-width:300px;max-height:200px;">`)

	// 将直接的图片URL转换为HTML img标签
	urlImgRegex := regexp.MustCompile(`\bhttps?://[^\s]+\.(jpg|jpeg|png|gif|webp|svg)\b`)
	text = urlImgRegex.ReplaceAllString(text, `<img src="$0" style="max-width:300px;max-height:200px;">`)

	// 提取并临时替换所有 <img> 标签（包括刚生成的），避免被转义
	imgTags := []string{}
	imgRegex := regexp.MustCompile(`<img[^>]+>`)
	text = imgRegex.ReplaceAllStringFunc(text, func(match string) string {
		imgTags = append(imgTags, match)
		return fmt.Sprintf("\x00IMG_TAG_%d\x00", len(imgTags)-1)
	})

	// 替换HTML特殊字符（除了已经是HTML标签的内容）
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	text = strings.ReplaceAll(text, "\"", "&quot;")

	// 将连续的空行（段落分隔）转换为 </p><p>
	re := regexp.MustCompile(`\n\n+`)
	text = re.ReplaceAllString(text, "</p><p>")

	// 将单个换行转换为 <br>
	text = strings.ReplaceAll(text, "\n", "<br>")

	// 恢复 <img> 标签
	for i, imgTag := range imgTags {
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00IMG_TAG_%d\x00", i), imgTag)
	}

	// 添加段落标签（如果内容不以标签开头）
	if !strings.HasPrefix(text, "<p>") && !strings.HasPrefix(text, "<img") {
		text = "<p>" + text
	}
	if !strings.HasSuffix(text, "</p>") && !strings.HasSuffix(text, ">") {
		text = text + "</p>"
	}

	return text
}

// ParseEmailsFromExcel 从Excel文件中解析邮箱列表
// 默认读取第一列数据作为邮箱地址
func ParseEmailsFromExcel(fileData []byte) ([]string, error) {
	var emails []string

	f, err := excelize.OpenReader(bytes.NewReader(fileData))
	if err != nil {
		return nil, fmt.Errorf("打开Excel文件失败: %w", err)
	}
	defer f.Close()

	// 获取第一个工作表
	sheetList := f.GetSheetList()
	if len(sheetList) == 0 {
		return nil, fmt.Errorf("Excel文件中没有工作表")
	}
	sheetName := sheetList[0]

	// 获取所有行
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("读取工作表失败: %w", err)
	}

	// 邮箱验证正则
	emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

	// 遍历行（跳过标题行）
	for i, row := range rows {
		if i == 0 && len(row) > 0 && strings.Contains(strings.ToLower(row[0]), "邮箱") {
			continue // 跳过标题行
		}
		if len(row) > 0 {
			email := strings.TrimSpace(row[0])
			if email != "" && emailRegex.MatchString(email) {
				// 避免重复
				exists := false
				for _, e := range emails {
					if e == email {
						exists = true
						break
					}
				}
				if !exists {
					emails = append(emails, email)
				}
			}
		}
	}

	return emails, nil
}

// SendToEmails 使用指定模板向邮箱列表发送邮件
// templateID: 模板ID
// emails: 邮箱列表
// 返回：发送成功数、失败数、错误
func (s *EmailService) SendToEmails(templateID uint64, emails []string) (sent int, failed int, err error) {
	// 获取模板
	templateService := NewEmailTemplateService(s.db)
	template, err := templateService.GetTemplate(templateID)
	if err != nil {
		return 0, 0, fmt.Errorf("获取模板失败: %w", err)
	}

	if !template.IsActive {
		return 0, 0, fmt.Errorf("模板未启用")
	}

	// 构建空的公司对象用于渲染模板（不替换公司相关变量）
	emptyCompany := &models.Company{
		Name: "商家",
	}

	// 渲染模板
	subject, body := templateService.RenderTemplate(template, emptyCompany, "")

	// 批量发送
	for _, email := range emails {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}

		// 检查测试模式
		recipient := email
		if s.conf.TestMode {
			testRecipient := strings.TrimSpace(s.conf.TestRecipient)
			if testRecipient != "" {
				recipient = testRecipient
			}
		}

		if err := s.SendEmail(0, recipient, subject, body, s.conf.CooldownMinutes); err != nil {
			log.Printf("[EmailBatch] 发送失败 email=%s err=%v", email, err)
			failed++
		} else {
			sent++
		}
	}

	return sent, failed, nil
}
