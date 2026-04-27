package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"

	"googleMap/config"
	"googleMap/handlers"
	"googleMap/models"
	"googleMap/services"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	cfg := config.Get()

	// 连接数据库（不强制要求成功）
	var db *gorm.DB
	var dbErr error
	db, dbErr = gorm.Open(mysql.Open(cfg.Database.DSN()), &gorm.Config{})
	if dbErr != nil {
		log.Printf("⚠️ 数据库连接失败: %v", dbErr)
		log.Println("⚠️ 将以无数据库模式启动，部分功能可能不可用")
		db = nil // 将 db 设置为 nil，以便后续检查
	}

	// 自动迁移（仅在数据库连接成功时）
	if db != nil {
		db.AutoMigrate(&models.SearchRecord{}, &models.Company{}, &models.CompanyEmail{}, &models.EmailLog{}, &models.CronTask{})
	}

	// 启动定时任务调度器（仅在数据库连接成功时）
	var scheduler *services.SchedulerService
	if db != nil {
		scheduler = services.NewSchedulerService(db)
		scheduler.Start()
	}

	// 预检查 Rod 浏览器（提前下载 Chromium 并验证依赖）
	rodService := services.NewRodMapsService()
	if err := rodService.EnsureBrowser(); err != nil {
		log.Printf("⚠️ Rod 浏览器预检查失败: %v", err)
		log.Println("⚠️ Rod 爬取模式可能无法使用，请安装 Chromium 依赖库")
	}

	// 初始化 handlers（仅在数据库连接成功时）
	var searchHandler *handlers.SearchHandler
	var emailHandler *handlers.EmailHandler
	var scraperHandler *handlers.ScraperHandler
	var googleSearchHandler *handlers.GoogleSearchHandler
	var cronHandler *handlers.CronHandler
	var exportHandler *handlers.ExportHandler

	if db != nil {
		searchHandler = handlers.NewSearchHandler(db)
		emailHandler = handlers.NewEmailHandler(db)
		scraperHandler = handlers.NewScraperHandler(db)
		googleSearchHandler = handlers.NewGoogleSearchHandler(db)
		cronHandler = handlers.NewCronHandler(db, scheduler)
		exportHandler = handlers.NewExportHandler(db)
	}

	cityHandler := handlers.NewCityHandler()

	r := gin.Default()

	// 加载模板
	tmpl := template.Must(template.ParseGlob("templates/*.html"))
	r.SetHTMLTemplate(tmpl)

	// 静态文件
	r.Static("/static", "./static")

	// 页面路由
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"googleAPIKey": cfg.Google.APIKey,
		})
	})
	r.GET("/admin", func(c *gin.Context) {
		c.HTML(http.StatusOK, "admin.html", nil)
	})
	r.GET("/google-search", func(c *gin.Context) {
		c.HTML(http.StatusOK, "google_search.html", nil)
	})
	r.GET("/cron", func(c *gin.Context) {
		c.HTML(http.StatusOK, "cron.html", nil)
	})

	// API 路由
	api := r.Group("/api")
	{
		// 数据库相关路由（仅在数据库连接成功时）
		if searchHandler != nil {
			api.POST("/search", searchHandler.Search)
			api.POST("/search/next", searchHandler.SearchNextPage)
			api.POST("/search/stop", searchHandler.StopSearch)
			api.GET("/search/progress", searchHandler.GetTaskProgress)
			api.GET("/search/status", searchHandler.GetTaskStatus)
			api.GET("/place/detail", searchHandler.GetPlaceDetail)
			api.GET("/companies", searchHandler.GetCompanies)

			api.GET("/records", searchHandler.GetRecords)
			api.GET("/records/:id", searchHandler.GetRecordDetail)
			api.DELETE("/records/:id", searchHandler.DeleteRecord)

			// AI分析（已禁用）
			// api.POST("/ai/analyze", searchHandler.AIAnalyze)

			// 公司邮箱
			api.GET("/companies/:id/emails", searchHandler.GetCompanyEmails)
			api.GET("/emails", searchHandler.GetAllEmails)
		}

		if emailHandler != nil {
			api.POST("/email/send", emailHandler.SendEmail)
			api.POST("/email/batch-send", emailHandler.BatchSendEmail)
			api.GET("/email/logs", emailHandler.GetEmailLogs)
			api.GET("/email/cooldown", emailHandler.CheckCooldown)
		}

		if scraperHandler != nil {
			// 爬取相关
			api.POST("/scrape", scraperHandler.ScrapeSingle)
			api.POST("/scrape/batch", scraperHandler.ScrapeBatch)
			api.GET("/scrape/results", scraperHandler.GetScrapeResults)
		}

		if googleSearchHandler != nil {
			// 谷歌搜索+爬取
			api.POST("/google-search", googleSearchHandler.Search)
			api.GET("/google-search/tasks", googleSearchHandler.GetTasks)
			api.GET("/google-search/results", googleSearchHandler.GetTaskResults)
			api.DELETE("/google-search/tasks/:id", googleSearchHandler.DeleteTask)
			api.GET("/google-search/emails", googleSearchHandler.ExportEmails)
		}

		if cronHandler != nil {
			// 定时任务
			api.POST("/cron", cronHandler.CreateTask)
			api.PUT("/cron", cronHandler.UpdateTask)
			api.DELETE("/cron/:id", cronHandler.DeleteTask)
			api.PUT("/cron/:id/toggle", cronHandler.ToggleTask)
			api.POST("/cron/:id/run", cronHandler.RunOnce)
			api.GET("/cron", cronHandler.GetTasks)
		}

		if exportHandler != nil {
			// 导出Excel
			api.GET("/export/record/:id", exportHandler.ExportByRecord)
			api.POST("/export/record/:id/send-email", exportHandler.SendEmailsByRecord)
			api.GET("/export/all", exportHandler.ExportAll)
		}

		// 城市数据（不需要数据库）
		api.GET("/continents", cityHandler.GetContinents)
		api.GET("/cities", cityHandler.GetCitiesByCountry)
	}

	port := cfg.Server.Port
	fmt.Printf("🌍 Google Map 商家搜索系统启动: http://localhost:%s\n", port)
	fmt.Printf("📊 后台管理页面: http://localhost:%s/admin\n", port)
	fmt.Printf("⏰ 定时任务调度器已启动\n")
	r.Run(":" + port)
}
