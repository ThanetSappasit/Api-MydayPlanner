package notification

import (
	"context"
	"fmt"
	"log"
	"mydayplanner/model"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"google.golang.org/api/option"
	"gorm.io/gorm"
)

type MulticastRequest struct {
	Tokens   []string          `json:"tokens" binding:"required"`
	Title    string            `json:"title" binding:"required"`
	Body     string            `json:"body" binding:"required"`
	Data     map[string]string `json:"data,omitempty"`
	ImageURL string            `json:"image_url,omitempty"`
}

// TaskInfo เก็บข้อมูลที่ต้องใช้ซ้ำๆ
type TaskInfo struct {
	Task    model.Tasks
	Users   []model.User
	Tokens  []string
	IsGroup bool
	BoardID interface{}
}

// NotificationProcessor จัดการการประมวลผล notification
type NotificationProcessor struct {
	db              *gorm.DB
	firestoreClient *firestore.Client
	app             *firebase.App
	taskCache       map[int]*TaskInfo // cache ข้อมูล task เพื่อไม่ต้อง query ซ้ำ
}

func SendNotificationTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/send_notification", func(c *gin.Context) {
		SendNotification(c, db, firestoreClient)
	})
}

func SendNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Warning: No .env file found or failed to load")
	}

	now := time.Now().UTC()

	// เริ่ม Firebase app
	serviceAccountKeyPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_1")
	if serviceAccountKeyPath == "" {
		c.JSON(500, gin.H{"error": "Firebase credentials not configured"})
		return
	}

	app, err := initializeFirebaseApp(serviceAccountKeyPath)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to initialize Firebase app: " + err.Error()})
		return
	}

	var notifications []model.Notification

	// Query สำหรับ notifications ที่พร้อมส่งแล้ว พร้อม preload Task data
	query := db.Preload("Task").Where(
		"(is_send = '0' AND ((beforedue_date IS NOT NULL AND beforedue_date <= ?) OR (beforedue_date IS NULL AND due_date <= ?))) OR "+
			"(is_send = '1' AND due_date <= ?)",
		now, now, now,
	)

	if err := query.Find(&notifications).Error; err != nil {
		c.JSON(500, gin.H{"error": "Failed to fetch notifications"})
		return
	}

	// สร้าง processor พร้อม cache
	processor := &NotificationProcessor{
		db:              db,
		firestoreClient: firestoreClient,
		app:             app,
		taskCache:       make(map[int]*TaskInfo),
	}

	successCount := 0
	errorCount := 0

	// ประมวลผลแต่ละ notification
	for _, notification := range notifications {
		shouldSend, updateIsSend := processor.shouldSendNotification(notification, now)

		if shouldSend {
			if processor.processNotification(notification, updateIsSend, now, db) {
				successCount++
			} else {
				errorCount++
			}
		}
	}

	c.JSON(200, gin.H{
		"message":       "Notifications processed successfully",
		"current_time":  now.Format(time.RFC3339),
		"total_count":   len(notifications),
		"success_count": successCount,
		"error_count":   errorCount,
	})
}

// shouldSendNotification ตรวจสอบว่าควรส่ง notification หรือไม่
func (p *NotificationProcessor) shouldSendNotification(notification model.Notification, now time.Time) (bool, string) {
	if notification.IsSend == "0" {
		// กรณี is_send = 0: ตรวจสอบ beforedue_date ก่อน
		if notification.BeforeDueDate != nil && (notification.BeforeDueDate.Before(now) || notification.BeforeDueDate.Equal(now)) {
			return true, "1" // เปลี่ยนเป็น 1 หลังส่ง beforedue_date
		} else if notification.BeforeDueDate == nil && (notification.DueDate.Before(now) || notification.DueDate.Equal(now)) {
			return true, "2" // เปลี่ยนเป็น 2 หลังส่ง due_date
		}
	} else if notification.IsSend == "1" {
		// กรณี is_send = 1: ตรวจสอบเฉพาะ due_date
		if notification.DueDate.Before(now) || notification.DueDate.Equal(now) {
			return true, "2" // เปลี่ยนเป็น 2 หลังส่ง due_date
		}
	}
	return false, ""
}

// processNotification ประมวลผล notification แต่ละตัว
func (p *NotificationProcessor) processNotification(notification model.Notification, updateIsSend string, now time.Time, db *gorm.DB) bool {
	fmt.Printf("Sending notification for Task ID: %d\n", notification.TaskID)

	// สร้างข้อความแจ้งเตือน
	message := buildNotificationMessage(notification)
	if message == "" {
		return false
	}

	// ดึงข้อมูล task (ใช้ cache ถ้ามี)
	taskInfo, err := p.getTaskInfo(notification.TaskID)
	if err != nil {
		log.Printf("Failed to get task info for Task ID %d: %v", notification.TaskID, err)
		return false
	}

	if len(taskInfo.Tokens) == 0 {
		log.Printf("No tokens found for user of Task ID %d", notification.TaskID)
		return false
	}

	timestamp, err := p.getTimeInfo(updateIsSend, notification)
	if err != nil {
		log.Printf("Failed to get time info for Task ID %d: %v", notification.TaskID, err)
		return false
	}

	// สร้าง data payload
	data := map[string]string{
		"taskid":    fmt.Sprintf("%d", notification.TaskID),
		"timestamp": timestamp,
		"boardid":   fmt.Sprintf("%v", taskInfo.BoardID),
	}

	// ส่งการแจ้งเตือน
	err = sendMulticastNotification(p.app, taskInfo.Tokens, "แจ้งเตือนงาน", message, data)
	if err != nil {
		log.Printf("Failed to send notification for Task ID %d: %v", notification.TaskID, err)
		return false
	}

	// อัพเดท is_send status ใน database
	if err := p.db.Model(&notification).Update("is_send", updateIsSend).Error; err != nil {
		log.Printf("Failed to update notification %d: %v", notification.NotificationID, err)
		return false
	}

	// อัพเดท Firestore
	updateFirestoreNotification(p.firestoreClient, notification, taskInfo.IsGroup, updateIsSend, db)

	return true
}

// getTaskInfo ดึงข้อมูล task พร้อมใช้ cache
func (p *NotificationProcessor) getTaskInfo(taskID int) (*TaskInfo, error) {
	// ตรวจสอบ cache ก่อน
	if info, exists := p.taskCache[taskID]; exists {
		return info, nil
	}

	// ดึงข้อมูล task
	var task model.Tasks
	if err := p.db.First(&task, taskID).Error; err != nil {
		return nil, fmt.Errorf("failed to find task: %v", err)
	}

	// ตรวจสอบว่าเป็น group task หรือไม่
	isGroup, err := p.isGroupTask(task)
	if err != nil {
		return nil, fmt.Errorf("error checking if task is group: %v", err)
	}

	// ดึง users และ tokens
	users, tokens, err := p.getUsersAndTokens(task)
	if err != nil {
		return nil, fmt.Errorf("failed to get users and tokens: %v", err)
	}

	boardID := "Today"
	if task.BoardID != nil {
		boardID = fmt.Sprintf("%d", *task.BoardID)
	}

	// สร้าง TaskInfo และเก็บใน cache
	taskInfo := &TaskInfo{
		Task:    task,
		Users:   users,
		Tokens:  tokens,
		IsGroup: isGroup,
		BoardID: boardID, // ✅ ใช้ค่าที่ตรวจสอบแล้ว
	}

	p.taskCache[taskID] = taskInfo
	return taskInfo, nil
}

func (p *NotificationProcessor) getTimeInfo(updateIsSend string, notification model.Notification) (string, error) {
	var timestamp string
	if updateIsSend == "1" {
		timestamp = notification.BeforeDueDate.Format(time.RFC3339)
	} else if updateIsSend == "2" {
		timestamp = notification.DueDate.Format(time.RFC3339)
	} else {
		return "", fmt.Errorf("invalid update status: %s", updateIsSend)
	}
	return timestamp, nil
}

// getUsersAndTokens ดึง users และ FCM tokens ในครั้งเดียว
func (p *NotificationProcessor) getUsersAndTokens(task model.Tasks) ([]model.User, []string, error) {
	var userIDs []int

	// ตรวจสอบว่ามี board_id หรือไม่
	if task.BoardID != nil {
		// ค้นหา users ใน BoardUser
		var boardUsers []model.BoardUser
		if err := p.db.Where("board_id = ?", *task.BoardID).Find(&boardUsers).Error; err != nil {
			return nil, nil, fmt.Errorf("failed to find board users: %v", err)
		}

		if len(boardUsers) > 0 {
			// มี users ใน board
			for _, boardUser := range boardUsers {
				userIDs = append(userIDs, boardUser.UserID)
			}
		} else {
			// ไม่มี users ใน BoardUser ให้ดู CreatedBy ใน Board
			var board model.Board
			if err := p.db.First(&board, *task.BoardID).Error; err != nil {
				return nil, nil, fmt.Errorf("failed to find board: %v", err)
			}
			userIDs = append(userIDs, board.CreatedBy)
		}
	} else {
		// ไม่มี board_id ให้ใช้ CreateBy ใน Tasks
		if task.CreateBy != nil {
			userIDs = append(userIDs, *task.CreateBy)
		} else {
			return nil, nil, fmt.Errorf("no user found for task %d", task.TaskID)
		}
	}

	// ดึง users
	var users []model.User
	if err := p.db.Where("user_id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to find users: %v", err)
	}

	// ดึง FCM tokens จาก Firestore
	var tokens []string
	ctx := context.Background()
	for _, user := range users {
		doc, err := p.firestoreClient.Collection("usersLogin").Doc(user.Email).Get(ctx)
		if err != nil {
			log.Printf("Failed to get Firestore doc for email %s: %v", user.Email, err)
			continue
		}

		if doc.Exists() {
			data := doc.Data()
			if fcmToken, ok := data["FMCToken"].(string); ok && fcmToken != "" {
				tokens = append(tokens, fcmToken)
			}
		}
	}

	return users, tokens, nil
}

// isGroupTask ตรวจสอบว่าเป็น group task หรือไม่
func (p *NotificationProcessor) isGroupTask(task model.Tasks) (bool, error) {
	// ไม่มี board_id = personal task
	if task.BoardID == nil {
		return false, nil
	}

	// ตรวจสอบว่ามี users ใน BoardUser หรือไม่
	var boardUserCount int64
	if err := p.db.Model(&model.BoardUser{}).Where("board_id = ?", *task.BoardID).Count(&boardUserCount).Error; err != nil {
		return false, fmt.Errorf("failed to count board users: %v", err)
	}

	// ถ้ามี users ใน board = group task
	if boardUserCount > 0 {
		return true, nil
	}

	// ถ้าไม่มี users ใน BoardUser ให้ตรวจสอบ creator
	var board model.Board
	if err := p.db.First(&board, *task.BoardID).Error; err != nil {
		return false, fmt.Errorf("failed to find board: %v", err)
	}

	return false, nil
}

func initializeFirebaseApp(serviceAccountKeyPath string) (*firebase.App, error) {
	ctx := context.Background()
	opt := option.WithCredentialsFile(serviceAccountKeyPath)
	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		return nil, fmt.Errorf("error initializing app: %v", err)
	}
	return app, nil
}

func buildNotificationMessage(noti model.Notification) string {
	taskName := noti.Task.TaskName

	// ตรวจสอบว่าเป็นการแจ้งเตือน beforedue_date หรือ due_date
	if noti.BeforeDueDate != nil && noti.IsSend == "0" {
		return fmt.Sprintf("⏰ ใกล้ถึงเวลา: %s", taskName)
	} else if noti.IsSend == "1" || (noti.BeforeDueDate == nil && noti.IsSend == "0") {
		return fmt.Sprintf("📌 ถึงกำหนดแล้ว: %s", taskName)
	}

	return ""
}

func sendMulticastNotification(app *firebase.App, tokens []string, title, body string, data map[string]string) error {
	ctx := context.Background()

	client, err := app.Messaging(ctx)
	if err != nil {
		return fmt.Errorf("error getting Messaging client: %v", err)
	}

	message := &messaging.MulticastMessage{
		Data: data,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Tokens: tokens,
	}

	response, err := client.SendEachForMulticast(ctx, message)
	if err != nil {
		return fmt.Errorf("error sending multicast message: %v", err)
	}

	log.Printf("Successfully sent multicast message. Success: %d, Failure: %d",
		response.SuccessCount, response.FailureCount)

	if response.FailureCount > 0 {
		for idx, resp := range response.Responses {
			if !resp.Success {
				log.Printf("Failed to send to token %s: %v", tokens[idx], resp.Error)
			}
		}
	}

	return nil
}

func updateFirestoreNotification(client *firestore.Client, notification model.Notification, isGroup bool, newStatus string, db *gorm.DB) error {
	ctx := context.Background()

	var docPath string
	// อัพเดท is_send status ใน Firestore
	updateData := map[string]interface{}{
		"isSend": newStatus,
	}

	if isGroup {
		// Group task: /BoardTasks/taskid/Notifications/notiid
		docPath = fmt.Sprintf("BoardTasks/%d/Notifications/%d", notification.TaskID, notification.NotificationID)

		if newStatus == "1" {
			// ดึงข้อมูล BoardUsers จาก database
			var boardUsers []model.BoardUser
			var task model.Tasks

			// ดึง task เพื่อได้ board_id
			if err := db.First(&task, notification.TaskID).Error; err != nil {
				return fmt.Errorf("failed to find task: %v", err)
			}

			if task.BoardID == nil {
				return fmt.Errorf("task has no board_id")
			}

			// ดึง board users
			if err := db.Where("board_id = ?", *task.BoardID).Find(&boardUsers).Error; err != nil {
				return fmt.Errorf("failed to find board users: %v", err)
			}

			// เตรียม update data สำหรับ group notification
			updateData["notiCount"] = false
			updateData["isNotiRemind"] = true
			updateData["isNotiRemindShow"] = true
			updateData["dueDateOld"] = firestore.Delete
			updateData["remindMeBeforeOld"] = firestore.Delete
			updateData["updatedAt"] = time.Now().UTC()

			// อัพเดท userNotifications สำหรับแต่ละ user ใน board
			// สร้าง nested map structure สำหรับ userNotifications
			userNotifications := make(map[string]interface{})
			for _, boardUser := range boardUsers {
				userIDStr := fmt.Sprintf("%d", boardUser.UserID)
				userNotifications[userIDStr] = map[string]interface{}{
					"isShow":           false,
					"isNotiRemindShow": true,
				}
			}
			updateData["userNotifications"] = userNotifications
		} else if newStatus == "2" {
			if notification.RecurringPattern == "onetime" {
				var boardUsers []model.BoardUser
				var task model.Tasks

				// ดึง task เพื่อได้ board_id
				if err := db.First(&task, notification.TaskID).Error; err != nil {
					return fmt.Errorf("failed to find task: %v", err)
				}

				if task.BoardID == nil {
					return fmt.Errorf("task has no board_id")
				}

				// ดึง board users
				if err := db.Where("board_id = ?", *task.BoardID).Find(&boardUsers).Error; err != nil {
					return fmt.Errorf("failed to find board users: %v", err)
				}
				updateData["isShow"] = true
				updateData["notiCount"] = false
				updateData["updatedAt"] = time.Now().UTC()

				// สำหรับการลบฟิลด์ใน Firestore ใช้ firestore.Delete
				updateData["dueDateOld"] = firestore.Delete
				updateData["remindMeBeforeOld"] = firestore.Delete
				userNotifications := make(map[string]interface{})
				for _, boardUser := range boardUsers {
					userIDStr := fmt.Sprintf("%d", boardUser.UserID)
					userNotifications[userIDStr] = map[string]interface{}{
						"isShow": true,
					}
				}
				updateData["userNotifications"] = userNotifications
			} else {
				var boardUsers []model.BoardUser
				var task model.Tasks

				// ดึง task เพื่อได้ board_id
				if err := db.First(&task, notification.TaskID).Error; err != nil {
					return fmt.Errorf("failed to find task: %v", err)
				}

				if task.BoardID == nil {
					return fmt.Errorf("task has no board_id")
				}

				// ดึง board users
				if err := db.Where("board_id = ?", *task.BoardID).Find(&boardUsers).Error; err != nil {
					return fmt.Errorf("failed to find board users: %v", err)
				}
				nextDueDate := calculateNextDueDate(notification)

				updateData["dueDate"] = nextDueDate
				updateData["updatedAt"] = time.Now().UTC()
				updateData["dueDateOld"] = notification.DueDate
				updateData["remindMeBeforeOld"] = notification.BeforeDueDate
				updateData["isShow"] = false
				updateData["isNotiRemind"] = false
				updateData["notiCount"] = false

				if notification.BeforeDueDate != nil {
					nextRemindMeBefore := notification.BeforeDueDate.AddDate(0, 0, 1) // ตัวอย่าง: เพิ่มวันถัดไป
					updateData["remindMeBefore"] = nextRemindMeBefore
				} else {
					updateData["remindMeBefore"] = nil // ถ้าไม่มี beforedue_date ให้เป็น nil
				}

				userNotifications := make(map[string]interface{})
				for _, boardUser := range boardUsers {
					userIDStr := fmt.Sprintf("%d", boardUser.UserID)
					userNotifications[userIDStr] = map[string]interface{}{
						"isShow":            true,
						"isNotiRemindShow":  true,
						"dueDateOld":        notification.DueDate,
						"remindMeBeforeOld": notification.BeforeDueDate,
					}
				}
				updateData["userNotifications"] = userNotifications

			}
		}
	} else {
		// Personal task: /Notifications/email/Tasks/notiid
		email, err := getTaskOwnerEmail(db, notification.TaskID)
		if err != nil {
			return fmt.Errorf("failed to get task owner email: %v", err)
		}
		docPath = fmt.Sprintf("Notifications/%s/Tasks/%d", email, notification.NotificationID)

		if newStatus == "1" {
			updateData["notiCount"] = false
			updateData["isNotiRemind"] = true
			updateData["isNotiRemindShow"] = true
			updateData["updatedAt"] = time.Now().UTC()

			// สำหรับการลบฟิลด์ใน Firestore ใช้ firestore.Delete
			updateData["dueDateOld"] = firestore.Delete
			updateData["remindMeBeforeOld"] = firestore.Delete
		} else if newStatus == "2" {
			if notification.RecurringPattern == "onetime" {
				updateData["isShow"] = true
				updateData["notiCount"] = false
				updateData["updatedAt"] = time.Now().UTC()

				// สำหรับการลบฟิลด์ใน Firestore ใช้ firestore.Delete
				updateData["dueDateOld"] = firestore.Delete
				updateData["remindMeBeforeOld"] = firestore.Delete
			} else {
				// สำหรับ recurring notifications (ไม่ใช่ onetime)
				// คำนวณ nextDueDate (คุณอาจต้องใช้ logic ที่เหมาะสมตาม recurring pattern)
				nextDueDate := calculateNextDueDate(notification)

				updateData["dueDate"] = nextDueDate
				updateData["updatedAt"] = time.Now().UTC()
				updateData["dueDateOld"] = notification.DueDate
				updateData["remindMeBeforeOld"] = notification.BeforeDueDate
				updateData["isShow"] = false
				updateData["isNotiRemind"] = false
				updateData["notiCount"] = false

				if notification.BeforeDueDate != nil {
					nextRemindMeBefore := notification.BeforeDueDate.AddDate(0, 0, 1) // ตัวอย่าง: เพิ่มวันถัดไป
					updateData["remindMeBefore"] = nextRemindMeBefore
				} else {
					updateData["remindMeBefore"] = nil // ถ้าไม่มี beforedue_date ให้เป็น nil
				}
			}

		}
	}

	_, err := client.Doc(docPath).Set(ctx, updateData, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("failed to update Firestore document at %s: %v", docPath, err)
	}

	log.Printf("Successfully updated Firestore notification at path: %s", docPath)
	return nil
}

func getTaskOwnerEmail(db *gorm.DB, taskID int) (string, error) {
	var task model.Tasks
	if err := db.First(&task, "task_id = ?", taskID).Error; err != nil {
		return "", fmt.Errorf("failed to find task: %v", err)
	}

	if task.CreateBy == nil {
		return "", fmt.Errorf("task has no creator (create_by is nil)")
	}

	var user model.User
	if err := db.First(&user, "user_id = ?", *task.CreateBy).Error; err != nil {
		return "", fmt.Errorf("failed to find user: %v", err)
	}

	return user.Email, nil
}

// Helper function สำหรับคำนวณ next due date ตาม recurring pattern
func calculateNextDueDate(notification model.Notification) time.Time {
	notification.DueDate = notification.DueDate.AddDate(0, 0, 1) // ตัวอย่าง: เพิ่มวันถัดไป
	return notification.DueDate
}
