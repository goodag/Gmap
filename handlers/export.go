package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"googleMap/models"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

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

	query := h.db.Model(&models.Company{})
	if keyword != "" {
		query = query.Where("name LIKE ? OR formatted_address LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if source != "" {
		query = query.Where("source = ?", source)
	}

	var companies []models.Company
	query.Order("id DESC").Find(&companies)

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

	// 表头
	headers := []string{"序号", "公司名称", "地址", "电话", "邮箱", "网站", "评分", "评价数", "公司简介", "AI评分", "来源"}
	colWidths := []float64{8, 30, 40, 20, 35, 30, 8, 10, 50, 10, 10}

	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
		f.SetColWidth(sheet, string(rune('A'+i)), string(rune('A'+i)), colWidths[i])
	}

	// 列宽（超过J列的特殊处理）
	f.SetColWidth(sheet, "A", "A", colWidths[0])
	f.SetColWidth(sheet, "B", "B", colWidths[1])
	f.SetColWidth(sheet, "C", "C", colWidths[2])
	f.SetColWidth(sheet, "D", "D", colWidths[3])
	f.SetColWidth(sheet, "E", "E", colWidths[4])
	f.SetColWidth(sheet, "F", "F", colWidths[5])
	f.SetColWidth(sheet, "G", "G", colWidths[6])
	f.SetColWidth(sheet, "H", "H", colWidths[7])
	f.SetColWidth(sheet, "I", "I", colWidths[8])
	f.SetColWidth(sheet, "J", "J", colWidths[9])
	f.SetColWidth(sheet, "K", "K", colWidths[10])

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

		// 来源
		source := co.Source
		switch source {
		case "google_api":
			source = "Google API"
		case "rod":
			source = "Rod爬取"
		case "google_search":
			source = "谷歌搜索"
		}

		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), i+1)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), co.Name)
		f.SetCellValue(sheet, fmt.Sprintf("C%d", row), co.FormattedAddress)
		f.SetCellValue(sheet, fmt.Sprintf("D%d", row), co.Phone)
		f.SetCellValue(sheet, fmt.Sprintf("E%d", row), emails)

		// 网站设为超链接
		if co.Website != "" {
			f.SetCellValue(sheet, fmt.Sprintf("F%d", row), co.Website)
			f.SetCellHyperLink(sheet, fmt.Sprintf("F%d", row), co.Website, "External")
			f.SetCellStyle(sheet, fmt.Sprintf("F%d", row), fmt.Sprintf("F%d", row), linkStyle)
		}

		f.SetCellValue(sheet, fmt.Sprintf("G%d", row), co.Rating)
		f.SetCellValue(sheet, fmt.Sprintf("H%d", row), co.UserRatingsTotal)
		f.SetCellValue(sheet, fmt.Sprintf("I%d", row), co.CompanyIntro)
		f.SetCellValue(sheet, fmt.Sprintf("J%d", row), co.AIScore)
		f.SetCellValue(sheet, fmt.Sprintf("K%d", row), source)

		// 设置数据样式
		for col := 1; col <= 11; col++ {
			cell, _ := excelize.CoordinatesToCellName(col, row)
			if col != 6 || co.Website == "" { // 网站列单独设了链接样式
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
	lastCell, _ := excelize.CoordinatesToCellName(11, len(companies)+1)
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
