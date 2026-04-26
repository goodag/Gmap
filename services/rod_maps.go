package services

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"googleMap/config"
	"googleMap/models"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// RodMapsService 使用无头浏览器直接爬取 Google Maps
type RodMapsService struct {
	proxyAddr  string
	chromePath string
	stopFlag   bool
	mu         sync.Mutex
}

func NewRodMapsService() *RodMapsService {
	cfg := config.Get()
	return &RodMapsService{
		proxyAddr:  cfg.Proxy.Address,
		chromePath: cfg.Proxy.ChromePath,
		stopFlag:   false,
	}
}

// Stop 停止所有正在进行的搜索任务
func (s *RodMapsService) Stop() {
	s.mu.Lock()
	s.stopFlag = true
	s.mu.Unlock()
	log.Println("[Rod] 收到停止信号，将停止所有搜索任务")
}

// ResetStopFlag 重置停止标志
func (s *RodMapsService) ResetStopFlag() {
	s.mu.Lock()
	s.stopFlag = false
	s.mu.Unlock()
}

// ShouldStop 检查是否应该停止
func (s *RodMapsService) ShouldStop() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopFlag
}

// RodSearchRequest Rod搜索请求
type RodSearchRequest struct {
	Keyword   string  `json:"keyword"`
	City      string  `json:"city"` // 城市名（可选，如 "New York"、"Tokyo"）
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Radius    int     `json:"radius"` // 米
	MaxCount  int     `json:"max_count"`
}

// RodConcurrentRequest 并发搜索请求
type RodConcurrentRequest struct {
	Keyword    string                                            `json:"keyword"`
	Cities     []string                                          `json:"cities"`     // 多个城市
	MaxCount   int                                               `json:"max_count"`  // 每个城市最大数量
	Concurrent int                                               `json:"concurrent"` // 并发数（同时开几个浏览器）
	OnProgress func(city string, index, total, fetchedCount int) // 进度回调函数
}

// RodConcurrentResult 单个城市的搜索结果
type RodConcurrentResult struct {
	City      string           `json:"city"`
	Companies []models.Company `json:"companies"`
	Error     string           `json:"error,omitempty"`
	Duration  string           `json:"duration"`
}

// RodBusinessResult 一个商家的原始数据
type RodBusinessResult struct {
	Name           string   `json:"name"`
	Address        string   `json:"address"`
	Phone          string   `json:"phone"`
	Website        string   `json:"website"`
	Rating         float64  `json:"rating"`
	ReviewCount    int      `json:"review_count"`
	Category       string   `json:"category"`
	PlaceID        string   `json:"place_id"`
	Latitude       float64  `json:"latitude"`
	Longitude      float64  `json:"longitude"`
	OpeningHours   []string `json:"opening_hours"`
	BusinessStatus string   `json:"business_status"`
	Href           string   `json:"href"` // Google Maps 详情页链接
}

// SearchNearby 用 Rod 在 Google Maps 搜索附近商家
func (s *RodMapsService) SearchNearby(req RodSearchRequest) ([]models.Company, error) {
	startTime := time.Now()
	if req.MaxCount <= 0 {
		req.MaxCount = 30
	}
	if req.MaxCount > 60 {
		req.MaxCount = 60
	}

	browser, err := s.launchBrowser()
	if err != nil {
		return nil, fmt.Errorf("启动浏览器失败: %w", err)
	}
	defer browser.MustClose()

	page, err := browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return nil, fmt.Errorf("创建页面失败: %w", err)
	}

	// 设置视口大小
	page.MustSetViewport(1920, 1080, 1, false)

	// 注入反检测脚本
	s.injectStealth(page)

	// 构建 Google Maps 搜索 URL
	searchURL := s.buildSearchURL(req)
	log.Printf("[Rod] 访问: %s", searchURL)

	err = page.Navigate(searchURL)
	if err != nil {
		return nil, fmt.Errorf("导航失败: %w", err)
	}

	// 等待页面加载（Google Maps SPA 用 WaitStable 代替 WaitLoad）
	_ = page.Timeout(15 * time.Second).WaitStable(time.Second)
	if err != nil {
		return nil, fmt.Errorf("页面加载超时: %w", err)
	}

	// EvalOnNewDocument 会在每个新文档自动执行，无需再次注入

	// 等待 Google Maps 动态内容加载
	time.Sleep(3 * time.Second)

	// 截图调试
	s.debugScreenshot(page, "01_after_load")

	// 关闭可能出现的 cookie 同意弹窗
	s.dismissDialogs(page)
	time.Sleep(1 * time.Second)

	s.debugScreenshot(page, "02_after_dismiss")

	// 等待搜索结果列表出现
	err = s.waitForResults(page)
	if err != nil {
		// 尝试截图查看页面状态
		s.debugScreenshot(page, "03_no_results")
		log.Printf("[Rod] 页面 HTML 片段: %s", s.getPageSnippet(page))
		return nil, fmt.Errorf("等待搜索结果超时: %w", err)
	}

	s.debugScreenshot(page, "04_results_found")

	// 滚动加载更多结果
	businesses := s.scrollAndCollect(page, req.MaxCount)

	log.Printf("[Rod] 列表收集完成，共 %d 个商家，耗时 %v", len(businesses), time.Since(startTime))

	// 获取每个商家的详情（总超时 3 分钟）
	deadline := time.Now().Add(3 * time.Minute)
	companies := make([]models.Company, 0, len(businesses))
	for i, biz := range businesses {
		// 检查是否需要停止
		if s.ShouldStop() {
			log.Printf("[Rod] 收到停止信号，停止获取详情，已完成 %d/%d", i, len(businesses))
			break
		}

		if time.Now().After(deadline) {
			log.Printf("[Rod] 详情获取超时，已完成 %d/%d", i, len(businesses))
			break
		}
		log.Printf("[Rod] 获取详情 %d/%d: %s (总耗时 %v)", i+1, len(businesses), biz.Name, time.Since(startTime))
		detail := s.getBusinessDetail(page, biz)
		company := s.convertToCompany(detail)
		companies = append(companies, company)
	}

	log.Printf("[Rod] 搜索完成: %d 个商家，总耗时 %v", len(companies), time.Since(startTime))
	return companies, nil
}

// ConcurrentSearch 并发搜索多个城市/地区
func (s *RodMapsService) ConcurrentSearch(req RodConcurrentRequest) []RodConcurrentResult {
	// 重置停止标志
	s.ResetStopFlag()

	if req.Concurrent <= 0 {
		req.Concurrent = 1
	}
	if req.Concurrent > 5 {
		req.Concurrent = 5 // 最多5个并发，避免资源耗尽
	}
	if req.MaxCount <= 0 {
		req.MaxCount = 30
	}

	results := make([]RodConcurrentResult, len(req.Cities))
	totalFetched := 0
	progressLock := sync.Mutex{}

	// 并发任务开始前先做一次浏览器预检查，避免每个城市都重复等待失败
	if err := s.EnsureBrowser(); err != nil {
		for i, city := range req.Cities {
			city = strings.TrimSpace(city)
			if city == "" {
				results[i] = RodConcurrentResult{City: city, Error: "城市名为空", Duration: "0s"}
				continue
			}
			results[i] = RodConcurrentResult{City: city, Error: err.Error(), Duration: "0s"}
		}
		return results
	}

	sem := make(chan struct{}, req.Concurrent) // 信号量控制并发数
	var wg sync.WaitGroup

	for i, city := range req.Cities {
		city = strings.TrimSpace(city)
		if city == "" {
			results[i] = RodConcurrentResult{City: city, Error: "城市名为空"}
			continue
		}

		wg.Add(1)
		go func(idx int, cityName string) {
			defer wg.Done()

			// 检查是否已停止
			if s.ShouldStop() {
				log.Printf("[Rod-并发] 任务已停止，跳过城市: %s", cityName)
				results[idx] = RodConcurrentResult{City: cityName, Error: "任务已停止"}
				return
			}

			sem <- struct{}{}        // 获取信号量
			defer func() { <-sem }() // 释放信号量

			start := time.Now()
			log.Printf("[Rod-并发] 开始搜索 [%d/%d]: %s + %s", idx+1, len(req.Cities), req.Keyword, cityName)

			// 再次检查是否已停止
			if s.ShouldStop() {
				log.Printf("[Rod-并发] 任务已停止，取消搜索: %s", cityName)
				results[idx] = RodConcurrentResult{City: cityName, Error: "任务已停止"}
				return
			}

			companies, err := s.SearchNearby(RodSearchRequest{
				Keyword:  req.Keyword,
				City:     cityName,
				MaxCount: req.MaxCount,
			})

			duration := time.Since(start)
			result := RodConcurrentResult{
				City:     cityName,
				Duration: fmt.Sprintf("%.1fs", duration.Seconds()),
			}

			if err != nil {
				result.Error = err.Error()
				log.Printf("[Rod-并发] 搜索失败 [%s]: %v (耗时 %s)", cityName, err, result.Duration)
			} else {
				result.Companies = companies
				log.Printf("[Rod-并发] 搜索完成 [%s]: %d 家商家 (耗时 %s)", cityName, len(companies), result.Duration)
			}

			results[idx] = result

			// 更新进度
			progressLock.Lock()
			totalFetched += len(result.Companies)
			if req.OnProgress != nil {
				req.OnProgress(cityName, idx, len(req.Cities), totalFetched)
			}
			progressLock.Unlock()
		}(i, city)
	}

	wg.Wait()
	return results
}

// launchBrowser 启动浏览器（带反检测）
// EnsureBrowser 预下载并检查 Chromium 是否可用（应在启动时调用一次）
// findChromeBin 按优先级查找可用的 Chrome/Chromium 路径
func (s *RodMapsService) findChromeBin() string {
	// 1. 用户配置的路径优先
	if s.chromePath != "" {
		if _, err := os.Stat(s.chromePath); err == nil {
			log.Printf("[Rod] 使用配置的 Chrome: %s", s.chromePath)
			return s.chromePath
		}
		log.Printf("[Rod] 配置的 chrome_path 不存在: %s", s.chromePath)
	}

	// 2. 环境变量指定路径（部署场景常用）
	envChrome := strings.TrimSpace(os.Getenv("CHROME_BIN"))
	if envChrome != "" {
		if _, err := os.Stat(envChrome); err == nil {
			log.Printf("[Rod] 使用 CHROME_BIN: %s", envChrome)
			return envChrome
		}
		log.Printf("[Rod] CHROME_BIN 指向文件不存在: %s", envChrome)
	}
	envChrome = strings.TrimSpace(os.Getenv("CHROME_PATH"))
	if envChrome != "" {
		if _, err := os.Stat(envChrome); err == nil {
			log.Printf("[Rod] 使用 CHROME_PATH: %s", envChrome)
			return envChrome
		}
		log.Printf("[Rod] CHROME_PATH 指向文件不存在: %s", envChrome)
	}

	// 3. 系统安装的 Chrome/Chromium
	candidates := []string{
		"/usr/bin/google-chrome-stable",
		"/usr/bin/google-chrome",
		"/opt/google/chrome/google-chrome",
		"/opt/google/chrome/chrome",
		"/usr/bin/microsoft-edge-stable",
		"/usr/bin/microsoft-edge",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/usr/lib64/chromium-browser/chromium-browser",
		"/snap/bin/chromium",
		"/usr/lib/chromium-browser/chromium-browser",
		// Windows 路径
		"C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
		"C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			log.Printf("[Rod] 找到系统 Chrome: %s", path)
			return path
		}
	}

	// 不再回退到 Rod 自动下载的 Chromium，避免在旧 glibc 环境长时间等待后失败
	log.Println("[Rod] ⚠️ 未找到可用系统浏览器，已禁用 Rod 内置 Chromium 回退")
	return ""
}

func (s *RodMapsService) requireChromeBin() (string, error) {
	chromeBin := s.findChromeBin()
	if chromeBin != "" {
		return chromeBin, nil
	}

	return "", fmt.Errorf("未找到可用 Chrome/Chromium。请安装系统浏览器并在 config.json 中设置 proxy.chrome_path，或设置环境变量 CHROME_BIN（CentOS 7 禁止使用 Rod 自动下载 Chromium，因 GLIBC 版本不兼容）")
}

func (s *RodMapsService) EnsureBrowser() error {
	log.Println("[Rod] 检查浏览器环境...")

	chromeBin, err := s.requireChromeBin()
	if err != nil {
		log.Printf("[Rod] ❌ 浏览器预检查失败: %v", err)
		log.Println("[Rod] 💡 建议:")
		log.Println("[Rod]   1) CentOS 7 安装系统 Chrome 并配置 proxy.chrome_path")
		log.Println("[Rod]   2) 或设置环境变量 CHROME_BIN=/usr/bin/google-chrome-stable")
		return err
	}

	l := launcher.New().
		Leakless(false).
		Headless(true).
		Set("no-sandbox").
		Set("disable-gpu").
		Set("disable-dev-shm-usage").
		Bin(chromeBin)

	controlURL, err := l.Launch()
	if err != nil {
		log.Printf("[Rod] ❌ 浏览器启动失败: %v", err)
		log.Println("[Rod] 💡 请安装依赖库:")
		log.Println("[Rod]   sudo yum install -y atk at-spi2-atk cups-libs libXcomposite libXdamage libXrandr mesa-libgbm pango alsa-lib gtk3 nss libdrm libxkbcommon")
		return fmt.Errorf("浏览器启动失败: %v", err)
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("浏览器连接失败: %v", err)
	}
	browser.MustClose()

	log.Printf("[Rod] ✅ 浏览器就绪 (使用: %s)", chromeBin)
	return nil
}

func (s *RodMapsService) launchBrowser() (*rod.Browser, error) {
	chromeBin, err := s.requireChromeBin()
	if err != nil {
		return nil, err
	}

	l := launcher.New().
		Leakless(false).
		Headless(true).
		Set("disable-gpu").
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-setuid-sandbox").
		Set("lang", "en-US").
		Set("disable-blink-features", "AutomationControlled").
		Set("window-size", "1920,1080").
		Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36").
		Set("disable-features", "TranslateUI").
		Set("disable-extensions").
		Set("disable-component-extensions-with-background-pages").
		Bin(chromeBin)

	if s.proxyAddr != "" {
		l = l.Proxy(s.proxyAddr)
	}

	log.Println("[Rod] 正在启动浏览器...")
	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("启动浏览器失败: %v", err)
	}
	log.Println("[Rod] 浏览器已启动，正在连接...")

	browser := rod.New().ControlURL(controlURL)
	err = browser.Connect()
	if err != nil {
		return nil, fmt.Errorf("连接浏览器失败: %v", err)
	}

	log.Println("[Rod] 浏览器连接成功")
	return browser, nil
}

// injectStealth 注入反检测脚本（使用 EvalOnNewDocument，通过 CDP 直接注入，不经过 Function.apply）
func (s *RodMapsService) injectStealth(page *rod.Page) {
	stealth := `
		Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
		window.chrome = { runtime: {}, loadTimes: function(){}, csi: function(){} };
		Object.defineProperty(navigator, 'plugins', { get: () => [1, 2, 3, 4, 5] });
		Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en'] });
		if (window.navigator && window.navigator.permissions) {
			var origQuery = window.navigator.permissions.query.bind(window.navigator.permissions);
			window.navigator.permissions.query = function(params) {
				if (params.name === 'notifications') {
					return Promise.resolve({ state: Notification.permission });
				}
				return origQuery(params);
			};
		}
	`
	// EvalOnNewDocument 使用 CDP Page.addScriptToEvaluateOnNewDocument，不会被 Function.apply 包装
	_, err := page.EvalOnNewDocument(stealth)
	if err != nil {
		log.Printf("[Rod] stealth注入警告: %v", err)
	}
}

// buildSearchURL 构建 Google Maps 搜索链接
func (s *RodMapsService) buildSearchURL(req RodSearchRequest) string {
	// 如果有城市名但没有坐标，直接用 "keyword in city" 格式搜索
	if req.City != "" && req.Latitude == 0 && req.Longitude == 0 {
		query := req.Keyword
		if query != "" {
			query = query + " in " + req.City
		} else {
			query = req.City
		}
		return "https://www.google.com/maps/search/" + url.PathEscape(query)
	}

	// 如果有城市名+坐标，用城市名辅助搜索更精确
	if req.City != "" {
		query := req.Keyword
		if query != "" {
			query = query + " in " + req.City
		} else {
			query = req.City
		}
		zoom := s.radiusToZoom(req.Radius)
		return fmt.Sprintf("https://www.google.com/maps/search/%s/@%f,%f,%dz",
			url.PathEscape(query), req.Latitude, req.Longitude, zoom)
	}

	// 仅有坐标（原有逻辑）
	zoom := s.radiusToZoom(req.Radius)
	keyword := url.PathEscape(req.Keyword)
	return fmt.Sprintf("https://www.google.com/maps/search/%s/@%f,%f,%dz",
		keyword, req.Latitude, req.Longitude, zoom)
}

// radiusToZoom 将半径（米）转为 Google Maps 缩放级别
func (s *RodMapsService) radiusToZoom(radius int) int {
	if radius <= 0 {
		return 15
	}
	// 近似公式：zoom ≈ 15 - log2(radius / 1000)
	zoom := 15 - int(math.Log2(float64(radius)/1000.0))
	if zoom < 5 {
		zoom = 5
	}
	if zoom > 20 {
		zoom = 20
	}
	return zoom
}

// dismissDialogs 关闭 cookie 同意等弹窗
func (s *RodMapsService) dismissDialogs(page *rod.Page) {
	// Google cookie consent
	selectors := []string{
		"button[aria-label='Accept all']",
		"button[aria-label='Reject all']",
		"form[action='https://consent.google.com/save'] button",
		".VfPpkd-LgbsSe[data-mdc-dialog-action='accept']",
	}
	for _, sel := range selectors {
		el, err := page.Timeout(2 * time.Second).Element(sel)
		if err == nil && el != nil {
			_ = el.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(1 * time.Second)
			return
		}
	}
}

// waitForResults 等待搜索结果列表出现
func (s *RodMapsService) waitForResults(page *rod.Page) error {
	// Google Maps 搜索结果的多种可能选择器
	selectors := []string{
		"div[role='feed']",           // 搜索结果列表容器
		"div.Nv2PK",                  // 商家卡片
		"a[href*='/maps/place/']",    // 商家链接
		"div.m6QErb.DxyBCb",          // 侧边栏滚动容器
		"div[aria-label*='Results']", // 搜索结果区域
		"div[aria-label*='results']", // 搜索结果（小写）
		"div.qjESne",                 // 搜索结果项
	}
	for attempt := 0; attempt < 2; attempt++ {
		if s.hasNoResultsMessage(page) {
			return fmt.Errorf("搜索无结果")
		}
		for _, sel := range selectors {
			el, err := page.Timeout(3 * time.Second).Element(sel)
			if err == nil && el != nil {
				log.Printf("[Rod] 找到结果容器: %s", sel)
				return nil
			}
		}
		// 没找到，可能页面还在加载，等待后重试
		log.Printf("[Rod] 第 %d 次未找到结果，等待重试...", attempt+1)
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("未找到搜索结果列表")
}

// hasNoResultsMessage 检查页面是否出现明确的无结果提示
func (s *RodMapsService) hasNoResultsMessage(page *rod.Page) bool {
	body, err := page.Timeout(1200 * time.Millisecond).Element("body")
	if err != nil || body == nil {
		return false
	}
	text, err := body.Text()
	if err != nil {
		return false
	}
	text = strings.ToLower(text)
	keywords := []string{
		"no results found",
		"did not match any locations",
		"找不到结果",
		"未找到结果",
		"没有找到",
	}
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			log.Printf("[Rod] 检测到无结果提示: %s", kw)
			return true
		}
	}
	return false
}

// scrollAndCollect 滚动搜索结果并收集商家基本信息
func (s *RodMapsService) scrollAndCollect(page *rod.Page, maxCount int) []RodBusinessResult {
	results := make([]RodBusinessResult, 0)
	seenNames := make(map[string]bool)
	noNewCount := 0

	if s.hasNoResultsMessage(page) {
		log.Printf("[Rod] 页面明确提示无结果，快速结束收集")
		return results
	}

	for i := 0; i < 12; i++ { // 最多滚动12次，避免无结果场景长时间等待
		// 检查是否需要停止
		if s.ShouldStop() {
			log.Printf("[Rod] 收到停止信号，停止滚动收集，已收集 %d 个商家", len(results))
			break
		}

		// 提取当前可见的商家
		newItems := s.extractListItems(page)
		addedAny := false
		for _, item := range newItems {
			key := item.Name + "|" + item.Address
			if !seenNames[key] && item.Name != "" {
				seenNames[key] = true
				results = append(results, item)
				addedAny = true
			}
		}

		if len(results) >= maxCount {
			break
		}

		if !addedAny {
			noNewCount++
			if noNewCount >= 2 {
				break // 连续2次没有新数据，说明到底了
			}
		} else {
			noNewCount = 0
		}

		// 滚动搜索结果面板
		s.scrollResultsPanel(page)
		time.Sleep(1 * time.Second)
	}

	if len(results) > maxCount {
		results = results[:maxCount]
	}

	log.Printf("[Rod] 共收集 %d 个商家", len(results))
	return results
}

// extractListItems 使用 Rod 原生 DOM 方法提取商家列表（彻底避免 JS eval）
func (s *RodMapsService) extractListItems(page *rod.Page) []RodBusinessResult {
	results := make([]RodBusinessResult, 0)
	seenNames := make(map[string]bool)

	// 方式1: 通过 a[href*="/maps/place/"] 找所有商家链接
	links, err := page.Elements(`a[href*="/maps/place/"]`)
	if err != nil {
		log.Printf("[Rod] 查找商家链接失败: %v", err)
	} else {
		log.Printf("[Rod] 找到 %d 个 place 链接", len(links))
		for _, link := range links {
			biz := s.extractFromLink(link)
			if biz.Name != "" && !seenNames[biz.Name] {
				seenNames[biz.Name] = true
				results = append(results, biz)
			}
		}
	}

	// 方式2: 如果方式1没找到，尝试 aria-label 链接
	if len(results) == 0 {
		ariaLinks, err := page.Elements(`a[aria-label][href*="google.com/maps"]`)
		if err == nil {
			log.Printf("[Rod] 找到 %d 个 aria-label 链接", len(ariaLinks))
			for _, link := range ariaLinks {
				label, _ := link.Attribute("aria-label")
				if label == nil || *label == "" {
					continue
				}
				name := strings.TrimSpace(*label)
				// 跳过功能按钮
				if s.isSkipLabel(name) {
					continue
				}
				href := s.getAttr(link, "href")
				placeID, lat, lng := s.parseHref(href)
				if !seenNames[name] {
					seenNames[name] = true
					results = append(results, RodBusinessResult{
						Name:      name,
						PlaceID:   placeID,
						Latitude:  lat,
						Longitude: lng,
					})
				}
			}
		}
	}

	// 方式3: 如果还是没找到，遍历 feed 子元素
	if len(results) == 0 {
		feed, err := page.Element(`div[role='feed']`)
		if err == nil {
			children, err := feed.Elements(`:scope > div`)
			if err == nil {
				log.Printf("[Rod] feed 子元素: %d 个", len(children))
				for _, child := range children {
					biz := s.extractFromContainer(child)
					if biz.Name != "" && !seenNames[biz.Name] {
						seenNames[biz.Name] = true
						results = append(results, biz)
					}
				}
			}
		}
	}

	log.Printf("[Rod] 本次提取到 %d 个商家", len(results))
	return results
}

// extractFromLink 从一个 place 链接提取商家信息
func (s *RodMapsService) extractFromLink(link *rod.Element) RodBusinessResult {
	biz := RodBusinessResult{}

	href := s.getAttr(link, "href")
	biz.Href = href
	biz.PlaceID, biz.Latitude, biz.Longitude = s.parseHref(href)

	// 获取名称：先从 aria-label 取
	ariaLabel, _ := link.Attribute("aria-label")
	if ariaLabel != nil && *ariaLabel != "" {
		biz.Name = strings.TrimSpace(*ariaLabel)
	}

	// 尝试从父容器中找更多信息
	// 向上找容器：最多向上3层
	container := link
	for i := 0; i < 3; i++ {
		parent, err := container.Parent()
		if err != nil {
			break
		}
		container = parent
	}

	// 名称选择器
	if biz.Name == "" {
		nameSelectors := []string{".qBF1Pd", ".fontHeadlineSmall", ".NrDZNb", ".OSrXXb", ".SPZz6b", `[role="heading"]`}
		for _, sel := range nameSelectors {
			el, err := container.Element(sel)
			if err == nil {
				text, _ := el.Text()
				text = strings.TrimSpace(text)
				if text != "" {
					biz.Name = text
					break
				}
			}
		}
	}

	// 如果还没有名称，从链接文本内容取
	if biz.Name == "" {
		text, _ := link.Text()
		// 取第一行非空文本
		lines := strings.Split(text, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && len(line) > 2 && len(line) < 200 {
				biz.Name = line
				break
			}
		}
	}

	// 评分和评论数：从容器文本中正则提取
	containerText, _ := container.Text()
	biz.Rating = s.extractRating(containerText)
	biz.ReviewCount = s.extractReviewCount(containerText)

	// 类别和地址：从 span.W4Efsd 提取
	spans, err := container.Elements("span.W4Efsd span")
	if err == nil {
		textParts := make([]string, 0)
		for _, span := range spans {
			t, _ := span.Text()
			t = strings.TrimSpace(t)
			if t != "" && t != "·" && len(t) > 1 && !regexp.MustCompile(`^\([\d,]+\)$`).MatchString(t) && !regexp.MustCompile(`^\d\.\d$`).MatchString(t) {
				textParts = append(textParts, t)
			}
		}
		if len(textParts) >= 1 {
			biz.Category = textParts[0]
		}
		if len(textParts) >= 2 {
			biz.Address = textParts[1]
		}
	}

	return biz
}

// extractFromContainer 从一个容器元素中提取商家信息
func (s *RodMapsService) extractFromContainer(container *rod.Element) RodBusinessResult {
	biz := RodBusinessResult{}

	// 先找里面的 place 链接
	link, err := container.Element(`a[href*="/maps/place/"]`)
	if err == nil {
		return s.extractFromLink(link)
	}

	// 没有 place 链接，尝试从 aria-label 链接
	link, err = container.Element(`a[aria-label]`)
	if err == nil {
		label, _ := link.Attribute("aria-label")
		if label != nil && *label != "" && !s.isSkipLabel(*label) {
			biz.Name = strings.TrimSpace(*label)
			href := s.getAttr(link, "href")
			biz.PlaceID, biz.Latitude, biz.Longitude = s.parseHref(href)
		}
	}

	// 尝试 heading
	if biz.Name == "" {
		headingSelectors := []string{".qBF1Pd", ".fontHeadlineSmall", `[role="heading"]`, "h3", "h2"}
		for _, sel := range headingSelectors {
			el, err := container.Element(sel)
			if err == nil {
				text, _ := el.Text()
				text = strings.TrimSpace(text)
				if text != "" && len(text) < 200 {
					biz.Name = text
					break
				}
			}
		}
	}

	if biz.Name != "" {
		text, _ := container.Text()
		biz.Rating = s.extractRating(text)
		biz.ReviewCount = s.extractReviewCount(text)
	}

	return biz
}

// extractRating 从文本中提取评分
func (s *RodMapsService) extractRating(text string) float64 {
	// 匹配 "4.5" 形式的评分（1.0-5.0）
	re := regexp.MustCompile(`\b([1-5]\.\d)\b`)
	matches := re.FindAllString(text, -1)
	for _, m := range matches {
		f, _ := strconv.ParseFloat(m, 64)
		if f >= 1.0 && f <= 5.0 {
			return f
		}
	}
	return 0
}

// extractReviewCount 从文本中提取评论数
func (s *RodMapsService) extractReviewCount(text string) int {
	// 匹配 "(1,234)" 或 "(123)" 形式
	re := regexp.MustCompile(`\(([\d,]+)\)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		n, _ := strconv.Atoi(strings.ReplaceAll(matches[1], ",", ""))
		return n
	}
	return 0
}

// parseHref 从 Google Maps URL 中提取坐标和 placeID
func (s *RodMapsService) parseHref(href string) (placeID string, lat, lng float64) {
	if href == "" {
		return
	}
	coordRe := regexp.MustCompile(`@(-?\d+\.?\d*),(-?\d+\.?\d*)`)
	if m := coordRe.FindStringSubmatch(href); len(m) > 2 {
		lat, _ = strconv.ParseFloat(m[1], 64)
		lng, _ = strconv.ParseFloat(m[2], 64)
	}
	placeRe := regexp.MustCompile(`place/[^/]+/([^/?\s]+)`)
	if m := placeRe.FindStringSubmatch(href); len(m) > 1 {
		placeID = m[1]
	}
	return
}

// getAttr 安全获取元素属性
func (s *RodMapsService) getAttr(el *rod.Element, name string) string {
	val, _ := el.Attribute(name)
	if val == nil {
		return ""
	}
	return *val
}

// isSkipLabel 判断是否为功能按钮标签（应跳过）
func (s *RodMapsService) isSkipLabel(label string) bool {
	skipLabels := []string{"Close", "Menu", "Search", "Sign in", "Directions",
		"Layers", "Zoom in", "Zoom out", "Google Maps", "Send to your phone",
		"Share", "Saved", "Contribute", "Your timeline"}
	for _, skip := range skipLabels {
		if strings.EqualFold(label, skip) {
			return true
		}
	}
	return false
}

// scrollResultsPanel 滚动搜索结果面板（使用 Rod 原生方法）
func (s *RodMapsService) scrollResultsPanel(page *rod.Page) {
	// 尝试找到 feed 容器并滚动
	feedSelectors := []string{
		`div[role='feed']`,
		`.m6QErb.DxyBCb`,
		`.m6QErb`,
	}
	for _, sel := range feedSelectors {
		el, err := page.Timeout(1200 * time.Millisecond).Element(sel)
		if err == nil {
			// 使用 Mouse wheel 模拟滚动
			box, err := el.Shape()
			if err == nil && len(box.Quads) > 0 {
				quad := box.Quads[0]
				// 在元素中心位置滚动
				centerX := (quad[0] + quad[2] + quad[4] + quad[6]) / 4
				centerY := (quad[1] + quad[3] + quad[5] + quad[7]) / 4
				_ = page.Mouse.MoveTo(proto.Point{X: centerX, Y: centerY})
				_ = page.Mouse.Scroll(0, 600, 0)
				log.Printf("[Rod] 滚动容器: %s", sel)
				return
			}
		}
	}

	// 兜底: 按 End 键
	_ = page.Keyboard.Press(0x23) // End key
	log.Printf("[Rod] 使用 End 键滚动")
}

// getBusinessDetail 点击商家获取详细信息（使用 Rod 原生 DOM）
func (s *RodMapsService) getBusinessDetail(page *rod.Page, biz RodBusinessResult) RodBusinessResult {
	// 直接导航到商家详情页（避免虚拟滚动导致元素不在 DOM 中）
	if biz.Href != "" {
		// Google Maps 是 SPA，Navigate 不能等 load 事件，用 NavigateE + WaitStable 代替
		err := page.Navigate(biz.Href)
		if err != nil {
			log.Printf("[Rod] 导航到详情页失败: %s, err: %v", biz.Name, err)
			return biz
		}
		// 不用 WaitLoad（SPA 可能不触发 load），改用 WaitStable 等内容稳定
		_ = page.Timeout(5 * time.Second).WaitStable(300 * time.Millisecond)
		time.Sleep(1 * time.Second)
	} else {
		log.Printf("[Rod] 无详情链接: %s", biz.Name)
		return biz
	}

	// 用短超时的 page 查找详情元素，避免阻塞
	dp := page.Timeout(3 * time.Second)

	// 电话
	phoneSelectors := []string{
		`button[data-item-id*="phone"] .Io6YTe`,
		`a[data-item-id*="phone"]`,
		`button[data-tooltip="Copy phone number"]`,
		`[data-item-id*="phone"] .rogA2c`,
	}
	for _, sel := range phoneSelectors {
		el, err := dp.Element(sel)
		if err == nil {
			text, _ := el.Text()
			text = strings.TrimSpace(text)
			if text != "" {
				biz.Phone = text
				break
			}
			// 从 aria-label 获取
			al, _ := el.Attribute("aria-label")
			if al != nil && *al != "" {
				biz.Phone = s.extractPhoneFromLabel(*al)
				break
			}
		}
	}

	// 网站
	webSelectors := []string{
		`a[data-item-id="authority"]`,
		`a[data-tooltip="Open website"]`,
	}
	for _, sel := range webSelectors {
		el, err := dp.Element(sel)
		if err == nil {
			href, _ := el.Attribute("href")
			if href != nil && *href != "" {
				biz.Website = *href
				break
			}
			text, _ := el.Text()
			if strings.TrimSpace(text) != "" {
				biz.Website = strings.TrimSpace(text)
				break
			}
		}
	}

	// 地址
	addrSelectors := []string{
		`button[data-item-id="address"] .Io6YTe`,
		`button[data-item-id="address"] .rogA2c`,
		`[data-item-id="address"]`,
	}
	for _, sel := range addrSelectors {
		el, err := dp.Element(sel)
		if err == nil {
			text, _ := el.Text()
			text = strings.TrimSpace(text)
			if text != "" {
				biz.Address = text
				break
			}
		}
	}

	// 营业时间
	hourRows, err := dp.Elements(`table.eK4R0e tr, table.WgFkxc tr`)
	if err == nil {
		hours := make([]string, 0)
		for _, row := range hourRows {
			cols, err := row.Elements("td")
			if err == nil && len(cols) >= 2 {
				day, _ := cols[0].Text()
				time, _ := cols[len(cols)-1].Text()
				if strings.TrimSpace(day) != "" {
					hours = append(hours, strings.TrimSpace(day)+": "+strings.TrimSpace(time))
				}
			}
		}
		biz.OpeningHours = hours
	}

	// 营业状态
	statusSelectors := []string{".ZDu9vd span", ".o0Svhf"}
	for _, sel := range statusSelectors {
		el, err := dp.Element(sel)
		if err == nil {
			text, _ := el.Text()
			text = strings.ToLower(text)
			if strings.Contains(text, "permanently") || strings.Contains(text, "closed") {
				biz.BusinessStatus = "CLOSED_PERMANENTLY"
			} else if strings.Contains(text, "temporarily") {
				biz.BusinessStatus = "CLOSED_TEMPORARILY"
			}
			break
		}
	}
	if biz.BusinessStatus == "" {
		biz.BusinessStatus = "OPERATIONAL"
	}

	return biz
}

// extractPhoneFromLabel 从 aria-label 中提取电话号码
func (s *RodMapsService) extractPhoneFromLabel(label string) string {
	re := regexp.MustCompile(`[\d\s\-\+\(\)]{7,}`)
	m := re.FindString(label)
	return strings.TrimSpace(m)
}

// convertToCompany 将 Rod 结果转为 Company model
func (s *RodMapsService) convertToCompany(biz RodBusinessResult) models.Company {
	domain := ""
	if biz.Website != "" {
		domain = extractDomainFromURL(biz.Website)
	}

	var openHoursJSON string
	if len(biz.OpeningHours) > 0 {
		b, _ := json.Marshal(biz.OpeningHours)
		openHoursJSON = string(b)
	}

	placeID := biz.PlaceID
	if placeID == "" || len(placeID) > 200 {
		// place_id 为空或是 Google Maps 内部长链接（data:4m7!...），用 MD5 生成短ID
		raw := biz.Name + "|" + biz.Address
		if biz.Href != "" {
			raw = biz.Href
		}
		hash := md5.Sum([]byte(raw))
		placeID = fmt.Sprintf("rod_%x", hash)
	}

	return models.Company{
		Source:           "rod",
		PlaceID:          placeID,
		Name:             biz.Name,
		FormattedAddress: biz.Address,
		Phone:            biz.Phone,
		Website:          biz.Website,
		Domain:           domain,
		Rating:           biz.Rating,
		UserRatingsTotal: biz.ReviewCount,
		Types:            biz.Category,
		Latitude:         biz.Latitude,
		Longitude:        biz.Longitude,
		BusinessStatus:   biz.BusinessStatus,
		OpeningHours:     openHoursJSON,
	}
}

// jsonEscape 将字符串转为 JSON 安全格式
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// sanitize 清理字符串作为 ID
func sanitize(s string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9]`)
	result := re.ReplaceAllString(s, "")
	if len(result) > 30 {
		result = result[:30]
	}
	return strings.ToLower(result)
}

// debugScreenshot 保存调试截图到临时目录
func (s *RodMapsService) debugScreenshot(page *rod.Page, name string) {
	data, err := page.Screenshot(true, nil)
	if err != nil {
		log.Printf("[Rod] 截图失败 (%s): %v", name, err)
		return
	}
	filename := fmt.Sprintf("rod_debug_%s.png", name)
	if err := os.WriteFile(filename, data, 0644); err != nil {
		log.Printf("[Rod] 保存截图失败 (%s): %v", name, err)
		return
	}
	log.Printf("[Rod] 调试截图已保存: %s (%d bytes)", filename, len(data))
}

// getPageSnippet 获取页面 HTML 片段用于调试
func (s *RodMapsService) getPageSnippet(page *rod.Page) string {
	el, err := page.Element("body")
	if err != nil {
		return "no body"
	}
	html, err := el.HTML()
	if err != nil {
		return "html error"
	}
	if len(html) > 2000 {
		return html[:2000]
	}
	return html
}

// SearchByText 纯文本搜索（不限定坐标）
func (s *RodMapsService) SearchByText(keyword string, maxCount int) ([]models.Company, error) {
	return s.SearchNearby(RodSearchRequest{
		Keyword:  keyword,
		MaxCount: maxCount,
	})
}

// GetZoomLevel 暴露给外部使用
func GetZoomLevel(radius int) int {
	svc := &RodMapsService{}
	return svc.radiusToZoom(radius)
}

// ParseRatingFromString 从字符串解析评分
func ParseRatingFromString(s string) float64 {
	re := regexp.MustCompile(`(\d+\.?\d*)`)
	m := re.FindString(s)
	if m == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(m, 64)
	return f
}
