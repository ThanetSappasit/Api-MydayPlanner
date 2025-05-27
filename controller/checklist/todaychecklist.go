package checklist

import (
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"gorm.io/gorm"
)

func TodayChecklistController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/todaychecklist", middleware.AccessTokenMiddleware())
	{
		routes.POST("/create", func(c *gin.Context) {
			CreateTodayChecklistFirebase(c, db, firestoreClient)
		})
	}
}

func CreateTodayChecklistFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.CreateChecklistTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var user model.User
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	// ตรวจสอบว่า task มีอยู่จริงหรือไม่
	taskDoc, err := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Doc(req.TaskID).Get(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}
	if !taskDoc.Exists() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task does not exist"})
		return
	}

	// ดึงข้อมูล checklistID ทั้งหมดที่มีอยู่
	checklistIter := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Doc(req.TaskID).Collection("Checklists").Documents(c)
	defer checklistIter.Stop()

	existingChecklistIDs := make(map[int]bool)
	maxChecklistID := 0

	for {
		doc, err := checklistIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch existing checklists"})
			return
		}

		// แปลง checklistID จาก string เป็น int
		if checklistIDStr, ok := doc.Data()["ChecklistID"].(string); ok {
			if checklistIDInt, parseErr := strconv.Atoi(checklistIDStr); parseErr == nil {
				existingChecklistIDs[checklistIDInt] = true
				if checklistIDInt > maxChecklistID {
					maxChecklistID = checklistIDInt
				}
			}
		}
	}

	// หา checklistID ที่ว่างที่เล็กที่สุด
	var newChecklistID int
	found := false

	// ตรวจสอบจาก 1 ไปจนถึง maxChecklistID เพื่อหาช่องว่าง
	for i := 1; i <= maxChecklistID; i++ {
		if !existingChecklistIDs[i] {
			newChecklistID = i
			found = true
			break
		}
	}

	// หากไม่มีช่องว่าง ให้ใช้เลขถัดไป
	if !found {
		newChecklistID = maxChecklistID + 1
	}

	checklistID := fmt.Sprintf("%d", newChecklistID)

	checklistData := map[string]interface{}{
		"ChecklistID":   checklistID,
		"ChecklistName": req.Checklistname,
		"CreatedAt":     time.Now(),
		"Archived":      false,
	}

	// สร้าง checklist ใหม่
	_, err = firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Doc(req.TaskID).Collection("Checklists").Doc(checklistID).Set(c, checklistData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create checklist in Firestore: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "Checklist created successfully",
		"checklistID": checklistID,
	})
}
