package task

import (
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func CreateTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/task", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		CreateTask(c, db, firestoreClient)
	})
}

func TodayTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/todaytasks", middleware.AccessTokenMiddleware())
	{
		routes.POST("/create", func(c *gin.Context) {
			CreateTodayTask(c, db, firestoreClient)
		})
	}
}

func CreateTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var task dto.CreateTaskRequest
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
		BoardID:     &task.BoardID, // ใช้ BoardID จาก request
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

	// ประกาศตัวแปร notification นอก if block
	var notification model.Notification
	var hasNotification bool

	// Handle reminders
	if task.Reminder != nil {
		hasNotification = true

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

		notification = model.Notification{
			TaskID:           newTask.TaskID,
			DueDate:          parsedDueDate,
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

	// สร้าง response object
	response := gin.H{
		"message": "Task created successfully",
		"taskID":  newTask.TaskID,
	}

	// เพิ่ม notificationID เฉพาะเมื่อมีการสร้าง notification
	if hasNotification {
		response["notificationID"] = notification.NotificationID
	}

	c.JSON(http.StatusCreated, response)
}

func CreateTodayTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var task dto.CreateTodayTaskRequest
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

	// ประกาศตัวแปร notification นอก if block
	var notification model.Notification
	var hasNotification bool

	// Handle reminders
	if task.Reminder != nil {
		hasNotification = true

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

		notification = model.Notification{
			TaskID:           newTask.TaskID,
			DueDate:          parsedDueDate,
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

	// สร้าง response object
	response := gin.H{
		"message": "Task created successfully",
		"taskID":  newTask.TaskID,
	}

	// เพิ่ม notificationID เฉพาะเมื่อมีการสร้าง notification
	if hasNotification {
		response["notificationID"] = notification.NotificationID // แก้ไขให้ใช้ NotificationID (Pascal case)
	}

	c.JSON(http.StatusCreated, response)
}
