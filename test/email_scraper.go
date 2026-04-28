package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/gocolly/colly/v2"
)

// ScrapedPage 爬取结果
type ScrapedPage struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	BodyText    string   `json:"body_text"`
	Emails      []string `json:"emails"`
	Phones      []string `json:"phones"`
	SocialLinks []string `json:"social_links"`
	Success     bool     `json:"success"`
	Error       string   `json:"error"`
}

// EmailScraper 邮箱爬取器
type EmailScraper struct {
	chromePath string
}

func NewEmailScraper(chromePath string) *EmailScraper {
	return &EmailScraper{
		chromePath: chromePath,
	}
}

// ScrapeEmail 爬取网址中的邮箱（双层策略）
func (s *EmailScraper) ScrapeEmail(targetURL string) *ScrapedPage {
	log.Printf("开始爬取: %s", targetURL)

	// 1. 先使用 colly 静态爬取（快速）
	page := s.scrapeWithColly(targetURL)
	if page != nil && len(page.Emails) > 0 {
		log.Printf("[Colly] 成功提取到 %d 个邮箱", len(page.Emails))
		return page
	}

	log.Println("[Colly] 未找到邮箱，尝试使用 Rod 浏览器...")

	// 2. 使用 Rod 浏览器爬取（支持JS渲染和多Tab）
	rodPage := s.scrapeWithRod(targetURL)
	if rodPage != nil && rodPage.Success && len(rodPage.Emails) > 0 {
		log.Printf("[Rod] 成功提取到 %d 个邮箱", len(rodPage.Emails))
		return rodPage
	}

	log.Println("[Rod] 未找到邮箱，爬取完成")
	if page != nil {
		return page
	}
	if rodPage != nil {
		return rodPage
	}

	return &ScrapedPage{
		URL:     targetURL,
		Success: false,
		Error:   "所有爬取方式均失败",
	}
}

// scrapeWithColly 使用 Colly 静态爬取
func (s *EmailScraper) scrapeWithColly(targetURL string) *ScrapedPage {
	result := &ScrapedPage{URL: targetURL}

	// 验证URL
	if !isValidURL(targetURL) {
		result.Error = "无效的URL"
		return result
	}

	c := colly.NewCollector(
		colly.AllowedDomains(hostnameFromURL(targetURL)),
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	c.SetRequestTimeout(30 * time.Second)

	emailSet := make(map[string]bool)
	phoneSet := make(map[string]bool)
	socialLinks := make(map[string]bool)

	emailRegex := regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

	// 获取标题
	c.OnHTML("title", func(e *colly.HTMLElement) {
		result.Title = e.Text
	})

	// 获取描述
	c.OnHTML(`meta[name="description"]`, func(e *colly.HTMLElement) {
		result.Description = e.Attr("content")
	})

	// 提取文本内容
	c.OnHTML("body", func(e *colly.HTMLElement) {
		text := e.Text
		if len(text) > 3000 {
			text = text[:3000]
		}
		result.BodyText = text

		// 提取邮箱
		for _, email := range emailRegex.FindAllString(text, -1) {
			email = strings.ToLower(strings.TrimSpace(email))
			if isValidEmail(email) && !emailSet[email] {
				emailSet[email] = true
			}
		}
	})

	// 提取链接
	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		href := e.Attr("href")
		if strings.Contains(href, "mailto:") {
			email := strings.ReplaceAll(href, "mailto:", "")
			email = strings.ToLower(strings.TrimSpace(email))
			if isValidEmail(email) && !emailSet[email] {
				emailSet[email] = true
			}
		}

		// 社交链接
		socialDomains := []string{"linkedin.com", "twitter.com", "facebook.com", "github.com", "youtube.com", "instagram.com"}
		for _, domain := range socialDomains {
			if strings.Contains(strings.ToLower(href), domain) {
				socialLinks[href] = true
			}
		}
	})

	// 处理错误
	c.OnError(func(r *colly.Response, err error) {
		result.Error = err.Error()
		log.Printf("[Colly] 错误: %v", err)
	})

	// 开始爬取
	err := c.Visit(targetURL)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	// 转换结果
	for email := range emailSet {
		result.Emails = append(result.Emails, email)
	}
	for phone := range phoneSet {
		result.Phones = append(result.Phones, phone)
	}
	for link := range socialLinks {
		result.SocialLinks = append(result.SocialLinks, link)
	}

	result.Success = true
	return result
}

// scrapeWithRod 使用 Rod 浏览器爬取（支持JS渲染和多Tab）
func (s *EmailScraper) scrapeWithRod(targetURL string) *ScrapedPage {
	result := &ScrapedPage{URL: targetURL, Success: false}

	if targetURL == "" {
		result.Error = "URL为空"
		return result
	}

	// 验证URL格式
	parsed, err := url.Parse(targetURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		if !strings.HasPrefix(targetURL, "http") {
			targetURL = "https://" + targetURL
			parsed, err = url.Parse(targetURL)
			if err != nil {
				result.Error = "无效的URL"
				return result
			}
		} else {
			result.Error = "无效的URL"
			return result
		}
	}
	result.URL = targetURL

	// 查找浏览器路径
	chromeBin := s.findChromeBin()
	if chromeBin == "" {
		result.Error = "未找到Chrome浏览器"
		return result
	}

	browser, err := s.launchBrowser(chromeBin)
	if err != nil {
		result.Error = fmt.Sprintf("浏览器启动失败: %v", err)
		return result
	}
	defer browser.MustClose()

	page := browser.MustPage()
	defer page.MustClose()

	page.MustSetViewport(1920, 1080, 1, false)

	log.Printf("[Rod] 爬取网站: %s", targetURL)

	err = page.Navigate(targetURL)
	if err != nil {
		result.Error = fmt.Sprintf("导航失败: %v", err)
		return result
	}

	err = page.Timeout(30 * time.Second).WaitStable(2 * time.Second)
	if err != nil {
		log.Printf("[Rod] 页面加载超时，继续尝试提取数据: %v", err)
	}

	titleVal, err := page.Eval(`document.title`)
	if err == nil && titleVal != nil {
		result.Title = fmt.Sprintf("%v", titleVal.Value)
	}

	var allText strings.Builder

	pageText, err := page.Eval(`document.body.innerText`)
	if err == nil {
		allText.WriteString(fmt.Sprintf("%v", pageText.Value))
	}

	// 尝试切换所有Tab
	s.extractFromAllTabs(page, &allText)

	result.BodyText = allText.String()
	if len(result.BodyText) > 3000 {
		result.BodyText = result.BodyText[:3000]
	}

	html, err := page.HTML()
	if err != nil {
		log.Printf("[Rod] 获取HTML失败: %v", err)
	}

	emailRegex := regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	emailSet := make(map[string]bool)

	if result.BodyText != "" {
		for _, email := range emailRegex.FindAllString(result.BodyText, -1) {
			email = strings.ToLower(strings.TrimSpace(email))
			if isValidEmail(email) && !emailSet[email] {
				emailSet[email] = true
			}
		}
	}

	if html != "" {
		for _, email := range emailRegex.FindAllString(html, -1) {
			email = strings.ToLower(strings.TrimSpace(email))
			if isValidEmail(email) && !emailSet[email] {
				emailSet[email] = true
			}
		}
	}

	for email := range emailSet {
		result.Emails = append(result.Emails, email)
	}

	log.Printf("[Rod] 爬取完成: %s, 找到 %d 个邮箱", targetURL, len(result.Emails))
	result.Success = true
	return result
}

// 辅助函数
func (s *EmailScraper) findChromeBin() string {
	candidates := []string{
		s.chromePath,
		"/usr/bin/chromium-browser",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
		"C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
	}

	for _, path := range candidates {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			log.Printf("[Rod] 找到浏览器: %s", path)
			return path
		}
	}
	return ""
}

func (s *EmailScraper) launchBrowser(chromeBin string) (*rod.Browser, error) {
	l := launcher.New().
		Leakless(false).
		Headless(true).
		Set("no-sandbox").
		Set("disable-gpu").
		Set("disable-dev-shm-usage").
		Set("disable-setuid-sandbox").
		Set("lang", "en-US").
		Set("disable-blink-features", "AutomationControlled").
		Set("window-size", "1920,1080").
		Bin(chromeBin)

	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("启动失败: %v", err)
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("连接失败: %v", err)
	}

	return browser, nil
}

func (s *EmailScraper) extractFromAllTabs(page *rod.Page, allText *strings.Builder) {
	tabSelectors := []string{
		`ul.nav-tabs li a`,
		`ul.nav li a[data-toggle="tab"]`,
		`button[role="tab"]`,
		`.tabs .tab`,
		`.tab-container .tab-button`,
		`div[role="tablist"] button`,
		`.tab-item`,
		`.nav-item a`,
	}

	for _, selector := range tabSelectors {
		tabs, err := page.Elements(selector)
		if err != nil || len(tabs) == 0 {
			continue
		}

		log.Printf("[Rod] 发现 %d 个Tab，尝试切换提取...", len(tabs))

		for i, tab := range tabs {
			tabText, _ := tab.Text()
			err := tab.Click(proto.InputMouseButtonLeft, 1)
			if err != nil {
				continue
			}

			time.Sleep(500 * time.Millisecond)

			tabContent, err := page.Eval(`document.body.innerText`)
			if err == nil {
				content := fmt.Sprintf("%v", tabContent.Value)
				allText.WriteString(" ")
				allText.WriteString(content)
				log.Printf("[Rod] Tab %d (%s) 提取完成", i+1, tabText)
			}
		}
		break
	}
}

func isValidURL(u string) bool {
	parsed, err := url.Parse(u)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func hostnameFromURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func isValidEmail(email string) bool {
	invalidSuffixes := []string{".png", ".jpg", ".gif", ".svg", ".css", ".js"}
	for _, s := range invalidSuffixes {
		if strings.HasSuffix(email, s) {
			return false
		}
	}
	invalidPrefixes := []string{"noreply", "no-reply", "donotreply", "mailer-daemon"}
	for _, p := range invalidPrefixes {
		if strings.HasPrefix(email, p) {
			return false
		}
	}
	return true
}

func repeatStr(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: go run email_scraper.go <网址>")
		fmt.Println("示例: go run email_scraper.go https://example.com")
		os.Exit(1)
	}

	targetURL := os.Args[1]

	// 获取Chrome路径（从环境变量或默认路径）
	chromePath := os.Getenv("CHROME_BIN")
	if chromePath == "" {
		chromePath = "/usr/bin/chromium-browser"
	}

	scraper := NewEmailScraper(chromePath)
	result := scraper.ScrapeEmail(targetURL)

	// 格式化输出
	fmt.Println("\n" + repeatStr("=", 60))
	fmt.Println("爬取结果")
	fmt.Println(repeatStr("=", 60))
	fmt.Printf("URL: %s\n", result.URL)
	fmt.Printf("标题: %s\n", result.Title)
	fmt.Printf("状态: %s\n", map[bool]string{true: "成功", false: "失败"}[result.Success])
	if result.Error != "" {
		fmt.Printf("错误: %s\n", result.Error)
	}

	fmt.Println("\n--- 找到的邮箱 ---")
	if len(result.Emails) > 0 {
		for i, email := range result.Emails {
			fmt.Printf("%d. %s\n", i+1, email)
		}
	} else {
		fmt.Println("未找到邮箱")
	}

	fmt.Println("\n--- 找到的电话 ---")
	if len(result.Phones) > 0 {
		for i, phone := range result.Phones {
			fmt.Printf("%d. %s\n", i+1, phone)
		}
	} else {
		fmt.Println("未找到电话")
	}

	fmt.Println("\n--- 社交链接 ---")
	if len(result.SocialLinks) > 0 {
		for i, link := range result.SocialLinks {
			fmt.Printf("%d. %s\n", i+1, link)
		}
	} else {
		fmt.Println("未找到社交链接")
	}

	fmt.Println("\n" + repeatStr("=", 60))

	// 输出JSON格式（便于程序处理）
	jsonOutput, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println("\nJSON输出:")
	fmt.Println(string(jsonOutput))
}
