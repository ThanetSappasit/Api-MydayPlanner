package notification

import (
	"context"
	"fmt"
	"log"
	"mydayplanner/model"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"gorm.io/gorm"
)

// RecurringPattern constants
const (
	PatternOneTime = "onetime"
	PatternDaily   = "daily"
	PatternWeekly  = "weekly"
	PatternMonthly = "monthly"
	PatternYearly  = "yearly"
)

// RepeatNotificationResult สำหรับ return ผลลัพธ์
type RepeatNotificationResult struct {
	Message      string `json:"message"`
	CurrentTime  string `json:"current_time"`
	TotalCount   int    `json:"total_count"`
	SuccessCount int    `json:"success_count"`
	ErrorCount   int    `json:"error_count"`
}

func ProcessRepeatNotifications(db *gorm.DB, firestoreClient *firestore.Client) (*RepeatNotificationResult, error) {
	now := time.Now().UTC()

	// ค้นหา notifications ที่ is_send = "2" และเป็น recurring pattern (ไม่รวม onetime)
	var completedNotifications []model.Notification

	query := db.Preload("Task").Where(
		"is_send = ? AND recurring_pattern != ? AND recurring_pattern != ? AND recurring_pattern IS NOT NULL",
		"2", PatternOneTime, "",
	)

	if err := query.Find(&completedNotifications).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch completed recurring notifications: %v", err)
	}

	if len(completedNotifications) == 0 {
		return &RepeatNotificationResult{
			Message:      "No recurring notifications to process",
			CurrentTime:  now.Format(time.RFC3339),
			TotalCount:   0,
			SuccessCount: 0,
			ErrorCount:   0,
		}, nil
	}

	log.Printf("📋 Found %d completed recurring notifications to process", len(completedNotifications))

	// ประมวลผลแบบ concurrent
	const maxWorkers = 10
	semaphore := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var resultMu sync.Mutex

	successCount := 0
	errorCount := 0

	for _, notification := range completedNotifications {
		wg.Add(1)
		go func(noti model.Notification) {
			defer wg.Done()
			semaphore <- struct{}{}        // acquire semaphore
			defer func() { <-semaphore }() // release semaphore

			if processRecurringNotification(db, firestoreClient, noti, now) {
				resultMu.Lock()
				successCount++
				resultMu.Unlock()
			} else {
				resultMu.Lock()
				errorCount++
				resultMu.Unlock()
			}
		}(notification)
	}

	wg.Wait()

	return &RepeatNotificationResult{
		Message:      "Recurring notifications processed successfully",
		CurrentTime:  now.Format(time.RFC3339),
		TotalCount:   len(completedNotifications),
		SuccessCount: successCount,
		ErrorCount:   errorCount,
	}, nil
}

// processRecurringNotification ประมวลผล notification แต่ละตัว
func processRecurringNotification(db *gorm.DB, firestoreClient *firestore.Client, notification model.Notification, now time.Time) bool {
	log.Printf("🔄 Processing recurring notification ID: %d (Pattern: %s)",
		notification.NotificationID, notification.RecurringPattern)

	// คำนวณวันที่ถัดไป
	nextDueDate, nextBeforeDueDate, err := calculateNextDueDates(
		notification.DueDate,
		notification.BeforeDueDate,
		notification.RecurringPattern,
	)
	if err != nil {
		log.Printf("❌ Failed to calculate next dates for notification %d: %v",
			notification.NotificationID, err)
		return false
	}

	log.Printf("📅 Next due date: %v, Next before due: %v",
		nextDueDate, nextBeforeDueDate)

	// เริ่ม transaction
	tx := db.Begin()
	if tx.Error != nil {
		log.Printf("❌ Failed to begin transaction for notification %d: %v",
			notification.NotificationID, tx.Error)
		return false
	}

	// อัปเดต notification ในฐานข้อมูล
	updateData := map[string]interface{}{
		"due_date": nextDueDate,
		"is_send":  "0", // รีเซ็ต status กลับไปเป็น 0
	}

	if nextBeforeDueDate != nil {
		updateData["beforedue_date"] = *nextBeforeDueDate
	} else {
		updateData["beforedue_date"] = nil
	}

	if err := tx.Model(&notification).Updates(updateData).Error; err != nil {
		tx.Rollback()
		log.Printf("❌ Failed to update notification %d in database: %v",
			notification.NotificationID, err)
		return false
	}

	// อัปเดต Firestore
	if err := updateFirestoreForRecurring(firestoreClient, notification, nextDueDate, nextBeforeDueDate, tx); err != nil {
		tx.Rollback()
		log.Printf("❌ Failed to update Firestore for notification %d: %v",
			notification.NotificationID, err)
		return false
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		log.Printf("❌ Failed to commit transaction for notification %d: %v",
			notification.NotificationID, err)
		return false
	}

	log.Printf("✅ Successfully processed recurring notification %d", notification.NotificationID)
	return true
}

// calculateNextDueDates คำนวณวันที่ถัดไปตาม pattern
func calculateNextDueDates(currentDueDate *time.Time, beforeDueDate *time.Time, pattern string) (nextDueDate time.Time, nextBeforeDueDate *time.Time, err error) {
	switch pattern {
	case PatternDaily:
		nextDueDate = currentDueDate.AddDate(0, 0, 1)
	case PatternWeekly:
		nextDueDate = currentDueDate.AddDate(0, 0, 7)
	case PatternMonthly:
		nextDueDate = currentDueDate.AddDate(0, 1, 0)
	case PatternYearly:
		nextDueDate = currentDueDate.AddDate(1, 0, 0)
	default:
		return time.Time{}, nil, fmt.Errorf("unsupported recurring pattern: %s", pattern)
	}

	// คำนวณ beforeDueDate ถ้ามี
	if beforeDueDate != nil {
		// คำนวณระยะห่างระหว่าง beforeDueDate และ dueDate
		duration := currentDueDate.Sub(*beforeDueDate)
		newBeforeDueDate := nextDueDate.Add(-duration)
		nextBeforeDueDate = &newBeforeDueDate
	}

	return nextDueDate, nextBeforeDueDate, nil
}

// updateFirestoreForRecurring อัปเดต Firestore สำหรับ recurring tasks
func updateFirestoreForRecurring(client *firestore.Client, notification model.Notification, nextDueDate time.Time, nextBeforeDueDate *time.Time, db *gorm.DB) error {
	ctx := context.Background()

	// ตรวจสอบว่าเป็น group task หรือไม่
	isGroup, err := isGroupTask(db, notification.Task)
	if err != nil {
		return fmt.Errorf("failed to check if task is group: %v", err)
	}

	var docPath string
	updateData := map[string]interface{}{
		"dueDate":      nextDueDate,
		"isShow":       false,
		"isNotiRemind": false,
		"notiCount":    false,
		"isSend":       "0", // รีเซ็ต status
		"updatedAt":    time.Now().UTC(),
	}

	// เพิ่ม remindMeBefore ถ้ามี
	if nextBeforeDueDate != nil {
		updateData["remindMeBefore"] = *nextBeforeDueDate
	} else {
		updateData["remindMeBefore"] = nil
	}

	if isGroup {
		docPath = fmt.Sprintf("BoardTasks/%d/Notifications/%d", notification.TaskID, notification.NotificationID)

		// อัปเดต user notifications สำหรับ group tasks
		var boardUsers []model.BoardUser
		if notification.Task.BoardID != nil {
			if err := db.Where("board_id = ?", *notification.Task.BoardID).Find(&boardUsers).Error; err != nil {
				return fmt.Errorf("failed to find board users: %v", err)
			}

			userNotifications := make(map[string]interface{})
			for _, boardUser := range boardUsers {
				userIDStr := fmt.Sprintf("%d", boardUser.UserID)
				userNotifications[userIDStr] = map[string]interface{}{
					"isShow":           false,
					"isNotiRemindShow": false,
				}
			}
			updateData["userNotifications"] = userNotifications
		}
	} else {
		// Individual task
		email, err := getTaskOwnerEmail(db, notification.TaskID)
		if err != nil {
			return fmt.Errorf("failed to get task owner email: %v", err)
		}
		docPath = fmt.Sprintf("Notifications/%s/Tasks/%d", email, notification.NotificationID)
	}

	// อัปเดต Firestore
	_, err = client.Doc(docPath).Set(ctx, updateData, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("failed to update Firestore document at %s: %v", docPath, err)
	}

	log.Printf("✅ Successfully updated Firestore for recurring notification at path: %s", docPath)
	return nil
}

// isGroupTask ตรวจสอบว่าเป็น group task หรือไม่
func isGroupTask(db *gorm.DB, task model.Tasks) (bool, error) {
	if task.BoardID == nil {
		return false, nil
	}

	var count int64
	err := db.Model(&model.BoardUser{}).Where("board_id = ?", *task.BoardID).Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// ValidateRecurringPattern ตรวจสอบความถูกต้องของ pattern
func ValidateRecurringPattern(pattern string) bool {
	validPatterns := []string{PatternOneTime, PatternDaily, PatternWeekly, PatternMonthly, PatternYearly}
	for _, validPattern := range validPatterns {
		if pattern == validPattern {
			return true
		}
	}
	return false
}
