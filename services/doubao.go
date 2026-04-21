package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"googleMap/config"

	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

// AIAnalysisResult AI分析结果
type AIAnalysisResult struct {
	CompanyIntro string   `json:"company_intro"` // 公司简介
	MainBusiness string   `json:"main_business"` // 主营业务
	Industry     string   `json:"industry"`      // 行业分类
	TargetMarket string   `json:"target_market"` // 目标市场
	CompanySize  string   `json:"company_size"`  // 公司规模
	IsRelevant   bool     `json:"is_relevant"`   // 是否符合要求
	Score        int      `json:"score"`         // 匹配评分 0-100
	Reason       string   `json:"reason"`        // 判断理由
	Keywords     []string `json:"keywords"`      // 关键词标签
}

// DoubaoService 豆包AI分析服务
type DoubaoService struct {
	client  *arkruntime.Client
	modelID string
	enabled bool
}

func NewDoubaoService() *DoubaoService {
	cfg := config.Get().Doubao
	s := &DoubaoService{
		modelID: cfg.ModelID,
		enabled: cfg.Enabled,
	}
	if cfg.Enabled && cfg.APIKey != "" {
		s.client = arkruntime.NewClientWithApiKey(
			cfg.APIKey,
			arkruntime.WithBaseUrl(cfg.BaseURL),
		)
	}
	return s
}

func (s *DoubaoService) IsEnabled() bool {
	// AI智能分析模块已禁用
	return false
}

// AnalyzeCompany 分析公司网页内容、生成简介、判断是否符合要求
func (s *DoubaoService) AnalyzeCompany(name, website, pageTitle, description, bodyText, requirement string) (*AIAnalysisResult, error) {
	if !s.IsEnabled() {
		return nil, fmt.Errorf("豆包AI未启用，请配置 doubao.api_key 和 doubao.enabled")
	}

	// 截取 bodyText 避免超长
	if len(bodyText) > 3000 {
		bodyText = bodyText[:3000]
	}

	prompt := fmt.Sprintf(`请分析以下公司/网站信息，用中文回复，返回严格JSON格式（不要markdown代码块）：

公司名: %s
网站: %s
网页标题: %s
网页描述: %s
网页正文摘要: %s

用户的筛选要求: %s

请返回以下JSON格式：
{
  "company_intro": "一句话公司简介（50字以内）",
  "main_business": "主营业务",
  "industry": "行业分类",
  "target_market": "目标市场（如：全球、北美、欧洲、亚洲等）",
  "company_size": "规模推测（大型/中型/小型/个人）",
  "is_relevant": true或false（是否符合用户的筛选要求）,
  "score": 0到100的匹配评分,
  "reason": "判断理由（30字以内）",
  "keywords": ["关键词1", "关键词2", "关键词3"]
}`, name, website, pageTitle, description, bodyText, requirement)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	systemContent := "你是一个专业的商业分析助手，擅长分析公司网站信息并生成结构化数据。只返回JSON，不要返回其他内容。"
	resp, err := s.client.CreateChatCompletion(ctx, model.ChatCompletionRequest{
		Model: s.modelID,
		Messages: []*model.ChatCompletionMessage{
			{
				Role:    model.ChatMessageRoleSystem,
				Content: &model.ChatCompletionMessageContent{StringValue: &systemContent},
			},
			{
				Role:    model.ChatMessageRoleUser,
				Content: &model.ChatCompletionMessageContent{StringValue: &prompt},
			},
		},
		Temperature: 0.3,
	})
	if err != nil {
		return nil, fmt.Errorf("豆包API调用失败: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("豆包返回空结果")
	}

	content := *resp.Choices[0].Message.Content.StringValue
	// 清理可能的markdown代码块
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result AIAnalysisResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		log.Printf("[AI] JSON解析失败, 原始内容: %s, err: %v", content, err)
		// 尝试提取大括号内容
		start := strings.Index(content, "{")
		end := strings.LastIndex(content, "}")
		if start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(content[start:end+1]), &result); err2 != nil {
				return nil, fmt.Errorf("AI返回格式错误: %w", err2)
			}
		} else {
			return nil, fmt.Errorf("AI返回非JSON: %s", content)
		}
	}

	return &result, nil
}

// BatchAnalyze 批量分析多个公司
func (s *DoubaoService) BatchAnalyze(items []AnalyzeItem, requirement string) []AnalyzeItemResult {
	results := make([]AnalyzeItemResult, len(items))
	for i, item := range items {
		log.Printf("[AI] 分析 %d/%d: %s", i+1, len(items), item.Name)
		analysis, err := s.AnalyzeCompany(item.Name, item.Website, item.PageTitle, item.Description, item.BodyText, requirement)
		results[i] = AnalyzeItemResult{
			CompanyID: item.CompanyID,
		}
		if err != nil {
			log.Printf("[AI] 分析失败: %s, err: %v", item.Name, err)
			results[i].Error = err.Error()
			continue
		}
		results[i].Result = analysis
		// 控制请求频率
		if i < len(items)-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return results
}

type AnalyzeItem struct {
	CompanyID   uint64
	Name        string
	Website     string
	PageTitle   string
	Description string
	BodyText    string
}

type AnalyzeItemResult struct {
	CompanyID uint64            `json:"company_id"`
	Result    *AIAnalysisResult `json:"result,omitempty"`
	Error     string            `json:"error,omitempty"`
}
