package task

import (
	"context"
	"errors"
	"fmt"
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

	// ตรวจสอบ user และ board user พร้อมกัน
	var user model.User
	if err := db.Where("user_id = ?", userId).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
		}
		return
	}

	var shouldSaveToFirestore = false
	var boardUser model.BoardUser
	if err := db.Where("board_id = ?", task.BoardID).First(&boardUser).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// ตรวจสอบว่า user เป็นเจ้าของบอร์ดหรือไม่
			var board model.Board
			if err := db.Where("board_id = ? AND create_by = ?", task.BoardID, userId).First(&board).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					c.JSON(http.StatusNotFound, gin.H{"error": "Access denied: not a board member or board owner"})
				} else {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board ownership"})
				}
				return
			}
			// ถ้าเจอบอร์ดที่ userId เป็นคนสร้าง ไม่ต้องบันทึก Firebase
			shouldSaveToFirestore = false
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch board user"})
			return
		}
	} else {
		// เจอ boardUser แล้ว ให้บันทึก Firebase ด้วย
		shouldSaveToFirestore = true
	}

	// เริ่ม transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// สร้าง task
	newTask := model.Tasks{
		BoardID:     &task.BoardID,
		TaskName:    task.TaskName,
		Description: stringToPtr(task.Description),
		Status:      task.Status,
		Priority:    stringToPtr(task.Priority),
		CreateBy:    intToPtr(user.UserID),
		CreateAt:    time.Now(),
	}

	if err := tx.Create(&newTask).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create task"})
		return
	}

	// จัดการ notification
	var notification model.Notification
	var hasNotification bool

	if task.Reminder != nil {
		hasNotification = true

		parsedDueDate, err := parseDateTime(task.Reminder.DueDate)
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Invalid DueDate format. Supported formats: '2006-01-02 15:04:05.999999', '2006-01-02 15:04:05', or RFC3339",
			})
			return
		}

		notification = model.Notification{
			TaskID:           newTask.TaskID,
			DueDate:          parsedDueDate,
			RecurringPattern: task.Reminder.RecurringPattern,
			IsSend:           false,
			CreatedAt:        time.Now(),
		}

		if err := tx.Create(&notification).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create notification"})
			return
		}
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	// บันทึกลง Firestore (หลัง database commit สำเร็จ) - เฉพาะเมื่อเจอ boardUser เท่านั้น
	if shouldSaveToFirestore {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		boardIDInt := int(task.BoardID)
		// บันทึก task ลง Firestore
		if err := saveTaskToFirestore(ctx, firestoreClient, &newTask, boardIDInt); err != nil {
			// Log error แต่ไม่ return เพราะ database บันทึกสำเร็จแล้ว
			// ควรมี logging system ที่นี่
			fmt.Printf("Warning: Failed to save task to Firestore: %v\n", err)
		}

		// บันทึก notification ลง Firestore ถ้ามี
		if hasNotification {
			if err := saveNotificationToFirestore(ctx, firestoreClient, &notification, user.Email); err != nil {
				// Log error แต่ไม่ return
				fmt.Printf("Warning: Failed to save notification to Firestore: %v\n", err)
			}
		}
	}

	// สร้าง response
	response := gin.H{
		"message": "Task created successfully",
		"taskID":  newTask.TaskID,
	}

	if hasNotification {
		response["notificationID"] = notification.NotificationID
	}

	c.JSON(http.StatusCreated, response)
}

// Helper functions
func stringToPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intToPtr(i int) *int {
	return &i
}

func parseDateTime(dateStr string) (time.Time, error) {
	// ลองรูปแบบต่างๆ ตามลำดับ
	formats := []string{
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported date format: %s", dateStr)
}

func saveTaskToFirestore(ctx context.Context, client *firestore.Client, task *model.Tasks, boardID int) error {
	taskPath := fmt.Sprintf("Boards/%d/Tasks/%d", boardID, task.TaskID)

	taskData := map[string]interface{}{
		"taskID":    task.TaskID,
		"boardID":   boardID,
		"taskName":  task.TaskName,
		"status":    task.Status,
		"createAt":  task.CreateAt,
		"updatedAt": time.Now(),
	}

	// เพิ่มข้อมูลที่เป็น pointer แบบปลอดภัย
	if task.Description != nil {
		taskData["description"] = *task.Description
	}
	if task.Priority != nil {
		taskData["priority"] = *task.Priority
	}
	if task.CreateBy != nil {
		taskData["createBy"] = *task.CreateBy
	}

	_, err := client.Doc(taskPath).Set(ctx, taskData)
	return err
}

func saveNotificationToFirestore(ctx context.Context, client *firestore.Client, notification *model.Notification, email string) error {
	notificationPath := fmt.Sprintf("Notifications/%s/Tasks/%d", email, notification.NotificationID)

	notificationData := map[string]interface{}{
		"notificationID": notification.NotificationID,
		"taskID":         notification.TaskID,
		"dueDate":        notification.DueDate,
		"isSend":         notification.IsSend,
		"createdAt":      notification.CreatedAt,
		"updatedAt":      time.Now(),
	}

	// เพิ่ม recurring pattern ถ้ามี
	if notification.RecurringPattern != "" {
		notificationData["recurringPattern"] = notification.RecurringPattern
	}

	_, err := client.Doc(notificationPath).Set(ctx, notificationData)
	return err
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
