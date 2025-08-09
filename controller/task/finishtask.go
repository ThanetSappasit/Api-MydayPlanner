package task

import (
	"context"
	"fmt"
	"log"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func FinishTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.PUT("/taskfinish/:taskid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		CompleteTask(c, db, firestoreClient)
	})
	router.PUT("/updatestatus/:taskid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		UpdateTaskStatus(c, db, firestoreClient)
	})
	router.PUT("/markasdoneTask/:taskid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		MarkAsdoneTaskStatus(c, db, firestoreClient)
	})
}

// ฟังก์ชั่นสำหรับเปลี่ยน status ของ task เป็น complete (2)
func CompleteTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	taskID := c.Param("taskid")

	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userID).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	var currentTask model.Tasks
	if err := db.Where("task_id = ?", taskID).First(&currentTask).Error; err != nil {
		status := http.StatusInternalServerError
		if err == gorm.ErrRecordNotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "Task not found"})
		return
	}

	var boardgroup model.BoardUser
	boardgroupExists := db.Where("board_id = ?", currentTask.BoardID).First(&boardgroup).Error == nil

	var notification model.Notification
	if err := db.Where("task_id = ?", currentTask.TaskID).First(&notification).Error; err != nil {
		status := http.StatusInternalServerError
		if err == gorm.ErrRecordNotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "Notification not found"})
		return
	}

	// สลับค่า status
	var newStatus string
	var message string

	if currentTask.Status == "2" {
		newStatus = "0"
		message = "Task reopened successfully"
	} else {
		newStatus = "2"
		message = "Task completed successfully"
	}

	// อัปเดต status ใน SQL
	if err := db.Model(&currentTask).Update("status", newStatus).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task status"})
		return
	}

	// อัปเดต is_send ใน SQL
	if err := db.Model(&notification).Update("is_send", true).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update is_send in SQL"})
		return
	}

	ctx := context.Background()

	if boardgroupExists {
		// Firestore: /Boards/{boardID}/Tasks/{taskID} - update status
		boardTaskRef := firestoreClient.
			Collection("Boards").
			Doc(fmt.Sprint(*currentTask.BoardID)).
			Collection("Tasks").
			Doc(fmt.Sprint(currentTask.TaskID))

		_, err := boardTaskRef.Update(ctx, []firestore.Update{
			{Path: "status", Value: newStatus},
		})
		if err != nil {
			log.Printf("Failed to update status in Firestore (Boards/Tasks): %v", err)
		}

		// Firestore: /BoardTasks/{boardID}/Notifications/{notificationID} - update isSend
		notiRef := firestoreClient.
			Collection("BoardTasks").
			Doc(fmt.Sprint(currentTask.TaskID)).
			Collection("Notifications").
			Doc(fmt.Sprint(notification.NotificationID))

		_, err = notiRef.Update(ctx, []firestore.Update{
			{Path: "isSend", Value: true},
		})
		if err != nil {
			log.Printf("Failed to update isSend in Firestore (BoardTasks/Notifications): %v", err)
		}
	} else {
		// Firestore: /Notifications/{email}/Tasks/{taskID} - update isSend
		notiRef := firestoreClient.
			Collection("Notifications").
			Doc(email).
			Collection("Tasks").
			Doc(fmt.Sprint(notification.NotificationID))

		_, err := notiRef.Update(ctx, []firestore.Update{
			{Path: "isSend", Value: true},
		})
		if err != nil {
			log.Printf("Failed to update isSend in Firestore (Notifications/Tasks): %v", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": message,
		"taskID":  taskID,
	})
}

// ฟังก์ชั่นสำหรับเปลี่ยน status แบบทั่วไป (ถ้าต้องการความยืดหยุ่น)
func UpdateTaskStatus(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	taskID := c.Param("taskid")

	var req dto.StatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if req.Status != "0" && req.Status != "1" && req.Status != "2" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status. Must be 0, 1, or 2"})
		return
	}

	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userID).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	var currentTask model.Tasks
	if err := db.Where("task_id = ?", taskID).First(&currentTask).Error; err != nil {
		status := http.StatusInternalServerError
		if err == gorm.ErrRecordNotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "Task not found"})
		return
	}

	if currentTask.Status == req.Status {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Task is already in this status"})
		return
	}

	// อัปเดต status ใน SQL
	if err := db.Model(&currentTask).Update("status", req.Status).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task status"})
		return
	}

	ctx := context.Background()

	// === 🔍 ตรวจสอบว่า board นี้เป็น group หรือไม่ ===
	var boardgroup model.BoardUser
	isGroupBoard := false
	if currentTask.BoardID != nil {
		err := db.Where("board_id = ?", *currentTask.BoardID).First(&boardgroup).Error
		if err == nil {
			isGroupBoard = true
		}
	}

	// === 🔥 Firestore: update status เสมอ ===
	if currentTask.BoardID != nil {
		boardTaskRef := firestoreClient.
			Collection("Boards").
			Doc(fmt.Sprint(*currentTask.BoardID)).
			Collection("Tasks").
			Doc(fmt.Sprint(currentTask.TaskID))

		_, err := boardTaskRef.Update(ctx, []firestore.Update{
			{Path: "status", Value: req.Status},
		})
		if err != nil {
			log.Printf("Failed to update Firestore (Boards/Tasks): %v", err)
		}
	}

	// === ถ้า status = 2 ต้องอัปเดต isSend เพิ่ม ===
	if req.Status == "2" {
		// SQL: update is_send
		var notification model.Notification
		if err := db.Where("task_id = ?", currentTask.TaskID).First(&notification).Error; err != nil {
			log.Printf("Failed to fetch notification: %v", err)
		} else {
			// อัปเดต SQL
			if err := db.Model(&notification).Update("is_send", true).Error; err != nil {
				log.Printf("Failed to update is_send in SQL: %v", err)
			}

			if isGroupBoard && currentTask.BoardID != nil {
				// Firestore: /BoardTasks/{boardID}/Notifications/{notificationID}
				notiRef := firestoreClient.
					Collection("BoardTasks").
					Doc(fmt.Sprint(currentTask.TaskID)).
					Collection("Notifications").
					Doc(fmt.Sprint(notification.NotificationID))

				_, err := notiRef.Update(ctx, []firestore.Update{
					{Path: "isSend", Value: true},
				})
				if err != nil {
					log.Printf("Failed to update isSend in Firestore (BoardTasks/Notifications): %v", err)
				}
			} else {
				// Firestore: /Notifications/{email}/Tasks/{taskID}
				notiRef := firestoreClient.
					Collection("Notifications").
					Doc(email).
					Collection("Tasks").
					Doc(fmt.Sprint(currentTask.TaskID))

				_, err := notiRef.Update(ctx, []firestore.Update{
					{Path: "isSend", Value: true},
				})
				if err != nil {
					log.Printf("Failed to update isSend in Firestore (Notifications/Tasks): %v", err)
				}
			}
		}
	}

	// === ส่งข้อความตามสถานะ ===
	var message string
	switch req.Status {
	case "0":
		message = "Task moved to todo"
	case "1":
		message = "Task started"
	case "2":
		message = "Task completed"
	}

	c.JSON(http.StatusOK, gin.H{
		"message": message,
		"taskID":  taskID,
	})
}

func MarkAsdoneTaskStatus(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	taskID := c.Param("taskid")

	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userID).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	var currentTask model.Tasks
	if err := db.Where("task_id = ?", taskID).First(&currentTask).Error; err != nil {
		status := http.StatusInternalServerError
		if err == gorm.ErrRecordNotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "Task not found"})
		return
	}
	var statusTask string
	switch currentTask.Status {
	case "0", "1":
		statusTask = "2"
	default:
		statusTask = currentTask.Status
	}

	var req struct{ Status string }
	req.Status = statusTask

	// อัปเดต status ใน SQL
	if err := db.Model(&currentTask).Update("status", req.Status).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task status"})
		return
	}

	ctx := context.Background()

	// === 🔍 ตรวจสอบว่า board นี้เป็น group หรือไม่ ===
	var boardgroup model.BoardUser
	if currentTask.BoardID != nil {
		err := db.Where("board_id = ?", *currentTask.BoardID).First(&boardgroup).Error
		if err == nil {

		}
	}

	// === 🔥 Firestore: update status เสมอ ===
	if currentTask.BoardID != nil {
		boardTaskRef := firestoreClient.
			Collection("Boards").
			Doc(fmt.Sprint(*currentTask.BoardID)).
			Collection("Tasks").
			Doc(fmt.Sprint(currentTask.TaskID))

		_, err := boardTaskRef.Update(ctx, []firestore.Update{
			{Path: "status", Value: req.Status},
		})
		if err != nil {
			log.Printf("Failed to update Firestore (Boards/Tasks): %v", err)
		}
	}

	// === ส่งข้อความตามสถานะ ===
	var message string
	switch req.Status {
	case "0":
		message = "Task moved to todo"
	case "1":
		message = "Task started"
	case "2":
		message = "Task completed"
	}

	c.JSON(http.StatusOK, gin.H{
		"message": message,
		"taskID":  taskID,
	})
}
