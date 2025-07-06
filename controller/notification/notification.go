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
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func NotificationTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/notification", middleware.AccessTokenMiddleware())
	{
		routes.POST("/add", func(c *gin.Context) {
			CreateNotification(c, db, firestoreClient)
		})
		routes.PUT("/update/:taskid", func(c *gin.Context) {
			UpdateNotificationDynamic(c, db, firestoreClient)
		})
	}
}

func CreateNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	var req dto.NotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid input"})
		return
	}

	var user model.User
	if err := db.Where("user_id = ?", userId).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
		}
		return
	}

	var task struct {
		TaskID   int  `db:"task_id"`
		BoardID  *int `db:"board_id"`
		CreateBy *int `db:"create_by"`
	}

	if err := db.Table("tasks").
		Select("task_id, board_id, create_by").
		Where("task_id = ?", req.TaskID).
		First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch task"})
		}
		return
	}

	// ตรวจสอบสิทธิ์การเข้าถึง
	if task.BoardID == nil {
		if task.CreateBy == nil || uint(*task.CreateBy) != userId {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not the owner of this personal task"})
			return
		}
	} else {
		var boardUser model.BoardUser
		if err := db.Where("board_id = ? AND user_id = ?", task.BoardID, userId).First(&boardUser).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				var board model.Board
				if err := db.Where("board_id = ? AND create_by = ?", task.BoardID, userId).First(&board).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not a board member or board owner"})
					} else {
						c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board ownership"})
					}
					return
				}
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch board user"})
				return
			}
		}
	}

	parsedDueDate, err := parseDateTime(req.DueDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid DueDate format: %v", err)})
		return
	}

	var existingNotification model.Notification
	err = db.Where("task_id = ?", req.TaskID).First(&existingNotification).Error

	isUpdate := false
	if err == nil {
		isUpdate = true
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing notification"})
		return
	}

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

	var notificationToSave model.Notification

	if isUpdate {
		existingNotification.DueDate = parsedDueDate
		existingNotification.RecurringPattern = req.RecurringPattern
		existingNotification.IsSend = parsedDueDate.Before(time.Now())

		if err := tx.Save(&existingNotification).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update notification"})
			return
		}
		notificationToSave = existingNotification
	} else {
		newNotification := model.Notification{
			TaskID:           req.TaskID,
			DueDate:          parsedDueDate,
			RecurringPattern: req.RecurringPattern,
			IsSend:           parsedDueDate.Before(time.Now()),
			CreatedAt:        time.Now(),
		}

		if err := tx.Create(&newNotification).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create notification"})
			return
		}
		notificationToSave = newNotification
	}

	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	notificationIDInt := int(notificationToSave.NotificationID)
	if err := saveTaskToFirestore(ctx, firestoreClient, &notificationToSave, notificationIDInt, user.Email); err != nil {
		fmt.Printf("Warning: Failed to save Notification to Firestore: %v\n", err)
	}

	statusCode := http.StatusOK
	message := "Notification updated successfully"
	if !isUpdate {
		statusCode = http.StatusCreated
		message = "Notification created successfully"
	}

	c.JSON(statusCode, gin.H{
		"message":          message,
		"notificationID":   notificationToSave.NotificationID,
		"taskID":           notificationToSave.TaskID,
		"dueDate":          notificationToSave.DueDate,
		"recurringPattern": notificationToSave.RecurringPattern,
		"isSend":           notificationToSave.IsSend,
		"createdAt":        notificationToSave.CreatedAt,
	})
}

func parseDateTime(dateStr string) (time.Time, error) {
	// ตรวจสอบ empty string ก่อน
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("due date is required")
	}

	// ตัด whitespace ออก
	dateStr = strings.TrimSpace(dateStr)
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("due date cannot be empty")
	}

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

func saveTaskToFirestore(ctx context.Context, client *firestore.Client, notification *model.Notification, notificationIDInt int, email string) error {
	taskPath := fmt.Sprintf("Notifications/%s/Tasks/%d", email, notificationIDInt)

	taskData := map[string]interface{}{
		"notificationID":   notificationIDInt,
		"taskID":           notification.TaskID,
		"dueDate":          notification.DueDate,
		"recurringPattern": notification.RecurringPattern,
		"isSend":           notification.IsSend,
		"createdAt":        notification.CreatedAt,
	}

	_, err := client.Doc(taskPath).Set(ctx, taskData)
	return err
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

	// Update recurring pattern if provided
	if req.RecurringPattern != nil {
		updates["recurring_pattern"] = *req.RecurringPattern
		notification.RecurringPattern = *req.RecurringPattern
	}

	// Update is_send if provided
	if req.IsSend != nil {
		updates["is_send"] = *req.IsSend
		notification.IsSend = *req.IsSend
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
	if err := updateFirebaseNotification(firestoreClient, user, notification, updates); err != nil {
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

func updateFirebaseNotification(firestoreClient *firestore.Client, user model.User, notification model.Notification, updatedFields map[string]interface{}) error {
	ctx := context.Background()

	// Construct the Firebase path: /Notifications/{email}/Tasks/{notificationID}
	docPath := fmt.Sprintf("Notifications/%s/Tasks/%d", user.Email, notification.NotificationID)

	// Create Firebase document data only for updated fields
	firebaseData := make(map[string]interface{})

	// Map SQL field names to Firebase field names and add only updated fields
	for sqlField, value := range updatedFields {
		switch sqlField {
		case "due_date":
			firebaseData["dueDate"] = value
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
