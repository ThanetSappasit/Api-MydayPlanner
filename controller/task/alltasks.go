package task

import (
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func AllTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/alltask", middleware.AccessTokenMiddleware())
	{
		routes.POST("/task", func(c *gin.Context) {
			CreateAllTask(c, db, firestoreClient)
		})
	}

}

func CreateAllTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var task dto.CreateAllTaskRequest
	if err := c.ShouldBindJSON(&task); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input", "details": err.Error()})
		return
	}

	var user model.User
	if err := db.Where("user_id = ?", userId).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}

	// Helper function สำหรับแปลง string เป็น pointer
	stringPtr := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}

	intPtr := func(i int) *int {
		return &i
	}

	newTask := model.Tasks{
		BoardID:     nil, // ตั้งค่าเป็น nil เพื่อรองรับ board null
		TaskName:    task.TaskName,
		Description: stringPtr(task.Description), // แปลงเป็น pointer
		Status:      task.Status,
		Priority:    stringPtr(task.Priority), // แปลงเป็น pointer
		CreateBy:    intPtr(user.UserID),      // แปลงเป็น pointer
		CreateAt:    time.Now(),
	}

	if err := tx.Create(&newTask).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create task"})
		return
	}

	// Handle reminders
	if task.Reminder != nil {
		// รองรับรูปแบบวันที่ที่คุณส่งมา: 2025-06-20 09:53:09.638825
		parsedDueDate, err := time.Parse("2006-01-02 15:04:05.999999", task.Reminder.DueDate)
		if err != nil {
			// ถ้า parse ไม่ได้ ลองรูปแบบอื่น
			parsedDueDate, err = time.Parse("2006-01-02 15:04:05", task.Reminder.DueDate)
			if err != nil {
				// ถ้ายังไม่ได้ ลอง RFC3339
				parsedDueDate, err = time.Parse(time.RFC3339, task.Reminder.DueDate)
				if err != nil {
					tx.Rollback()
					c.JSON(http.StatusBadRequest, gin.H{
						"error": "Invalid DueDate format. Supported formats: '2006-01-02 15:04:05.999999', '2006-01-02 15:04:05', or RFC3339",
					})
					return
				}
			}
		}

		// คำนวณ DueDate ตาม DayOfWeek
		finalDueDate := parsedDueDate
		if dayOfWeek, err := strconv.Atoi(task.DayOfWeek); err == nil {
			// DayOfWeek 1-7 แปลงเป็น 0-6 วันที่จะเพิ่ม
			if dayOfWeek >= 1 && dayOfWeek <= 7 {
				daysToAdd := dayOfWeek - 1
				finalDueDate = parsedDueDate.AddDate(0, 0, daysToAdd)
			}
		}

		notification := model.Notification{
			TaskID:           newTask.TaskID,
			DueDate:          finalDueDate,
			RecurringPattern: task.Reminder.RecurringPattern,
			IsSend:           false,
			CreatedAt:        time.Now(),
		}

		// บันทึก notification ลงฐานข้อมูล
		if err := tx.Create(&notification).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create notification"})
			return
		}
	}

	// Commit the transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Task created successfully",
		"taskID":  newTask.TaskID,
	})
}
