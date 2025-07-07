package checklist

import (
	"context"
	"fmt"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func FinishChecklistController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.PUT("/checklistfinish/:checklistid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		CompleteChecklist(c, db, firestoreClient)
	})
}

func CompleteChecklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	checklistIDStr := c.Param("checklistid")

	checklistID, err := strconv.Atoi(checklistIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid checklist ID"})
		return
	}

	var currentChecklist model.Checklist
	if err := db.Where("checklist_id = ?", checklistID).First(&currentChecklist).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Checklist not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get checklist data"})
		return
	}

	// ดึง task เพื่อตรวจสอบสิทธิ์
	var task struct {
		TaskID   int  `db:"task_id"`
		BoardID  *int `db:"board_id"`
		CreateBy *int `db:"create_by"`
	}
	if err := db.Table("tasks").
		Select("task_id, board_id, create_by").
		Where("task_id = ?", currentChecklist.TaskID).
		First(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch task for checklist"})
		return
	}

	// ตรวจสอบว่า user เป็นสมาชิกของ board หรือไม่
	shouldUpdateFirestore := false
	if task.BoardID != nil {
		var boardUser model.BoardUser
		if err := db.Where("board_id = ? AND user_id = ?", task.BoardID, userID).First(&boardUser).Error; err == nil {
			shouldUpdateFirestore = true
		}
	}

	// ตรวจสอบ status และเตรียมเปลี่ยนแปลง
	var newStatus string
	var message string
	if currentChecklist.Status == "1" {
		newStatus = "0"
		message = "Checklist reopened successfully"
	} else {
		newStatus = "1"
		message = "Checklist completed successfully"
	}

	// อัปเดตใน database
	if err := db.Model(&currentChecklist).Update("status", newStatus).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update checklist status"})
		return
	}

	// อัปเดตใน Firestore ถ้าเป็นสมาชิกบอร์ด
	if shouldUpdateFirestore {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := firestoreClient.Collection("BoardTasks").Doc(strconv.Itoa(task.TaskID)).Collection("Checklist").
			Doc(strconv.Itoa(checklistID)).
			Update(ctx, []firestore.Update{
				{Path: "status", Value: newStatus},
			})
		if err != nil {
			fmt.Printf("⚠️ Firestore update failed for checklist %d: %v\n", checklistID, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     message,
		"checklistID": checklistID,
	})
}
