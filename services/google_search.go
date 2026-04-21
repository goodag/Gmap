package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GoogleSearchItem 谷歌搜索单条结果
type GoogleSearchItem struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
}

// GoogleSearchResponse 谷歌 Custom Search API 响应
type GoogleSearchResponse struct {
	Items []GoogleSearchItem `json:"items"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// GoogleSearchService 谷歌搜索+colly爬取服务
type GoogleSearchService struct {
	apiKey  string
	cx      string // Custom Search Engine ID
	scraper *ScraperService
	filter  *ContentFilter
	client  *http.Client
}

func NewGoogleSearchService(apiKey, cx string) *GoogleSearchService {
	return &GoogleSearchService{
		apiKey:  apiKey,
		cx:      cx,
		scraper: NewScraperService(),
		filter:  NewContentFilter(),
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// SearchGoogle 调用 Google Custom Search API 搜索，返回 URL 列表
// pages: 搜索页数（每页10条），最多10页（100条）
func (s *GoogleSearchService) SearchGoogle(query, location, language string, pages int) ([]GoogleSearchItem, error) {
	if s.apiKey == "" || s.cx == "" {
		return nil, fmt.Errorf("未配置 Google Custom Search API Key 或 Custom Search Engine ID")
	}
	if pages < 1 {
		pages = 1
	}
	if pages > 10 {
		pages = 10
	}

	// 如果有地区限制，附加到查询
	q := query
	if location != "" {
		q = query + " " + location
	}

	var allItems []GoogleSearchItem

	for page := 0; page < pages; page++ {
		start := page*10 + 1 // Google Custom Search start index: 1, 11, 21, ...
		items, err := s.fetchPage(q, language, start)
		if err != nil {
			if page == 0 {
				return nil, err
			}
			break // 后续页出错则停止
		}
		allItems = append(allItems, items...)
		if len(items) < 10 {
			break // 不足 10 条说明已是最后一页
		}
		// 避免频繁调用 API
		if page < pages-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	return allItems, nil
}

func (s *GoogleSearchService) fetchPage(query, language string, start int) ([]GoogleSearchItem, error) {
	params := url.Values{}
	params.Set("key", s.apiKey)
	params.Set("cx", s.cx)
	params.Set("q", query)
	params.Set("start", fmt.Sprintf("%d", start))
	params.Set("num", "10")
	if language != "" {
		params.Set("hl", language)
		params.Set("lr", "lang_"+strings.Split(language, "-")[0])
	}

	apiURL := "https://www.googleapis.com/customsearch/v1?" + params.Encode()

	resp, err := s.client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("请求 Google Search API 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var result GoogleSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("Google API 错误 (%d): %s", result.Error.Code, result.Error.Message)
	}

	return result.Items, nil
}

// SearchAndScrapeRequest 搜索+爬取请求参数
type SearchAndScrapeRequest struct {
	Query    string        `json:"query" binding:"required"`
	Location string        `json:"location"`
	Language string        `json:"language"`
	Pages    int           `json:"pages"`
	Filter   *FilterConfig `json:"filter"`
}

// SearchAndScrapeResult 单个网站的搜索+爬取结果
type SearchAndScrapeResult struct {
	GoogleTitle   string       `json:"google_title"`
	GoogleSnippet string       `json:"google_snippet"`
	URL           string       `json:"url"`
	Domain        string       `json:"domain"`
	Page          *ScrapedPage `json:"page"`
	Filtered      bool         `json:"filtered"`
	FilterReason  string       `json:"filter_reason,omitempty"`
}

// RunSearchAndScrape 执行完整的搜索→爬取流程
// progressFn 是可选的进度回调，每完成一个URL时调用
func (s *GoogleSearchService) RunSearchAndScrape(
	req *SearchAndScrapeRequest,
	progressFn func(done, total int, result *SearchAndScrapeResult),
) ([]SearchAndScrapeResult, error) {
	// Step 1: 谷歌搜索获取 URL 列表
	lang := req.Language
	if lang == "" {
		lang = "zh-CN"
	}
	pages := req.Pages
	if pages == 0 {
		pages = 1
	}

	items, err := s.SearchGoogle(req.Query, req.Location, lang, pages)
	if err != nil {
		return nil, err
	}

	results := make([]SearchAndScrapeResult, 0, len(items))
	total := len(items)

	// Step 2: 对每个 URL 用 colly 爬取
	for i, item := range items {
		domain := extractDomain(item.Link)

		r := SearchAndScrapeResult{
			GoogleTitle:   item.Title,
			GoogleSnippet: item.Snippet,
			URL:           item.Link,
			Domain:        domain,
		}

		// 先做 URL/域名级别的过滤
		if req.Filter != nil && len(req.Filter.ExcludeDomains) > 0 {
			excluded := false
			for _, ed := range req.Filter.ExcludeDomains {
				if strings.Contains(domain, ed) {
					excluded = true
					r.Filtered = true
					r.FilterReason = "域名过滤: " + ed
					break
				}
			}
			if excluded {
				results = append(results, r)
				if progressFn != nil {
					progressFn(i+1, total, &r)
				}
				continue
			}
		}

		// colly 爬取网站
		page := s.scraper.ScrapeWebsite(item.Link)
		r.Page = page

		// 内容过滤
		if req.Filter != nil {
			filterResult := s.filter.FilterScrapedContent(page, req.Filter)
			if !filterResult.Passed {
				r.Filtered = true
				r.FilterReason = filterResult.Reason
			}
		}

		results = append(results, r)

		if progressFn != nil {
			progressFn(i+1, total, &r)
		}

		// 限速：每个网站爬取后间隔 500ms，避免对目标站点压力过大
		time.Sleep(500 * time.Millisecond)
	}

	return results, nil
}

func extractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := u.Hostname()
	host = strings.TrimPrefix(host, "www.")
	return host
}
