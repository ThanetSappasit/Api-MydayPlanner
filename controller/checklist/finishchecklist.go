package checklist

import (
	"mydayplanner/model"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func FinishChecklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
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
			c.JSON(http.StatusNotFound, gin.H{"error": "checklist not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get checklist data"})
		return
	}

	var currentTask model.Tasks
	if err := db.Where("task_id = ?", currentChecklist.TaskID).First(&currentTask).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task data"})
		return
	}

	// แก้ไข path ให้ตรงกับ Firestore structure ที่ถูกต้อง
	// เปลี่ยน "Checklist" เป็น "Checklists"
	docRef := firestoreClient.Collection("Checklists").Doc(checklistID)

	// ดึงข้อมูล checklist ปัจจุบันเพื่อเช็คสถานะ Archived
	doc, err := docRef.Get(c)
	if err != nil {
		// ถ้าไม่พบ document ให้สร้างใหม่พร้อม Archived: true
		if status.Code(err) == codes.NotFound {
			_, err = docRef.Set(c, map[string]interface{}{
				"ChecklistID":   checklistID,
				"TaskID":        currentTask.TaskID,
				"ChecklistName": currentChecklist.ChecklistName,
				"Archived":      true,
				"CreatedAt":     time.Now(),
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create checklist document"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"message": "checklist archived successfully", "checklistID": checklistID, "archived": true})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get checklist"})
		return
	}

	// เช็คสถานะ Archived ปัจจุบัน
	var currentArchived bool
	if archivedValue, exists := doc.Data()["Archived"]; exists {
		if archived, ok := archivedValue.(bool); ok {
			currentArchived = archived
		}
	}

	// สลับค่า Archived
	newArchivedValue := !currentArchived

	// อัพเดทสถานะ Archived
	_, err = docRef.Update(c, []firestore.Update{
		{Path: "Archived", Value: newArchivedValue},
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update checklist archive status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "checklist archived successfully",
		"checklistID": checklistID,
	})
}
