package checklist

import (
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func FinishChecklistController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.PUT("/checklistfinish/:checklistid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		CompleteChecklist(c, db, firestoreClient)
	})
}

// ฟังก์ชั่นสำหรับเปลี่ยน status ของ task เป็น complete (2)
func CompleteChecklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	checklistID := c.Param("checklistid")

	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userID).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	var currentChecklist model.Checklist
	if err := db.Where("checklist_id = ?", checklistID).First(&currentChecklist).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Checklist not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get Checklist data"})
		return
	}

	// ตรวจสอบ status และกำหนดการเปลี่ยนแปลง
	var newStatus string
	var message string

	if currentChecklist.Status == "1" {
		// ถ้า task เสร็จแล้ว (2) ให้เปลี่ยนเป็น todo (0)
		newStatus = "0"
		message = "Checklist reopened successfully"
	} else {
		// ถ้า task ยังไม่เสร็จ (0 หรือ 1) ให้เปลี่ยนเป็น complete (2)
		newStatus = "1"
		message = "Checklist completed successfully"
	}

	// อัปเดท status
	if err := db.Model(&currentChecklist).Update("status", newStatus).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update checklist status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     message,
		"checklistID": checklistID,
	})
}
