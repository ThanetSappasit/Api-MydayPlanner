package task

import (
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
}

// ฟังก์ชั่นสำหรับเปลี่ยน status ของ task เป็น complete (2)
func CompleteTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	taskID := c.Param("taskid")

	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userID).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	var currentTask model.Tasks
	if err := db.Where("task_id = ?", taskID).First(&currentTask).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task data"})
		return
	}

	// ตรวจสอบ status และกำหนดการเปลี่ยนแปลง
	var newStatus string
	var message string

	if currentTask.Status == "2" {
		// ถ้า task เสร็จแล้ว (2) ให้เปลี่ยนเป็น todo (0)
		newStatus = "0"
		message = "Task reopened successfully"
	} else {
		// ถ้า task ยังไม่เสร็จ (0 หรือ 1) ให้เปลี่ยนเป็น complete (2)
		newStatus = "2"
		message = "Task completed successfully"
	}

	// อัปเดท status
	if err := db.Model(&currentTask).Update("status", newStatus).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task status"})
		return
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

	// รับ status ใหม่จาก request body

	var req dto.StatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// ตรวจสอบว่า status ที่ส่งมาถูกต้องหรือไม่
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
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task data"})
		return
	}

	// ตรวจสอบว่า status เปลี่ยนแปลงจริงหรือไม่
	if currentTask.Status == req.Status {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Task is already in this status"})
		return
	}

	// อัปเดท status
	if err := db.Model(&currentTask).Update("status", req.Status).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task status"})
		return
	}

	// กำหนด message ตาม status
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
