package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"googleMap/models"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

// 从社交链接JSON中提取指定平台的链接
func extractSocialLink(socialLinksJSON string, platform string) string {
	if socialLinksJSON == "" {
		return ""
	}
	var links []string
	if err := json.Unmarshal([]byte(socialLinksJSON), &links); err != nil {
		return ""
	}
	for _, link := range links {
		if strings.Contains(strings.ToLower(link), platform) {
			return link
		}
	}
	return ""
}

// 从文本中提取主营产品（简单截取）
func extractProduct(text string) string {
	if text == "" {
		return ""
	}
	// 截取前100字作为主营产品描述
	if len(text) > 100 {
		return text[:100] + "..."
	}
	return text
}

type ExportHandler struct {
	db *gorm.DB
}

func NewExportHandler(db *gorm.DB) *ExportHandler {
	return &ExportHandler{db: db}
}

// ExportByRecord 按搜索记录导出商家数据为Excel
func (h *ExportHandler) ExportByRecord(c *gin.Context) {
	recordID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "无效ID"})
		return
	}

	// 查搜索记录
	var record models.SearchRecord
	if err := h.db.First(&record, recordID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "记录不存在"})
		return
	}

	// 查关联的公司
	var companies []models.Company
	h.db.Where("search_record_id = ?", recordID).Find(&companies)

	// 查所有公司的邮箱
	companyIDs := make([]uint64, len(companies))
	for i, co := range companies {
		companyIDs[i] = uint64(co.ID)
	}
	var allEmails []models.CompanyEmail
	if len(companyIDs) > 0 {
		h.db.Where("company_id IN ?", companyIDs).Find(&allEmails)
	}
	emailMap := make(map[uint64][]string)
	for _, e := range allEmails {
		emailMap[e.CompanyID] = append(emailMap[e.CompanyID], e.Email)
	}

	f := h.buildExcel(companies, emailMap, fmt.Sprintf("搜索记录 #%d", recordID))

	filename := fmt.Sprintf("商家数据_记录%d_%s.xlsx", recordID, time.Now().Format("20060102_150405"))
	h.sendExcel(c, f, filename)
}

// ExportAll 导出所有商家数据
func (h *ExportHandler) ExportAll(c *gin.Context) {
	keyword := c.Query("keyword")
	source := c.Query("source")

	log.Printf("[Export] 导出全部商家, keyword=%s, source=%s", keyword, source)

	query := h.db.Model(&models.Company{})
	if keyword != "" {
		query = query.Where("name LIKE ? OR formatted_address LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if source != "" {
		query = query.Where("source = ?", source)
	}

	var companies []models.Company
	query.Order("id DESC").Find(&companies)

	log.Printf("[Export] 查询到 %d 条商家数据", len(companies))

	// 查所有邮箱
	companyIDs := make([]uint64, len(companies))
	for i, co := range companies {
		companyIDs[i] = uint64(co.ID)
	}
	var allEmails []models.CompanyEmail
	if len(companyIDs) > 0 {
		h.db.Where("company_id IN ?", companyIDs).Find(&allEmails)
	}
	emailMap := make(map[uint64][]string)
	for _, e := range allEmails {
		emailMap[e.CompanyID] = append(emailMap[e.CompanyID], e.Email)
	}

	f := h.buildExcel(companies, emailMap, "全部商家")

	filename := fmt.Sprintf("商家数据_全部_%s.xlsx", time.Now().Format("20060102_150405"))
	h.sendExcel(c, f, filename)
}

func (h *ExportHandler) buildExcel(companies []models.Company, emailMap map[uint64][]string, sheetTitle string) *excelize.File {
	f := excelize.NewFile()
	sheet := "商家数据"
	index, _ := f.NewSheet(sheet)
	f.DeleteSheet("Sheet1")
	f.SetActiveSheet(index)

	// 表头样式
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 12, Color: "#FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#1a73e8"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
		Border: []excelize.Border{
			{Type: "left", Color: "#D4D4D4", Style: 1},
			{Type: "right", Color: "#D4D4D4", Style: 1},
			{Type: "top", Color: "#D4D4D4", Style: 1},
			{Type: "bottom", Color: "#D4D4D4", Style: 1},
		},
	})

	// 数据样式
	dataStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
		Border: []excelize.Border{
			{Type: "left", Color: "#E8E8E8", Style: 1},
			{Type: "right", Color: "#E8E8E8", Style: 1},
			{Type: "top", Color: "#E8E8E8", Style: 1},
			{Type: "bottom", Color: "#E8E8E8", Style: 1},
		},
	})

	// 链接样式
	linkStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Color: "#1a73e8", Underline: "single"},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
		Border: []excelize.Border{
			{Type: "left", Color: "#E8E8E8", Style: 1},
			{Type: "right", Color: "#E8E8E8", Style: 1},
			{Type: "top", Color: "#E8E8E8", Style: 1},
			{Type: "bottom", Color: "#E8E8E8", Style: 1},
		},
	})

	// 表头（按用户需求顺序）
	headers := []string{"序号", "公司名称", "地址", "电话", "官网", "邮箱", "评分", "Facebook", "Instagram", "行业", "来源", "业务简介", "主营产品"}
	colWidths := []float64{8, 30, 40, 20, 40, 35, 8, 40, 40, 30, 15, 50, 50}

	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, header)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
		f.SetColWidth(sheet, string(rune('A'+i)), string(rune('A'+i)), colWidths[i])
	}

	// 数据行
	for i, co := range companies {
		row := i + 2

		// 合并邮箱
		emails := ""
		if em, ok := emailMap[co.ID]; ok && len(em) > 0 {
			emails = joinStrings(em, "\n")
		} else if co.Email != "" {
			emails = co.Email
		}

		// 来源映射
		source := co.Source
		switch source {
		case "google_api", "map":
			source = "google_map"
		case "rod":
			source = "google_map"
		case "google_search":
			source = "google_search"
		}

		// 提取社交链接
		facebook := extractSocialLink(co.SocialLinks, "facebook")
		instagram := extractSocialLink(co.SocialLinks, "instagram")

		// 主营产品（从简介或正文提取）
		product := extractProduct(co.CompanyIntro)
		if product == "" {
			product = extractProduct(co.BodyText)
		}

		// 设置单元格值
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), i+1)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), co.Name)
		f.SetCellValue(sheet, fmt.Sprintf("C%d", row), co.FormattedAddress)
		f.SetCellValue(sheet, fmt.Sprintf("D%d", row), co.Phone)
		
		// 官网设为超链接
		if co.Website != "" {
			f.SetCellValue(sheet, fmt.Sprintf("E%d", row), co.Website)
			f.SetCellHyperLink(sheet, fmt.Sprintf("E%d", row), co.Website, "External")
			f.SetCellStyle(sheet, fmt.Sprintf("E%d", row), fmt.Sprintf("E%d", row), linkStyle)
		}
		
		f.SetCellValue(sheet, fmt.Sprintf("F%d", row), emails)
		f.SetCellValue(sheet, fmt.Sprintf("G%d", row), co.Rating)
		
		// Facebook设为超链接
		if facebook != "" {
			f.SetCellValue(sheet, fmt.Sprintf("H%d", row), facebook)
			f.SetCellHyperLink(sheet, fmt.Sprintf("H%d", row), facebook, "External")
			f.SetCellStyle(sheet, fmt.Sprintf("H%d", row), fmt.Sprintf("H%d", row), linkStyle)
		}
		
		// Instagram设为超链接
		if instagram != "" {
			f.SetCellValue(sheet, fmt.Sprintf("I%d", row), instagram)
			f.SetCellHyperLink(sheet, fmt.Sprintf("I%d", row), instagram, "External")
			f.SetCellStyle(sheet, fmt.Sprintf("I%d", row), fmt.Sprintf("I%d", row), linkStyle)
		}
		
		f.SetCellValue(sheet, fmt.Sprintf("J%d", row), co.Types)
		f.SetCellValue(sheet, fmt.Sprintf("K%d", row), source)
		f.SetCellValue(sheet, fmt.Sprintf("L%d", row), co.CompanyIntro)
		f.SetCellValue(sheet, fmt.Sprintf("M%d", row), product)

		// 设置数据样式
		for col := 1; col <= 13; col++ {
			cell, _ := excelize.CoordinatesToCellName(col, row)
			// 网站、Facebook、Instagram列单独设了链接样式
			if col != 5 && col != 8 && col != 9 {
				f.SetCellStyle(sheet, cell, cell, dataStyle)
			}
		}
	}

	// 冻结首行
	f.SetPanes(sheet, &excelize.Panes{
		Freeze:      true,
		Split:       false,
		XSplit:      0,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
	})

	// 自动筛选
	lastCell, _ := excelize.CoordinatesToCellName(13, len(companies)+1)
	f.AutoFilter(sheet, "A1:"+lastCell, nil)

	log.Printf("[Export] 导出 %s: %d 条商家数据", sheetTitle, len(companies))
	return f
}

func (h *ExportHandler) sendExcel(c *gin.Context, f *excelize.File, filename string) {
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Cache-Control", "no-cache")

	if err := f.Write(c.Writer); err != nil {
		log.Printf("[Export] 写入Excel失败: %v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	f.Close()
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
