package services

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

// ScrapedPage 爬取的网页信息
type ScrapedPage struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Emails      []string `json:"emails"`
	Phones      []string `json:"phones"`
	SocialLinks []string `json:"social_links"`
	BodyText    string   `json:"body_text"`
	Success     bool     `json:"success"`
	Error       string   `json:"error,omitempty"`
}

// ScraperService 网站爬取服务
type ScraperService struct{}

func NewScraperService() *ScraperService {
	return &ScraperService{}
}

var (
	emailRegex = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	phoneRegex = regexp.MustCompile(`(?:\+?\d{1,3}[\s\-]?)?\(?\d{2,4}\)?[\s\-]?\d{3,4}[\s\-]?\d{3,4}`)
)

// ScrapeWebsite 爬取单个网站获取联系信息
func (s *ScraperService) ScrapeWebsite(websiteURL string) *ScrapedPage {
	result := &ScrapedPage{URL: websiteURL}

	if websiteURL == "" {
		result.Error = "URL为空"
		return result
	}

	// 验证URL格式
	parsed, err := url.Parse(websiteURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		// 尝试加 https
		if !strings.HasPrefix(websiteURL, "http") {
			websiteURL = "https://" + websiteURL
			parsed, err = url.Parse(websiteURL)
			if err != nil {
				result.Error = "无效的URL"
				return result
			}
		}
	}
	result.URL = websiteURL

	c := colly.NewCollector(
		colly.AllowedDomains(parsed.Hostname(), "www."+parsed.Hostname()),
		colly.MaxDepth(2),
		colly.Async(false),
	)

	c.SetRequestTimeout(15 * time.Second)

	// 限速：避免对目标站点造成压力
	c.Limit(&colly.LimitRule{
		DomainGlob:  "*",
		Parallelism: 1,
		Delay:       1 * time.Second,
	})

	c.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	var allText strings.Builder
	emailSet := make(map[string]bool)
	phoneSet := make(map[string]bool)
	socialSet := make(map[string]bool)

	// 提取标题
	c.OnHTML("title", func(e *colly.HTMLElement) {
		if result.Title == "" {
			result.Title = strings.TrimSpace(e.Text)
		}
	})

	// 提取 meta description
	c.OnHTML(`meta[name="description"]`, func(e *colly.HTMLElement) {
		if result.Description == "" {
			result.Description = e.Attr("content")
		}
	})
	c.OnHTML(`meta[property="og:description"]`, func(e *colly.HTMLElement) {
		if result.Description == "" {
			result.Description = e.Attr("content")
		}
	})

	// 提取 mailto 链接中的邮箱
	c.OnHTML(`a[href^="mailto:"]`, func(e *colly.HTMLElement) {
		href := e.Attr("href")
		email := strings.TrimPrefix(href, "mailto:")
		email = strings.Split(email, "?")[0] // 去掉?subject=等参数
		email = strings.TrimSpace(email)
		if email != "" && !emailSet[email] {
			emailSet[email] = true
		}
	})

	// 提取 tel 链接中的电话
	c.OnHTML(`a[href^="tel:"]`, func(e *colly.HTMLElement) {
		href := e.Attr("href")
		phone := strings.TrimPrefix(href, "tel:")
		phone = strings.TrimSpace(phone)
		if phone != "" && !phoneSet[phone] {
			phoneSet[phone] = true
		}
	})

	// 提取社交媒体链接
	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		href := e.Attr("href")
		socialPlatforms := []string{
			"facebook.com", "twitter.com", "x.com", "instagram.com",
			"linkedin.com", "youtube.com", "tiktok.com", "pinterest.com",
			"weibo.com", "wechat.com",
		}
		for _, platform := range socialPlatforms {
			if strings.Contains(href, platform) && !socialSet[href] {
				socialSet[href] = true
				break
			}
		}
	})

	// 提取页面文本（用于正则匹配邮箱和电话）
	c.OnHTML("body", func(e *colly.HTMLElement) {
		text := e.Text
		// 限制收集的文本长度
		if len(text) > 10000 {
			text = text[:10000]
		}
		allText.WriteString(text)
	})

	// 访问 contact 等子页面
	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		linkLower := strings.ToLower(link)
		textLower := strings.ToLower(e.Text)
		// 匹配联系我们/about等页面
		contactKeywords := []string{"contact", "about", "impressum", "kontakt", "联系"}
		for _, kw := range contactKeywords {
			if strings.Contains(linkLower, kw) || strings.Contains(textLower, kw) {
				e.Request.Visit(link)
				break
			}
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		result.Error = fmt.Sprintf("爬取失败: %v", err)
	})

	if err := c.Visit(websiteURL); err != nil {
		result.Error = fmt.Sprintf("访问失败: %v", err)
		return result
	}

	c.Wait()

	// 从全文中用正则提取邮箱和电话
	bodyText := allText.String()
	for _, email := range emailRegex.FindAllString(bodyText, -1) {
		email = strings.ToLower(email)
		// 过滤常见无效邮箱
		if isValidEmail(email) && !emailSet[email] {
			emailSet[email] = true
		}
	}
	for _, phone := range phoneRegex.FindAllString(bodyText, -1) {
		phone = strings.TrimSpace(phone)
		if len(phone) >= 7 && !phoneSet[phone] {
			phoneSet[phone] = true
		}
	}

	// 转换为切片
	for email := range emailSet {
		result.Emails = append(result.Emails, email)
	}
	for phone := range phoneSet {
		result.Phones = append(result.Phones, phone)
	}
	for link := range socialSet {
		result.SocialLinks = append(result.SocialLinks, link)
	}

	// 保留精简的body文本用于过滤
	if len(bodyText) > 2000 {
		bodyText = bodyText[:2000]
	}
	result.BodyText = cleanText(bodyText)
	result.Success = result.Error == ""

	return result
}

// BatchScrapeWebsites 批量爬取
func (s *ScraperService) BatchScrapeWebsites(urls []string) []*ScrapedPage {
	results := make([]*ScrapedPage, 0, len(urls))
	for _, u := range urls {
		results = append(results, s.ScrapeWebsite(u))
	}
	return results
}

// isValidEmail 过滤无效邮箱
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

// cleanText 清理网页文本
func cleanText(text string) string {
	// 合并连续空白
	spaceRegex := regexp.MustCompile(`\s+`)
	text = spaceRegex.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}
