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

	// 连接数据库
	db, err := gorm.Open(mysql.Open(cfg.Database.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}

	// 自动迁移
	db.AutoMigrate(&models.SearchRecord{}, &models.Company{}, &models.CompanyEmail{}, &models.EmailLog{}, &models.CronTask{})

	// 启动定时任务调度器
	scheduler := services.NewSchedulerService(db)
	scheduler.Start()

	// 预检查 Rod 浏览器（提前下载 Chromium 并验证依赖）
	rodService := services.NewRodMapsService()
	if err := rodService.EnsureBrowser(); err != nil {
		log.Printf("⚠️ Rod 浏览器预检查失败: %v", err)
		log.Println("⚠️ Rod 爬取模式可能无法使用，请安装 Chromium 依赖库")
	}

	// 初始化 handlers
	searchHandler := handlers.NewSearchHandler(db)
	emailHandler := handlers.NewEmailHandler(db)
	scraperHandler := handlers.NewScraperHandler(db)
	googleSearchHandler := handlers.NewGoogleSearchHandler(db)
	cronHandler := handlers.NewCronHandler(db, scheduler)
	exportHandler := handlers.NewExportHandler(db)
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
		api.POST("/search", searchHandler.Search)
		api.POST("/search/next", searchHandler.SearchNextPage)
		api.POST("/search/stop", searchHandler.StopSearch)
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

		api.POST("/email/send", emailHandler.SendEmail)
		api.POST("/email/batch-send", emailHandler.BatchSendEmail)
		api.GET("/email/logs", emailHandler.GetEmailLogs)
		api.GET("/email/cooldown", emailHandler.CheckCooldown)

		// 爬取相关
		api.POST("/scrape", scraperHandler.ScrapeSingle)
		api.POST("/scrape/batch", scraperHandler.ScrapeBatch)
		api.GET("/scrape/results", scraperHandler.GetScrapeResults)

		// 谷歌搜索+爬取
		api.POST("/google-search", googleSearchHandler.Search)
		api.GET("/google-search/tasks", googleSearchHandler.GetTasks)
		api.GET("/google-search/results", googleSearchHandler.GetTaskResults)
		api.DELETE("/google-search/tasks/:id", googleSearchHandler.DeleteTask)
		api.GET("/google-search/emails", googleSearchHandler.ExportEmails)

		// 定时任务
		api.POST("/cron", cronHandler.CreateTask)
		api.PUT("/cron", cronHandler.UpdateTask)
		api.DELETE("/cron/:id", cronHandler.DeleteTask)
		api.PUT("/cron/:id/toggle", cronHandler.ToggleTask)
		api.POST("/cron/:id/run", cronHandler.RunOnce)
		api.GET("/cron", cronHandler.GetTasks)

		// 导出Excel
		api.GET("/export/record/:id", exportHandler.ExportByRecord)
		api.GET("/export/all", exportHandler.ExportAll)

		// 城市数据
		api.GET("/continents", cityHandler.GetContinents)
		api.GET("/cities", cityHandler.GetCitiesByCountry)
	}

	port := cfg.Server.Port
	fmt.Printf("🌍 Google Map 商家搜索系统启动: http://localhost:%s\n", port)
	fmt.Printf("📊 后台管理页面: http://localhost:%s/admin\n", port)
	fmt.Printf("⏰ 定时任务调度器已启动\n")
	r.Run(":" + port)
}
