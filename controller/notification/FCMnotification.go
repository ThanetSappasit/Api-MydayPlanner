package notification

import (
	"fmt"
	"mydayplanner/model"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RemindNotificationTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/remindtask", func(c *gin.Context) {
		RemindNotificationTask(c, db, firestoreClient)
	})
}

func RemindNotificationTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// ดึงเวลาปัจจุบัน
	now := time.Now()

	var notificationList []gin.H

	// Case 1: ดึง notifications ที่ถึงเวลา before_due และยังไม่ได้ส่ง (is_send = '0')
	var beforeDueNotifications []model.Notification
	err := db.Preload("Task").Where(
		"is_send = ? AND beforedue_date IS NOT NULL AND beforedue_date <= ?",
		"0", now,
	).Find(&beforeDueNotifications).Error

	if err != nil {
		fmt.Printf("Error querying before due notifications: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to query before due notifications",
		})
		return
	}

	// เพิ่ม before_due notifications ลงใน response
	for _, notification := range beforeDueNotifications {
		notificationData := gin.H{
			"notification_id":   notification.NotificationID,
			"task_id":           notification.TaskID,
			"task_name":         notification.Task.TaskName,
			"due_date":          notification.DueDate.Format("2006-01-02 15:04:05"),
			"before_due_date":   notification.BeforeDueDate.Format("2006-01-02 15:04:05"),
			"recurring_pattern": notification.RecurringPattern,
			"notification_type": "before_due",
			"time_status":       "Before due time reached",
			"current_status":    "Ready to send before_due notification",
			"action_needed":     "Send before_due notification and update is_send to '1'",
		}
		notificationList = append(notificationList, notificationData)
	}

	// Case 2: ดึง notifications ที่ถึงเวลา due_date
	// แบ่งเป็น 2 กรณี:
	// - กรณีที่ไม่มี before_due_date (is_send = '0')
	// - กรณีที่มี before_due_date และส่ง before_due แล้ว (is_send = '1')
	var dueNotifications []model.Notification
	err = db.Preload("Task").Where(
		"due_date <= ? AND ((beforedue_date IS NULL AND is_send = ?) OR (beforedue_date IS NOT NULL AND is_send = ?))",
		now, "0", "1",
	).Find(&dueNotifications).Error

	if err != nil {
		fmt.Printf("Error querying due notifications: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to query due notifications",
		})
		return
	}

	// เพิ่ม due notifications ลงใน response
	for _, notification := range dueNotifications {
		notificationData := gin.H{
			"notification_id":   notification.NotificationID,
			"task_id":           notification.TaskID,
			"task_name":         notification.Task.TaskName,
			"due_date":          notification.DueDate.Format("2006-01-02 15:04:05"),
			"recurring_pattern": notification.RecurringPattern,
			"notification_type": "due",
			"time_status":       "Due time reached",
			"current_status":    "Ready to send due notification",
			"action_needed":     "Send due notification and update is_send to '2'",
		}

		// เพิ่ม before_due_date ถ้ามี
		if notification.BeforeDueDate != nil {
			notificationData["before_due_date"] = notification.BeforeDueDate.Format("2006-01-02 15:04:05")
			notificationData["current_status"] = "Before due sent, ready to send due notification"
		} else {
			notificationData["current_status"] = "Ready to send due notification (no before_due)"
		}

		notificationList = append(notificationList, notificationData)
	}

	// สรุปผลลัพธ์
	totalFound := len(beforeDueNotifications) + len(dueNotifications)

	response := gin.H{
		"message":      "Notifications ready to send",
		"current_time": now.Format("2006-01-02 15:04:05"),
		"summary": gin.H{
			"total_found":              totalFound,
			"before_due_notifications": len(beforeDueNotifications),
			"due_notifications":        len(dueNotifications),
		},
		"notifications": notificationList,
		"next_step":     "Review the data and call send endpoint to proceed",
		"instructions": gin.H{
			"before_due": "Send notification and update is_send from '0' to '1'",
			"due":        "Send notification and update is_send to '2' (from '0' or '1')",
		},
	}

	c.JSON(http.StatusOK, response)
}
