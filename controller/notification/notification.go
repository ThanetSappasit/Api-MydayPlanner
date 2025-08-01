package notification

import (
	"context"
	"errors"
	"fmt"
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

func NotificationTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/notification", middleware.AccessTokenMiddleware())
	{
		// routes.POST("/add", func(c *gin.Context) {
		// 	CreateNotification(c, db, firestoreClient)
		// })
		routes.PUT("/update/:taskid", func(c *gin.Context) {
			UpdateNotificationDynamic(c, db, firestoreClient)
		})

	}
}

func UpdateNotificationDynamic(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	taskID := c.Param("taskid")

	// Convert taskID to integer for validation
	taskIDInt, err := strconv.Atoi(taskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	var req dto.UpdateNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Validate user exists
	var user model.User
	if err := db.Where("user_id = ?", userId).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
		}
		return
	}

	// Find the notification to update
	var notification model.Notification
	if err := db.Where("task_id = ?", taskIDInt).First(&notification).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Notification not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch notification"})
		}
		return
	}

	// ตรวจสอบ task และ board เพื่อกำหนด shouldSaveToFirestore
	var task struct {
		TaskID   int  `gorm:"column:task_id"`
		BoardID  *int `gorm:"column:board_id"`
		CreateBy *int `gorm:"column:create_by"`
	}

	if err := db.Table("tasks").
		Select("task_id, board_id, create_by").
		Where("task_id = ?", taskIDInt).
		First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch task"})
		}
		return
	}

	// ตรวจสอบสิทธิ์การเข้าถึงและกำหนด shouldSaveToFirestore
	shouldSaveToFirestore := false
	boardmember := false

	if task.BoardID == nil {
		// Today task - ตรวจสอบว่าเป็นเจ้าของ task
		if task.CreateBy == nil || uint(*task.CreateBy) != userId {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not the owner of this personal task"})
			return
		}
		shouldSaveToFirestore = false
		boardmember = false
	} else {
		// Board task - ตรวจสอบการเข้าถึงบอร์ด
		var boardUser model.BoardUser
		if err := db.Where("board_id = ? AND user_id = ?", task.BoardID, userId).First(&boardUser).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// ไม่ใช่ board member, ตรวจสอบว่าเป็น board owner หรือไม่
				var board model.Board
				if err := db.Where("board_id = ? AND create_by = ?", task.BoardID, userId).First(&board).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not a board member or board owner"})
					} else {
						c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board ownership"})
					}
					return
				}
				shouldSaveToFirestore = true // Board owner
				boardmember = false
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch board user"})
				return
			}
		} else {
			shouldSaveToFirestore = true // Board member
			boardmember = true
		}
	}

	// Create update map for dynamic updates
	updates := make(map[string]interface{})

	// Parse and update due date if provided
	if req.DueDate != nil {
		parsedDate, err := time.Parse(time.RFC3339, *req.DueDate)
		if err != nil {
			// Try alternative format if RFC3339 fails
			parsedDate, err = time.Parse("2006-01-02T15:04:05Z07:00", *req.DueDate)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid due date format. Use RFC3339 format"})
				return
			}
		}
		updates["due_date"] = parsedDate
		notification.DueDate = parsedDate
	}

	if req.BeforeDueDate != nil {
		parsedBeforeDate, err := time.Parse(time.RFC3339, *req.BeforeDueDate)
		if err != nil {
			parsedBeforeDate, err = time.Parse("2006-01-02T15:04:05Z07:00", *req.BeforeDueDate)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid before due date format. Use RFC3339 format"})
				return
			}
		}
		updates["beforedue_date"] = parsedBeforeDate
		notification.BeforeDueDate = &parsedBeforeDate
	} else {
		// If BeforeDueDate is not provided, set it to nil
		updates["beforedue_date"] = nil
		notification.BeforeDueDate = nil
	}

	// Update recurring pattern if provided
	if req.RecurringPattern != nil {
		updates["recurring_pattern"] = *req.RecurringPattern
		notification.RecurringPattern = *req.RecurringPattern
	}

	// Update is_send if provided
	if req.IsSend != nil {
		updates["is_send"] = *req.IsSend
		// notification.IsSend = *req.IsSend
	}

	// If no updates provided, return error
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	// Update in database
	if err := db.Model(&notification).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update notification"})
		return
	}

	// Update in Firebase with only the modified fields
	if err := updateFirebaseNotification(firestoreClient, user, notification, updates, shouldSaveToFirestore, boardmember); err != nil {
		// Log the error but don't fail the request since DB update succeeded
		fmt.Printf("Warning: Failed to update Firebase notification: %v\n", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Notification updated successfully",
		"notification": map[string]interface{}{
			"notification_id":   notification.NotificationID,
			"task_id":           notification.TaskID,
			"due_date":          notification.DueDate,
			"recurring_pattern": notification.RecurringPattern,
			"is_send":           notification.IsSend,
			"created_at":        notification.CreatedAt,
		},
	})
}

func updateFirebaseNotification(firestoreClient *firestore.Client, user model.User, notification model.Notification, updatedFields map[string]interface{}, shouldSaveToFirestore bool, boardmember bool) error {
	ctx := context.Background()

	// สร้าง path ตาม shouldSaveToFirestore และ boardmember
	var docPath string
	if shouldSaveToFirestore {
		if boardmember {
			// สำหรับ board tasks ที่ user เป็น board member
			docPath = fmt.Sprintf("BoardTasks/%d/Notifications/%d", notification.TaskID, notification.NotificationID)
		} else {
			// สำหรับ board tasks ที่ user เป็น board owner
			docPath = fmt.Sprintf("Notifications/%s/Tasks/%d", user.Email, notification.NotificationID)
		}
	} else {
		// สำหรับ today tasks (personal tasks)
		docPath = fmt.Sprintf("Notifications/%s/Tasks/%d", user.Email, notification.NotificationID)
	}

	// Create Firebase document data only for updated fields
	firebaseData := make(map[string]interface{})

	// Map SQL field names to Firebase field names and add only updated fields
	for sqlField, value := range updatedFields {
		switch sqlField {
		case "due_date":
			firebaseData["dueDate"] = value
		case "beforedue_date":
			firebaseData["beforeDueDate"] = value
		case "recurring_pattern":
			firebaseData["recurringPattern"] = value
		case "is_send":
			firebaseData["isSend"] = value
		}
	}

	// Always update the updatedAt timestamp when any field is updated
	firebaseData["updatedAt"] = time.Now()

	// Convert firebaseData map to []firestore.Update
	var updates []firestore.Update
	for k, v := range firebaseData {
		updates = append(updates, firestore.Update{Path: k, Value: v})
	}

	// Update only the modified fields in Firebase
	_, err := firestoreClient.Doc(docPath).Update(ctx, updates)
	if err != nil {
		return fmt.Errorf("failed to update Firebase document: %v", err)
	}

	return nil
}

// func CreateNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
// 	userId := c.MustGet("userId").(uint)

// 	var req dto.NotificationRequest
// 	if err := c.ShouldBindJSON(&req); err != nil {
// 		c.JSON(400, gin.H{"error": "Invalid input"})
// 		return
// 	}

// 	var user model.User
// 	if err := db.Where("user_id = ?", userId).First(&user).Error; err != nil {
// 		if errors.Is(err, gorm.ErrRecordNotFound) {
// 			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
// 		} else {
// 			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
// 		}
// 		return
// 	}

// 	var task struct {
// 		TaskID   int  `db:"task_id"`
// 		BoardID  *int `db:"board_id"`
// 		CreateBy *int `db:"create_by"`
// 	}

// 	if err := db.Table("tasks").
// 		Select("task_id, board_id, create_by").
// 		Where("task_id = ?", req.TaskID).
// 		First(&task).Error; err != nil {
// 		if err == gorm.ErrRecordNotFound {
// 			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
// 		} else {
// 			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch task"})
// 		}
// 		return
// 	}

// 	// ตรวจสอบสิทธิ์การเข้าถึงและกำหนด shouldSaveToFirestore
// 	shouldSaveToFirestore := false
// 	if task.BoardID == nil {
// 		// Today task - ตรวจสอบว่าเป็นเจ้าของ task
// 		if task.CreateBy == nil || uint(*task.CreateBy) != userId {
// 			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not the owner of this personal task"})
// 			return
// 		}
// 		shouldSaveToFirestore = false
// 	} else {
// 		// Board task - ตรวจสอบการเข้าถึงบอร์ด
// 		var boardUser model.BoardUser
// 		if err := db.Where("board_id = ? AND user_id = ?", task.BoardID, userId).First(&boardUser).Error; err != nil {
// 			if errors.Is(err, gorm.ErrRecordNotFound) {
// 				// ไม่ใช่ board member, ตรวจสอบว่าเป็น board owner หรือไม่
// 				var board model.Board
// 				if err := db.Where("board_id = ? AND create_by = ?", task.BoardID, userId).First(&board).Error; err != nil {
// 					if errors.Is(err, gorm.ErrRecordNotFound) {
// 						c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not a board member or board owner"})
// 					} else {
// 						c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board ownership"})
// 					}
// 					return
// 				}
// 				shouldSaveToFirestore = false // Board owner
// 			} else {
// 				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch board user"})
// 				return
// 			}
// 		} else {
// 			shouldSaveToFirestore = true // Board member
// 		}
// 	}

// 	parsedDueDate, err := parseDateTime(req.DueDate)
// 	if err != nil {
// 		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid DueDate format: %v", err)})
// 		return
// 	}

// 	var existingNotification model.Notification
// 	err = db.Where("task_id = ?", req.TaskID).First(&existingNotification).Error

// 	isUpdate := false
// 	if err == nil {
// 		isUpdate = true
// 	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing notification"})
// 		return
// 	}

// 	tx := db.Begin()
// 	if tx.Error != nil {
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
// 		return
// 	}
// 	defer func() {
// 		if r := recover(); r != nil {
// 			tx.Rollback()
// 		}
// 	}()

// 	var notificationToSave model.Notification

// 	if isUpdate {
// 		existingNotification.DueDate = parsedDueDate
// 		existingNotification.RecurringPattern = req.RecurringPattern
// 		existingNotification.IsSend = parsedDueDate.Before(time.Now())

// 		if err := tx.Save(&existingNotification).Error; err != nil {
// 			tx.Rollback()
// 			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update notification"})
// 			return
// 		}
// 		notificationToSave = existingNotification
// 	} else {
// 		newNotification := model.Notification{
// 			TaskID:           req.TaskID,
// 			DueDate:          parsedDueDate,
// 			RecurringPattern: req.RecurringPattern,
// 			IsSend:           parsedDueDate.Before(time.Now()),
// 			CreatedAt:        time.Now(),
// 		}

// 		if err := tx.Create(&newNotification).Error; err != nil {
// 			tx.Rollback()
// 			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create notification"})
// 			return
// 		}
// 		notificationToSave = newNotification
// 	}

// 	if err := tx.Commit().Error; err != nil {
// 		tx.Rollback()
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
// 		return
// 	}

// 	// บันทึกใน Firestore โดยใช้ shouldSaveToFirestore
// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	if err := saveNotificationToFirestore(ctx, firestoreClient, &notificationToSave, user.Email, shouldSaveToFirestore); err != nil {
// 		fmt.Printf("Warning: Failed to save Notification to Firestore: %v\n", err)
// 	}

// 	statusCode := http.StatusOK
// 	message := "Notification updated successfully"
// 	if !isUpdate {
// 		statusCode = http.StatusCreated
// 		message = "Notification created successfully"
// 	}

// 	c.JSON(statusCode, gin.H{
// 		"message":          message,
// 		"notificationID":   notificationToSave.NotificationID,
// 		"taskID":           notificationToSave.TaskID,
// 		"dueDate":          notificationToSave.DueDate,
// 		"recurringPattern": notificationToSave.RecurringPattern,
// 		"isSend":           notificationToSave.IsSend,
// 		"createdAt":        notificationToSave.CreatedAt,
// 	})
// }

// func parseDateTime(dateStr string) (time.Time, error) {
// 	// ตรวจสอบ empty string ก่อน
// 	if dateStr == "" {
// 		return time.Time{}, fmt.Errorf("due date is required")
// 	}

// 	// ตัด whitespace ออก
// 	dateStr = strings.TrimSpace(dateStr)
// 	if dateStr == "" {
// 		return time.Time{}, fmt.Errorf("due date cannot be empty")
// 	}

// 	formats := []string{
// 		"2006-01-02 15:04:05.999999",
// 		"2006-01-02 15:04:05",
// 		time.RFC3339,
// 		"2006-01-02T15:04:05.999999Z07:00",
// 		"2006-01-02T15:04:05Z07:00",
// 	}

// 	for _, format := range formats {
// 		if t, err := time.Parse(format, dateStr); err == nil {
// 			return t, nil
// 		}
// 	}

// 	return time.Time{}, fmt.Errorf("unsupported date format: %s", dateStr)
// }

// // บันทึกการแจ้งเตือนใน Firestore โดยใช้ logic เดียวกันกับ task service
// func saveNotificationToFirestore(ctx context.Context, client *firestore.Client, notification *model.Notification, email string, shouldSaveToFirestore bool) error {
// 	var notificationPath string

// 	// แยก path ตาม shouldSaveToFirestore
// 	if shouldSaveToFirestore {
// 		// สำหรับ board tasks ที่ user เป็น board member
// 		notificationPath = fmt.Sprintf("BoardTasks/%d/Notifications/%d", notification.TaskID, notification.NotificationID)
// 	} else {
// 		// สำหรับ today tasks หรือ board tasks ที่ user เป็น board owner
// 		notificationPath = fmt.Sprintf("Notifications/%s/Tasks/%d", email, notification.NotificationID)
// 	}

// 	notificationData := map[string]interface{}{
// 		"notificationID": notification.NotificationID,
// 		"taskID":         notification.TaskID,
// 		"dueDate":        notification.DueDate,
// 		"isSend":         notification.IsSend,
// 		"createdAt":      notification.CreatedAt,
// 		"updatedAt":      time.Now(),
// 	}

// 	// Add recurring pattern if exists
// 	if notification.RecurringPattern != "" {
// 		notificationData["recurringPattern"] = notification.RecurringPattern
// 	}

// 	_, err := client.Doc(notificationPath).Set(ctx, notificationData)
// 	return err
// }
