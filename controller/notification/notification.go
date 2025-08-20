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

	// กำหนดค่าเริ่มต้นเมื่อไม่พบ task
	shouldSaveToFirestore := false
	boardmember := false
	taskNotFound := false

	if err := db.Table("tasks").
		Select("task_id, board_id, create_by").
		Where("task_id = ?", taskIDInt).
		First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// ไม่พบ task - สร้าง notification record ใหม่
			taskNotFound = true

			// เพิ่มข้อมูลลง notification table สำหรับ task ที่ไม่พบ
			newNotification := model.Notification{
				TaskID:           taskIDInt,
				RecurringPattern: "onetime", // ตามค่าเริ่มต้นใน model
				IsSend:           "0",       // ตามค่าเริ่มต้นใน model (enum '0','1','2','')
				// CreatedAt จะถูกตั้งค่าอัตโนมัติด้วย autoCreateTime
			}

			// สร้าง notification record ใหม่
			if err := db.Create(&newNotification).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create notification for missing task"})
				return
			}

			// อัปเดต notification variable เป็น record ใหม่
			notification = newNotification

			// กำหนดค่าสำหรับ task ที่ไม่พบ
			shouldSaveToFirestore = false
			boardmember = false
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch task"})
			return
		}
	}

	// ถ้า task พบ ให้ตรวจสอบสิทธิ์การเข้าถึงตามปกติ
	if !taskNotFound {
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
		updates["due_date"] = &parsedDate
		notification.DueDate = &parsedDate
	}

	// Parse and update before due date if provided
	if req.BeforeDueDate != nil {
		parsedBeforeDate, err := time.Parse(time.RFC3339, *req.BeforeDueDate)
		if err != nil {
			parsedBeforeDate, err = time.Parse("2006-01-02T15:04:05Z07:00", *req.BeforeDueDate)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid before due date format. Use RFC3339 format"})
				return
			}
		}
		updates["beforedue_date"] = &parsedBeforeDate
		notification.BeforeDueDate = &parsedBeforeDate
	} else if req.BeforeDueDate != nil && *req.BeforeDueDate == "" {
		// If BeforeDueDate is explicitly set to empty string, set it to nil
		updates["beforedue_date"] = nil
		notification.BeforeDueDate = nil
	}

	// Parse and update snooze if provided (but not always processed)
	if req.Snooze != nil {
		if *req.Snooze == "" {
			// If snooze is explicitly set to empty string, set it to nil
			updates["snooze"] = nil
			notification.Snooze = nil
		} else {
			parsedSnooze, err := time.Parse(time.RFC3339, *req.Snooze)
			if err != nil {
				parsedSnooze, err = time.Parse("2006-01-02T15:04:05Z07:00", *req.Snooze)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid snooze date format. Use RFC3339 format"})
					return
				}
			}
			// Note: Snooze update is conditional and might not always be processed
			// You can add additional logic here to determine when to update snooze
			updates["snooze"] = &parsedSnooze
			notification.Snooze = &parsedSnooze
		}
	}

	// Update recurring pattern if provided
	if req.RecurringPattern != nil {
		updates["recurring_pattern"] = *req.RecurringPattern
		notification.RecurringPattern = *req.RecurringPattern
	}

	// Update is_send if provided (convert to string type as per model)
	if req.IsSend != nil {
		updates["is_send"] = *req.IsSend
		notification.IsSend = *req.IsSend
	}

	// If no updates provided and this is not a newly created notification, return error
	if len(updates) == 0 && !taskNotFound {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	// Update in database (only if there are updates or if it's not a newly created record)
	if len(updates) > 0 {
		if err := db.Model(&notification).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update notification"})
			return
		}
	}

	// Update in Firebase with only the modified fields (only if there are updates)
	if len(updates) > 0 {
		if err := updateFirebaseNotification(firestoreClient, user, notification, updates, shouldSaveToFirestore, boardmember); err != nil {
			// Log the error but don't fail the request since DB update succeeded
			fmt.Printf("Warning: Failed to update Firebase notification: %v\n", err)
		}
	}

	// Prepare response data with proper null handling
	responseData := map[string]interface{}{
		"notification_id":   notification.NotificationID,
		"task_id":           notification.TaskID,
		"recurring_pattern": notification.RecurringPattern,
		"is_send":           notification.IsSend,
		"created_at":        notification.CreatedAt,
	}

	// Add task_found status to response
	responseData["task_found"] = !taskNotFound

	// Handle nullable fields properly
	if notification.DueDate != nil {
		responseData["due_date"] = notification.DueDate
	} else {
		responseData["due_date"] = nil
	}

	if notification.BeforeDueDate != nil {
		responseData["before_due_date"] = notification.BeforeDueDate
	} else {
		responseData["before_due_date"] = nil
	}

	if notification.Snooze != nil {
		responseData["snooze"] = notification.Snooze
	} else {
		responseData["snooze"] = nil
	}

	// Adjust success message based on whether task was found
	var message string
	if taskNotFound {
		message = "Notification created for missing task and updated successfully"
	} else {
		message = "Notification updated successfully"
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      message,
		"notification": responseData,
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
		case "snooze":
			firebaseData["snooze"] = value
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
