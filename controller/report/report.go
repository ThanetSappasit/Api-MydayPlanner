package report

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ReportController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/report")
	{
		routes.GET("/allreport", middleware.AccessTokenMiddleware(), middleware.AdminMiddleware(), func(c *gin.Context) {
			ReadAllReport(c, db, firestoreClient)
		})
		routes.GET("/category/:categoryid", middleware.AccessTokenMiddleware(), middleware.AdminMiddleware(), func(c *gin.Context) {
			ReadCategoryReport(c, db, firestoreClient)
		})
		routes.POST("/send", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
			ReportSending(c, db, firestoreClient)
		})
		routes.DELETE("/delete/:rid", middleware.AccessTokenMiddleware(), middleware.AdminMiddleware(), func(c *gin.Context) {
			DeleteReport(c, db, firestoreClient)
		})
	}
}

// แยกฟังก์ชันสำหรับดึงชื่อประเภทรายงานเพื่อความชัดเจนและลดความซับซ้อน
func getCategoryName(categoryID int) (string, bool) {
	categories := map[int]string{
		1: "Suggestions",
		2: "Incorrect Information",
		3: "Problems or Issues",
		4: "Accessibility Issues",
		5: "Notification Issues",
		6: "Security Issues",
	}

	category, exists := categories[categoryID]
	return category, exists
}

func GenarateColor(category string) string {
	// สร้างสีตามประเภทของรายงาน
	switch category {
	case "Suggestions":
		return "#007AFF"
	case "Incorrect Information":
		return "#FF3B30"
	case "Problems or Issues":
		return "#34C759"
	case "Accessibility Issues":
		return "#FF9500"
	case "Notification Issues":
		return "#AF52DE"
	case "Security Issues":
		return "#FF2D55"
	default:
		return "#FFFFFF" // Default color (white)
	}
}

func ReportSending(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var reportdata dto.ReportdataRequest

	// ใช้ ShouldBindJSON เพราะไม่ต้องการอ่าน body หลายครั้ง
	if err := c.ShouldBindJSON(&reportdata); err != nil {
		c.JSON(400, gin.H{"error": "Invalid input", "details": err.Error()})
		return
	}

	// ตรวจสอบความถูกต้องของ CategoryID ก่อนที่จะทำการค้นหาผู้ใช้
	category, valid := getCategoryName(reportdata.CategoryID)
	if !valid {
		c.JSON(400, gin.H{"error": "Invalid report ID"})
		return
	}

	// สร้าง report โดยตรงโดยไม่ต้องสร้างตัวแปร report ชั่วคราว
	report := model.Report{
		UserID:      int(userId),
		Description: reportdata.Description,
		Category:    category,
		CreateAt:    time.Now(),
	}

	// ใช้ transaction เพื่อให้การทำงานกับฐานข้อมูลมีประสิทธิภาพยิ่งขึ้น
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Error; err != nil {
		c.JSON(500, gin.H{"error": "Failed to start transaction", "details": err.Error()})
		return
	}

	// ตรวจสอบว่าผู้ใช้มีอยู่จริงโดยใช้ SELECT ที่มีเฉพาะข้อมูลที่จำเป็น
	var user model.User
	if err := tx.Select("user_id", "email").Where("user_id = ?", userId).First(&user).Error; err != nil {
		tx.Rollback()
		c.JSON(500, gin.H{"error": "User not found", "details": err.Error()})
		return
	}

	// บันทึก report
	if err := tx.Create(&report).Error; err != nil {
		tx.Rollback()
		c.JSON(500, gin.H{"error": "Failed to save report", "details": err.Error()})
		return
	}

	reportdatafirebase := map[string]interface{}{
		"ReportID":    report.ReportID,
		"Category":    category,
		"Color":       GenarateColor(category),
		"CreateAt":    report.CreateAt,
		"Description": report.Description,
	}

	ctx := context.Background()
	subCollection := firestoreClient.Collection("Reports").Doc(user.Email).Collection(category)
	docRef := subCollection.Doc(fmt.Sprintf("report_%d", report.ReportID))

	if _, err := docRef.Set(ctx, reportdatafirebase); err != nil {
		tx.Rollback()
		c.JSON(500, gin.H{"error": "Failed to save report to Firestore", "details": err.Error()})
		return
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(500, gin.H{"error": "Failed to commit transaction", "details": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "Report sent successfully!"})
}

func DeleteReport(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	reportId := c.Param("rid")
	var report model.Report

	// ตรวจสอบว่าผู้ใช้มีสิทธิ์ในการลบรายงาน
	if err := db.Where("report_id = ?", reportId).First(&report).Error; err != nil {
		c.JSON(404, gin.H{"error": "Report not found"})
		return
	}

	// ลบรายงานจากฐานข้อมูล
	if err := db.Delete(&report).Error; err != nil {
		c.JSON(500, gin.H{"error": "Failed to delete report", "details": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "Report deleted successfully!"})
}

func ReadAllReport(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var reports []model.Report

	// ใช้ Preload เพื่อดึงข้อมูลผู้ใช้ที่เกี่ยวข้องในคำสั่งเดียว
	if err := db.Preload("User").Find(&reports).Error; err != nil {
		c.JSON(500, gin.H{"error": "Database error", "details": err.Error()})
		return
	}

	// แปลงข้อมูลรายงานให้เป็นรูปแบบที่ต้องการ
	var reportList []gin.H
	for _, report := range reports {
		reportList = append(reportList, gin.H{
			"ReportID":    report.ReportID,
			"Category":    report.Category,
			"Color":       GenarateColor(report.Category),
			"CreateAt":    report.CreateAt,
			"Name":        report.User.Name,
			"Email":       report.User.Email,
			"Description": report.Description,
		})
	}

	c.JSON(200, gin.H{"reports": reportList})
}

func ReadCategoryReport(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	category := c.Param("categoryid")
	categoryID, err := strconv.Atoi(category)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid category ID"})
		return
	}
	category, valid := getCategoryName(categoryID)
	if !valid {
		c.JSON(400, gin.H{"error": "Invalid category"})
		return
	}

	var reports []model.Report

	// ใช้ Preload เพื่อดึงข้อมูลผู้ใช้ที่เกี่ยวข้องในคำสั่งเดียว
	if err := db.Preload("User").Where("category = ?", category).Find(&reports).Error; err != nil {
		c.JSON(500, gin.H{"error": "Database error", "details": err.Error()})
		return
	}

	// แปลงข้อมูลรายงานให้เป็นรูปแบบที่ต้องการ
	var reportList []gin.H
	for _, report := range reports {
		reportList = append(reportList, gin.H{
			"ReportID":    report.ReportID,
			"Category":    report.Category,
			"Color":       GenarateColor(report.Category),
			"CreateAt":    report.CreateAt,
			"Name":        report.User.Name,
			"Email":       report.User.Email,
			"Description": report.Description,
		})
	}

	c.JSON(200, gin.H{"reports": reportList})
}
