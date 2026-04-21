package services

import (
	"strings"
)

// FilterConfig 过滤器配置
type FilterConfig struct {
	// 关键词过滤：网页内容包含任意关键词则保留（为空不过滤）
	IncludeKeywords []string `json:"include_keywords"`
	// 排除关键词：网页内容包含任意关键词则排除
	ExcludeKeywords []string `json:"exclude_keywords"`
	// 必须有邮箱
	RequireEmail bool `json:"require_email"`
	// 必须有电话
	RequirePhone bool `json:"require_phone"`
	// 必须有网站
	RequireWebsite bool `json:"require_website"`
	// 排除的域名后缀（如 .gov, .edu）
	ExcludeDomains []string `json:"exclude_domains"`
	// 最低评分
	MinRating float64 `json:"min_rating"`
	// 最少评价数
	MinReviews int `json:"min_reviews"`
	// 商家类型过滤（包含任意类型即保留）
	IncludeTypes []string `json:"include_types"`
	// 排除的商家类型
	ExcludeTypes []string `json:"exclude_types"`
}

// FilterResult 过滤结果
type FilterResult struct {
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
}

// ContentFilter 内容过滤器
type ContentFilter struct{}

func NewContentFilter() *ContentFilter {
	return &ContentFilter{}
}

// FilterBusiness 根据商家基本信息过滤
func (f *ContentFilter) FilterBusiness(name, email, phone, website, types string, rating float64, reviews int, cfg *FilterConfig) FilterResult {
	if cfg == nil {
		return FilterResult{Passed: true}
	}

	// 评分过滤
	if cfg.MinRating > 0 && rating < cfg.MinRating {
		return FilterResult{Passed: false, Reason: "评分低于最低要求"}
	}

	// 评价数过滤
	if cfg.MinReviews > 0 && reviews < cfg.MinReviews {
		return FilterResult{Passed: false, Reason: "评价数低于最低要求"}
	}

	// 邮箱必填
	if cfg.RequireEmail && email == "" {
		return FilterResult{Passed: false, Reason: "无邮箱信息"}
	}

	// 电话必填
	if cfg.RequirePhone && phone == "" {
		return FilterResult{Passed: false, Reason: "无电话信息"}
	}

	// 网站必填
	if cfg.RequireWebsite && website == "" {
		return FilterResult{Passed: false, Reason: "无网站信息"}
	}

	// 域名排除
	if len(cfg.ExcludeDomains) > 0 && website != "" {
		websiteLower := strings.ToLower(website)
		for _, domain := range cfg.ExcludeDomains {
			if strings.Contains(websiteLower, strings.ToLower(domain)) {
				return FilterResult{Passed: false, Reason: "域名被排除: " + domain}
			}
		}
	}

	// 商家类型过滤
	if len(cfg.IncludeTypes) > 0 {
		typesLower := strings.ToLower(types)
		found := false
		for _, t := range cfg.IncludeTypes {
			if strings.Contains(typesLower, strings.ToLower(t)) {
				found = true
				break
			}
		}
		if !found {
			return FilterResult{Passed: false, Reason: "不匹配指定商家类型"}
		}
	}

	if len(cfg.ExcludeTypes) > 0 {
		typesLower := strings.ToLower(types)
		for _, t := range cfg.ExcludeTypes {
			if strings.Contains(typesLower, strings.ToLower(t)) {
				return FilterResult{Passed: false, Reason: "匹配排除的商家类型: " + t}
			}
		}
	}

	return FilterResult{Passed: true}
}

// FilterScrapedContent 根据爬取的网页内容过滤
func (f *ContentFilter) FilterScrapedContent(page *ScrapedPage, cfg *FilterConfig) FilterResult {
	if cfg == nil || page == nil {
		return FilterResult{Passed: true}
	}

	contentLower := strings.ToLower(page.Title + " " + page.Description + " " + page.BodyText)

	// 包含关键词（任意一个即保留）
	if len(cfg.IncludeKeywords) > 0 {
		found := false
		for _, kw := range cfg.IncludeKeywords {
			if kw != "" && strings.Contains(contentLower, strings.ToLower(kw)) {
				found = true
				break
			}
		}
		if !found {
			return FilterResult{Passed: false, Reason: "网页内容不包含指定关键词"}
		}
	}

	// 排除关键词
	if len(cfg.ExcludeKeywords) > 0 {
		for _, kw := range cfg.ExcludeKeywords {
			if kw != "" && strings.Contains(contentLower, strings.ToLower(kw)) {
				return FilterResult{Passed: false, Reason: "网页内容包含排除关键词: " + kw}
			}
		}
	}

	// 要求爬取到邮箱
	if cfg.RequireEmail && len(page.Emails) == 0 {
		return FilterResult{Passed: false, Reason: "网站未找到邮箱"}
	}

	// 要求爬取到电话
	if cfg.RequirePhone && len(page.Phones) == 0 {
		return FilterResult{Passed: false, Reason: "网站未找到电话"}
	}

	return FilterResult{Passed: true}
}
