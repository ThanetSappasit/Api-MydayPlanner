package task

import (
	"mydayplanner/model"
	"net/http"

	"fmt"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func FinishTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	taskID := c.Param("taskid")

	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userID).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	var currentTask model.Tasks
	if err := db.Where("task_id = ? ", taskID).First(&currentTask).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task data"})
		return
	}
	docRef := firestoreClient.Collection("Boards").Doc(email).Collection("Boards").Doc(fmt.Sprintf("%v", currentTask.BoardID)).Collection("Tasks").Doc(taskID)

	// ดึงข้อมูล task ปัจจุบันเพื่อเช็คสถานะ Archived
	doc, err := docRef.Get(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task archive status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task archived successfully", "taskID": taskID})
}
