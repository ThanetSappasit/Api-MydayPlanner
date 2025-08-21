package notification

import (
	"context"
	"fmt"
	"log"
	"mydayplanner/model"
	"os"
	"sync"
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

// UserTokenInfo เก็บ cache ของ user tokens
type UserTokenInfo struct {
	Email string
	Token string
}

// NotificationProcessor จัดการการประมวลผล notification
type NotificationProcessor struct {
	db              *gorm.DB
	firestoreClient *firestore.Client
	app             *firebase.App
	taskCache       map[int]*TaskInfo         // cache ข้อมูล task
	userTokenCache  map[string]string         // cache FCM tokens โดยใช้ email เป็น key
	boardUserCache  map[int][]model.BoardUser // cache board users
	userCache       map[int]model.User        // cache users
	mu              sync.RWMutex              // mutex สำหรับ thread safety
}

// NotificationBatch สำหรับประมวลผลแบบ batch
type NotificationBatch struct {
	Notification model.Notification
	UpdateIsSend string
	ShouldSend   bool
	MessageType  string // "before", "due", "snooze", "recurring"
}

// NotificationResult สำหรับ return ผลลัพธ์
type NotificationResult struct {
	Message      string `json:"message"`
	CurrentTime  string `json:"current_time"`
	TotalCount   int    `json:"total_count"`
	SuccessCount int    `json:"success_count"`
	ErrorCount   int    `json:"error_count"`
	SkippedCount int    `json:"skipped_count"` // เพิ่ม field สำหรับนับงานที่ข้าม
}

// API Controller - เดิม
func SendNotificationTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/send_notification", func(c *gin.Context) {
		SendNotification(c, db, firestoreClient)
	})
}

// API Handler - เรียกใช้ business logic
func SendNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	result, err := ProcessNotifications(db, firestoreClient)
	if err != nil {
		log.Printf("API Error: %v", err)
		c.JSON(500, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(200, gin.H{
		"message":       result.Message,
		"current_time":  result.CurrentTime,
		"total_count":   result.TotalCount,
		"success_count": result.SuccessCount,
		"error_count":   result.ErrorCount,
		"skipped_count": result.SkippedCount, // เพิ่ม skipped_count ใน response
	})
}

// Cron Job Function - Enhanced version
func SendNotificationJob(db *gorm.DB, firestoreClient *firestore.Client) {
	log.Println("🔔 Starting notification cron job...")

	// 1. Process regular notifications
	result, err := ProcessNotifications(db, firestoreClient)
	if err != nil {
		log.Printf("❌ Notification job error: %v", err)
	} else {
		log.Printf("✅ Regular notifications completed - Success: %d, Error: %d, Skipped: %d, Total: %d",
			result.SuccessCount, result.ErrorCount, result.SkippedCount, result.TotalCount)
	}

	time.Sleep(1 * time.Second)

	// 2. Process snooze notifications
	log.Println("😴 Processing snooze notifications...")
	snoozeResult, err := ProcessSnoozeNotifications(db, firestoreClient)
	if err != nil {
		log.Printf("⚠️ Warning: Snooze notification error: %v", err)
	} else {
		log.Printf("✅ Snooze notifications completed - Success: %d, Error: %d, Skipped: %d, Total: %d",
			snoozeResult.SuccessCount, snoozeResult.ErrorCount, snoozeResult.SkippedCount, snoozeResult.TotalCount)
	}

	time.Sleep(1 * time.Second)

	// 3. Process recurring notifications (daily at 7:00 AM Thailand time)
	log.Println("🔄 Processing recurring notifications...")
	recurringResult, err := ProcessRecurringNotifications(db, firestoreClient)
	if err != nil {
		log.Printf("⚠️ Warning: Recurring notification error: %v", err)
	} else {
		log.Printf("✅ Recurring notifications completed - Success: %d, Error: %d, Skipped: %d, Total: %d",
			recurringResult.SuccessCount, recurringResult.ErrorCount, recurringResult.SkippedCount, recurringResult.TotalCount)
	}
}

func ProcessNotifications(db *gorm.DB, firestoreClient *firestore.Client) (*NotificationResult, error) {
	// โหลด environment variables
	if err := godotenv.Load(); err != nil {
		fmt.Println("Warning: No .env file found or failed to load")
	}

	now := time.Now().UTC()

	// เริ่ม Firebase app
	serviceAccountKeyPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_1")
	if serviceAccountKeyPath == "" {
		return nil, fmt.Errorf("Firebase credentials not configured")
	}

	app, err := initializeFirebaseApp(serviceAccountKeyPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize Firebase app: %s", err.Error())
	}

	var notifications []model.Notification

	// Query สำหรับ notifications ที่พร้อมส่งแล้ว (ไม่รวม snooze และ recurring)
	query := db.Preload("Task").Where(
		"(is_send = '0' AND ((beforedue_date IS NOT NULL AND beforedue_date <= ?) OR (beforedue_date IS NULL AND due_date <= ?))) OR "+
			"(is_send = '1' AND due_date <= ?)",
		now, now, now,
	)

	if err := query.Find(&notifications).Error; err != nil {
		return nil, fmt.Errorf("Failed to fetch notifications: %v", err)
	}

	// กรอง notifications ที่งานยังไม่เสร็จ (status != '2')
	filteredNotifications := []model.Notification{}
	for _, noti := range notifications {
		if noti.Task.Status != "2" {
			filteredNotifications = append(filteredNotifications, noti)
		}
	}

	// สร้าง processor พร้อม cache
	processor := &NotificationProcessor{
		db:              db,
		firestoreClient: firestoreClient,
		app:             app,
		taskCache:       make(map[int]*TaskInfo),
		userTokenCache:  make(map[string]string),
		boardUserCache:  make(map[int][]model.BoardUser),
		userCache:       make(map[int]model.User),
	}

	// Preload ข้อมูลที่จำเป็น
	processor.preloadData(filteredNotifications)

	successCount := 0
	errorCount := 0
	skippedCount := 0 // เพิ่ม counter สำหรับงานที่ข้าม

	// สร้าง batches สำหรับการประมวลผล
	batches := processor.prepareBatches(filteredNotifications, now)

	// ประมวลผลแบบ concurrent
	const maxWorkers = 10
	semaphore := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var resultMu sync.Mutex

	for _, batch := range batches {
		if batch.ShouldSend {
			wg.Add(1)
			go func(b NotificationBatch) {
				defer wg.Done()
				semaphore <- struct{}{}        // acquire semaphore
				defer func() { <-semaphore }() // release semaphore

				result := processor.processNotificationConcurrent(b.Notification, b.UpdateIsSend, b.MessageType, now, db)
				resultMu.Lock()
				switch result {
				case "success":
					successCount++
				case "skipped":
					skippedCount++
				case "error":
					errorCount++
				}
				resultMu.Unlock()
			}(batch)
		}
	}

	wg.Wait()

	return &NotificationResult{
		Message:      "Notifications processed successfully",
		CurrentTime:  now.Format(time.RFC3339),
		TotalCount:   len(filteredNotifications),
		SuccessCount: successCount,
		ErrorCount:   errorCount,
		SkippedCount: skippedCount,
	}, nil
}

// ProcessSnoozeNotifications จัดการการแจ้งเตือน snooze
func ProcessSnoozeNotifications(db *gorm.DB, firestoreClient *firestore.Client) (*NotificationResult, error) {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Warning: No .env file found or failed to load")
	}

	now := time.Now().UTC()

	serviceAccountKeyPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_1")
	if serviceAccountKeyPath == "" {
		return nil, fmt.Errorf("Firebase credentials not configured")
	}

	app, err := initializeFirebaseApp(serviceAccountKeyPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize Firebase app: %s", err.Error())
	}

	var notifications []model.Notification

	// Query สำหรับ snooze notifications (is_send = '3' และถึงเวลา snooze แล้ว)
	query := db.Preload("Task").Where(
		"is_send = '3' AND snooze IS NOT NULL AND snooze <= ?",
		now,
	)

	if err := query.Find(&notifications).Error; err != nil {
		return nil, fmt.Errorf("Failed to fetch snooze notifications: %v", err)
	}

	// กรอง notifications ที่งานยังไม่เสร็จ
	filteredNotifications := []model.Notification{}
	for _, noti := range notifications {
		if noti.Task.Status != "2" {
			filteredNotifications = append(filteredNotifications, noti)
		}
	}

	processor := &NotificationProcessor{
		db:              db,
		firestoreClient: firestoreClient,
		app:             app,
		taskCache:       make(map[int]*TaskInfo),
		userTokenCache:  make(map[string]string),
		boardUserCache:  make(map[int][]model.BoardUser),
		userCache:       make(map[int]model.User),
	}

	processor.preloadData(filteredNotifications)

	successCount := 0
	errorCount := 0
	skippedCount := 0

	const maxWorkers = 10
	semaphore := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var resultMu sync.Mutex

	for _, notification := range filteredNotifications {
		wg.Add(1)
		go func(noti model.Notification) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			result := processor.processSnoozeNotification(noti, now, db)
			resultMu.Lock()
			switch result {
			case "success":
				successCount++
			case "skipped":
				skippedCount++
			case "error":
				errorCount++
			}
			resultMu.Unlock()
		}(notification)
	}

	wg.Wait()

	return &NotificationResult{
		Message:      "Snooze notifications processed successfully",
		CurrentTime:  now.Format(time.RFC3339),
		TotalCount:   len(filteredNotifications),
		SuccessCount: successCount,
		ErrorCount:   errorCount,
		SkippedCount: skippedCount,
	}, nil
}

// ProcessRecurringNotifications จัดการการแจ้งเตือน recurring
func ProcessRecurringNotifications(db *gorm.DB, firestoreClient *firestore.Client) (*NotificationResult, error) {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Warning: No .env file found or failed to load")
	}

	// ใช้ Thailand timezone (GMT+7)
	thailandTZ, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		return nil, fmt.Errorf("Failed to load Thailand timezone: %v", err)
	}

	now := time.Now().In(thailandTZ)

	// ตรวจสอบว่าเป็นเวลา 7:00 AM หรือไม่ (ให้ tolerance 1 นาที)
	if now.Hour() != 7 || now.Minute() > 1 {
		log.Println("⏰ Not time for recurring notifications (should be 7:00 AM Thailand time)")
		return &NotificationResult{
			Message:      "Not time for recurring notifications",
			CurrentTime:  now.Format(time.RFC3339),
			TotalCount:   0,
			SuccessCount: 0,
			ErrorCount:   0,
			SkippedCount: 0,
		}, nil
	}

	serviceAccountKeyPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_1")
	if serviceAccountKeyPath == "" {
		return nil, fmt.Errorf("Firebase credentials not configured")
	}

	app, err := initializeFirebaseApp(serviceAccountKeyPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize Firebase app: %s", err.Error())
	}

	var notifications []model.Notification

	// Query สำหรับ recurring notifications ที่ไม่ใช่ "onetime" และงานยังไม่เสร็จ
	query := db.Preload("Task").Where(
		"recurring_pattern != 'onetime' AND recurring_pattern != 'never'",
	)

	if err := query.Find(&notifications).Error; err != nil {
		return nil, fmt.Errorf("Failed to fetch recurring notifications: %v", err)
	}

	// กรอง notifications ที่งานยังไม่เสร็จและเลยกำหนดมาแล้ว
	filteredNotifications := []model.Notification{}
	nowUTC := time.Now().UTC()

	for _, noti := range notifications {
		if noti.Task.Status != "2" && noti.DueDate != nil && noti.DueDate.Before(nowUTC) {
			filteredNotifications = append(filteredNotifications, noti)
		}
	}

	processor := &NotificationProcessor{
		db:              db,
		firestoreClient: firestoreClient,
		app:             app,
		taskCache:       make(map[int]*TaskInfo),
		userTokenCache:  make(map[string]string),
		boardUserCache:  make(map[int][]model.BoardUser),
		userCache:       make(map[int]model.User),
	}

	processor.preloadData(filteredNotifications)

	successCount := 0
	errorCount := 0
	skippedCount := 0

	const maxWorkers = 10
	semaphore := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var resultMu sync.Mutex

	for _, notification := range filteredNotifications {
		wg.Add(1)
		go func(noti model.Notification) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			result := processor.processRecurringNotification(noti, nowUTC, db)
			resultMu.Lock()
			switch result {
			case "success":
				successCount++
			case "skipped":
				skippedCount++
			case "error":
				errorCount++
			}
			resultMu.Unlock()
		}(notification)
	}

	wg.Wait()

	return &NotificationResult{
		Message:      "Recurring notifications processed successfully",
		CurrentTime:  nowUTC.Format(time.RFC3339),
		TotalCount:   len(filteredNotifications),
		SuccessCount: successCount,
		ErrorCount:   errorCount,
		SkippedCount: skippedCount,
	}, nil
}

// processSnoozeNotification ประมวลผล snooze notification - แก้ไขให้ return string
func (p *NotificationProcessor) processSnoozeNotification(notification model.Notification, now time.Time, db *gorm.DB) string {
	fmt.Printf("Processing snooze notification for Task ID: %d\n", notification.TaskID)

	taskInfo, err := p.getTaskInfoOptimized(notification.TaskID)
	if err != nil {
		log.Printf("Failed to get task info for snooze Task ID %d: %v", notification.TaskID, err)
		return "error"
	}

	if len(taskInfo.Tokens) == 0 {
		log.Printf("⏭️ Skipping snooze notification for Task ID %d - no FCM tokens (user disabled notifications)", notification.TaskID)
		// ยังคงอัปเดต database แม้ไม่ส่งแจ้งเตือน
		nextSnooze := now.Add(10 * time.Minute)
		if err := p.db.Model(&notification).Updates(map[string]interface{}{
			"snooze": nextSnooze,
		}).Error; err != nil {
			log.Printf("Failed to update snooze for notification %d: %v", notification.NotificationID, err)
			return "error"
		}
		updateFirestoreNotification(p.firestoreClient, notification, taskInfo.IsGroup, "3", db)
		return "skipped"
	}

	// สร้างข้อความสำหรับ snooze
	message := fmt.Sprintf("⏰ งานของคุณเลยเวลาที่กำหนดมาแล้ว: %s", taskInfo.Task.TaskName)

	data := map[string]string{
		"taskid":    fmt.Sprintf("%d", notification.TaskID),
		"timestamp": now.Format(time.RFC3339),
		"boardid":   fmt.Sprintf("%v", taskInfo.BoardID),
		"type":      "snooze",
	}

	err = sendMulticastNotification(p.app, taskInfo.Tokens, "แจ้งเตือนงาน", message, data)
	if err != nil {
		log.Printf("Failed to send snooze notification for Task ID %d: %v", notification.TaskID, err)
		return "error"
	}

	// อัปเดต snooze เป็นเวลาถัดไป (บวก 10 นาที) และรีเซ็ต is_send เป็น 3
	nextSnooze := now.Add(10 * time.Minute)
	if err := p.db.Model(&notification).Updates(map[string]interface{}{
		"snooze": nextSnooze,
	}).Error; err != nil {
		log.Printf("Failed to update snooze for notification %d: %v", notification.NotificationID, err)
		return "error"
	}

	updateFirestoreNotification(p.firestoreClient, notification, taskInfo.IsGroup, "3", db)
	fmt.Printf("✅ Successfully sent snooze notification for Task ID: %d\n", notification.TaskID)
	return "success"
}

// processRecurringNotification ประมวลผล recurring notification - แก้ไขให้ return string
func (p *NotificationProcessor) processRecurringNotification(notification model.Notification, now time.Time, db *gorm.DB) string {
	fmt.Printf("Processing recurring notification for Task ID: %d\n", notification.TaskID)

	taskInfo, err := p.getTaskInfoOptimized(notification.TaskID)
	if err != nil {
		log.Printf("Failed to get task info for recurring Task ID %d: %v", notification.TaskID, err)
		return "error"
	}

	if len(taskInfo.Tokens) == 0 {
		log.Printf("⏭️ Skipping recurring notification for Task ID %d - no FCM tokens (user disabled notifications)", notification.TaskID)
		// ยังคงอัปเดต Firestore แม้ไม่ส่งแจ้งเตือน
		updateFirestoreNotification(p.firestoreClient, notification, taskInfo.IsGroup, "recurring", db)
		return "skipped"
	}

	// สร้างข้อความสำหรับ recurring
	daysPassed := int(now.Sub(*notification.DueDate).Hours() / 24)
	var message string
	if daysPassed == 1 {
		message = fmt.Sprintf("📅 งานเลยกำหนด 1 วันแล้ว: %s", taskInfo.Task.TaskName)
	} else {
		message = fmt.Sprintf("📅 งานเลยกำหนด %d วันแล้ว: %s", daysPassed, taskInfo.Task.TaskName)
	}

	data := map[string]string{
		"taskid":    fmt.Sprintf("%d", notification.TaskID),
		"timestamp": now.Format(time.RFC3339),
		"boardid":   fmt.Sprintf("%v", taskInfo.BoardID),
		"type":      "recurring",
	}

	err = sendMulticastNotification(p.app, taskInfo.Tokens, "แจ้งเตือนงาน", message, data)
	if err != nil {
		log.Printf("Failed to send recurring notification for Task ID %d: %v", notification.TaskID, err)
		return "error"
	}

	updateFirestoreNotification(p.firestoreClient, notification, taskInfo.IsGroup, "recurring", db)
	fmt.Printf("✅ Successfully sent recurring notification for Task ID: %d\n", notification.TaskID)
	return "success"
}

// preloadData โหลดข้อมูลที่จำเป็นล่วงหน้าเพื่อลด database queries
func (p *NotificationProcessor) preloadData(notifications []model.Notification) {
	// เก็บ task IDs ที่ unique
	taskIDs := make(map[int]bool)
	boardIDs := make(map[int]bool)

	for _, noti := range notifications {
		taskIDs[noti.TaskID] = true
		if noti.Task.BoardID != nil {
			boardIDs[*noti.Task.BoardID] = true
		}
	}

	// Preload board users สำหรับทุก board
	var allBoardUsers []model.BoardUser
	var boardIDList []int
	for boardID := range boardIDs {
		boardIDList = append(boardIDList, boardID)
	}

	if len(boardIDList) > 0 {
		p.db.Where("board_id IN ?", boardIDList).Find(&allBoardUsers)

		// จัดกลุ่ม board users ตาม board_id
		for _, bu := range allBoardUsers {
			p.boardUserCache[bu.BoardID] = append(p.boardUserCache[bu.BoardID], bu)
		}
	}

	// Preload users
	userIDs := make(map[int]bool)
	for _, bu := range allBoardUsers {
		userIDs[bu.UserID] = true
	}

	// เพิ่ม creator IDs
	for _, noti := range notifications {
		if noti.Task.CreateBy != nil {
			userIDs[*noti.Task.CreateBy] = true
		}
	}

	var userIDList []int
	for userID := range userIDs {
		userIDList = append(userIDList, userID)
	}

	if len(userIDList) > 0 {
		var users []model.User
		p.db.Where("user_id IN ?", userIDList).Find(&users)

		for _, user := range users {
			p.userCache[user.UserID] = user
		}

		// Preload FCM tokens
		p.preloadTokens(users)
	}
}

// preloadTokens โหลด FCM tokens จาก Firestore แบบ batch
func (p *NotificationProcessor) preloadTokens(users []model.User) {
	ctx := context.Background()
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5) // จำกัด concurrent Firestore calls

	for _, user := range users {
		wg.Add(1)
		go func(u model.User) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			doc, err := p.firestoreClient.Collection("usersLogin").Doc(u.Email).Get(ctx)
			if err == nil && doc.Exists() {
				data := doc.Data()
				if fcmToken, ok := data["FMCToken"].(string); ok && fcmToken != "" {
					p.mu.Lock()
					p.userTokenCache[u.Email] = fcmToken
					p.mu.Unlock()
				}
			}
		}(user)
	}
	wg.Wait()
}

// prepareBatches เตรียม batches สำหรับการประมวลผล
func (p *NotificationProcessor) prepareBatches(notifications []model.Notification, now time.Time) []NotificationBatch {
	batches := make([]NotificationBatch, 0, len(notifications))

	for _, notification := range notifications {
		shouldSend, updateIsSend, messageType := p.shouldSendNotification(notification, now)
		batches = append(batches, NotificationBatch{
			Notification: notification,
			UpdateIsSend: updateIsSend,
			ShouldSend:   shouldSend,
			MessageType:  messageType,
		})
	}

	return batches
}

// shouldSendNotification ตรวจสอบว่าควรส่ง notification หรือไม่
func (p *NotificationProcessor) shouldSendNotification(notification model.Notification, now time.Time) (bool, string, string) {
	if notification.IsSend == "0" {
		if notification.BeforeDueDate != nil && (notification.BeforeDueDate.Before(now) || notification.BeforeDueDate.Equal(now)) {
			return true, "1", "before"
		} else if notification.BeforeDueDate == nil && (notification.DueDate.Before(now) || notification.DueDate.Equal(now)) {
			return true, "2", "due"
		}
	} else if notification.IsSend == "1" {
		if notification.DueDate.Before(now) || notification.DueDate.Equal(now) {
			return true, "2", "due"
		}
	}
	return false, "", ""
}

// processNotificationConcurrent ประมวลผล notification แบบ concurrent-safe - แก้ไขให้ return string
func (p *NotificationProcessor) processNotificationConcurrent(notification model.Notification, updateIsSend string, messageType string, now time.Time, db *gorm.DB) string {
	fmt.Printf("Processing %s notification for Task ID: %d\n", messageType, notification.TaskID)

	message := buildNotificationMessage(notification, messageType)
	if message == "" {
		return "error"
	}

	taskInfo, err := p.getTaskInfoOptimized(notification.TaskID)
	if err != nil {
		log.Printf("Failed to get task info for Task ID %d: %v", notification.TaskID, err)
		return "error"
	}

	if len(taskInfo.Tokens) == 0 {
		log.Printf("⏭️ Skipping notification for Task ID %d - no FCM tokens (user disabled notifications)", notification.TaskID)
		// ยังคงอัปเดต database status แม้ไม่ส่งแจ้งเตือน
		if err := p.db.Model(&notification).Update("is_send", updateIsSend).Error; err != nil {
			log.Printf("Failed to update notification %d: %v", notification.NotificationID, err)
			return "error"
		}
		updateFirestoreNotification(p.firestoreClient, notification, taskInfo.IsGroup, updateIsSend, db)
		return "skipped"
	}

	timestamp, err := p.getTimeInfo(updateIsSend, notification)
	if err != nil {
		log.Printf("Failed to get time info for Task ID %d: %v", notification.TaskID, err)
		return "error"
	}

	data := map[string]string{
		"taskid":    fmt.Sprintf("%d", notification.TaskID),
		"timestamp": timestamp,
		"boardid":   fmt.Sprintf("%v", taskInfo.BoardID),
		"type":      messageType,
	}

	err = sendMulticastNotification(p.app, taskInfo.Tokens, "แจ้งเตือนงาน", message, data)
	if err != nil {
		log.Printf("Failed to send notification for Task ID %d: %v", notification.TaskID, err)
		return "error"
	}

	if err := p.db.Model(&notification).Update("is_send", updateIsSend).Error; err != nil {
		log.Printf("Failed to update notification %d: %v", notification.NotificationID, err)
		return "error"
	}

	updateFirestoreNotification(p.firestoreClient, notification, taskInfo.IsGroup, updateIsSend, db)
	fmt.Printf("✅ Successfully sent %s notification for Task ID: %d\n", messageType, notification.TaskID)
	return "success"
}

// getTaskInfoOptimized ดึงข้อมูล task โดยใช้ cache ที่ preload แล้ว
func (p *NotificationProcessor) getTaskInfoOptimized(taskID int) (*TaskInfo, error) {
	p.mu.RLock()
	if info, exists := p.taskCache[taskID]; exists {
		p.mu.RUnlock()
		return info, nil
	}
	p.mu.RUnlock()

	var task model.Tasks
	if err := p.db.First(&task, taskID).Error; err != nil {
		return nil, fmt.Errorf("failed to find task: %v", err)
	}

	isGroup, err := p.isGroupTaskOptimized(task)
	if err != nil {
		return nil, fmt.Errorf("error checking if task is group: %v", err)
	}

	users, tokens, err := p.getUsersAndTokensOptimized(task)
	if err != nil {
		return nil, fmt.Errorf("failed to get users and tokens: %v", err)
	}

	boardID := "Today"
	if task.BoardID != nil {
		boardID = fmt.Sprintf("%d", *task.BoardID)
	}

	taskInfo := &TaskInfo{
		Task:    task,
		Users:   users,
		Tokens:  tokens,
		IsGroup: isGroup,
		BoardID: boardID,
	}

	p.mu.Lock()
	p.taskCache[taskID] = taskInfo
	p.mu.Unlock()

	return taskInfo, nil
}

// getUsersAndTokensOptimized ใช้ cache แทนการ query database
func (p *NotificationProcessor) getUsersAndTokensOptimized(task model.Tasks) ([]model.User, []string, error) {
	var users []model.User
	var tokens []string

	if task.BoardID != nil {
		// ใช้ cached board users
		p.mu.RLock()
		boardUsers, exists := p.boardUserCache[*task.BoardID]
		p.mu.RUnlock()

		if exists && len(boardUsers) > 0 {
			for _, boardUser := range boardUsers {
				if user, userExists := p.userCache[boardUser.UserID]; userExists {
					users = append(users, user)
					if token, tokenExists := p.userTokenCache[user.Email]; tokenExists {
						tokens = append(tokens, token)
					}
				}
			}
		} else {
			// ไม่มี users ใน BoardUser ให้ดู CreatedBy ใน Board
			var board model.Board
			if err := p.db.First(&board, *task.BoardID).Error; err != nil {
				return nil, nil, fmt.Errorf("failed to find board: %v", err)
			}

			if user, exists := p.userCache[board.CreatedBy]; exists {
				users = append(users, user)
				if token, tokenExists := p.userTokenCache[user.Email]; tokenExists {
					tokens = append(tokens, token)
				}
			}
		}
	} else {
		// ไม่มี board_id ให้ใช้ CreateBy ใน Tasks
		if task.CreateBy != nil {
			if user, exists := p.userCache[*task.CreateBy]; exists {
				users = append(users, user)
				if token, tokenExists := p.userTokenCache[user.Email]; tokenExists {
					tokens = append(tokens, token)
				}
			}
		}
	}

	return users, tokens, nil
}

// isGroupTaskOptimized ใช้ cached data
func (p *NotificationProcessor) isGroupTaskOptimized(task model.Tasks) (bool, error) {
	if task.BoardID == nil {
		return false, nil
	}

	p.mu.RLock()
	boardUsers, exists := p.boardUserCache[*task.BoardID]
	p.mu.RUnlock()

	return exists && len(boardUsers) > 0, nil
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

func initializeFirebaseApp(serviceAccountKeyPath string) (*firebase.App, error) {
	ctx := context.Background()
	opt := option.WithCredentialsFile(serviceAccountKeyPath)
	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		return nil, fmt.Errorf("error initializing app: %v", err)
	}
	return app, nil
}

func buildNotificationMessage(noti model.Notification, messageType string) string {
	taskName := noti.Task.TaskName

	switch messageType {
	case "before":
		return fmt.Sprintf("⏰ ใกล้ถึงเวลา: %s", taskName)
	case "due":
		return fmt.Sprintf("📌 ถึงกำหนดแล้ว: %s", taskName)
	case "snooze":
		return fmt.Sprintf("⏰ งานของคุณเลยเวลาที่กำหนดมาแล้ว: %s", taskName)
	case "recurring":
		return fmt.Sprintf("📅 งานยังไม่เสร็จ: %s", taskName)
	default:
		// Fallback to original logic
		if noti.BeforeDueDate != nil && noti.IsSend == "0" {
			return fmt.Sprintf("⏰ ใกล้ถึงเวลา: %s", taskName)
		} else if noti.IsSend == "1" || (noti.BeforeDueDate == nil && noti.IsSend == "0") {
			return fmt.Sprintf("📌 ถึงกำหนดแล้ว: %s", taskName)
		}
	}

	return ""
}

func sendMulticastNotification(app *firebase.App, tokens []string, title, body string, data map[string]string) error {
	ctx := context.Background()

	client, err := app.Messaging(ctx)
	if err != nil {
		return fmt.Errorf("error getting Messaging client: %v", err)
	}

	// แบ่ง tokens เป็น batches (FCM จำกัดที่ 500 tokens ต่อ request)
	const batchSize = 500
	for i := 0; i < len(tokens); i += batchSize {
		end := i + batchSize
		if end > len(tokens) {
			end = len(tokens)
		}

		batch := tokens[i:end]
		message := &messaging.MulticastMessage{
			Data: data,
			Notification: &messaging.Notification{
				Title: title,
				Body:  body,
			},
			Tokens: batch,
		}

		response, err := client.SendEachForMulticast(ctx, message)
		if err != nil {
			log.Printf("Error sending batch %d-%d: %v", i, end-1, err)
			continue
		}

		log.Printf("Batch %d-%d: Success: %d, Failure: %d",
			i, end-1, response.SuccessCount, response.FailureCount)

		if response.FailureCount > 0 {
			for idx, resp := range response.Responses {
				if !resp.Success {
					log.Printf("Failed to send to token %s: %v", batch[idx], resp.Error)
				}
			}
		}
	}

	return nil
}

func updateFirestoreNotification(client *firestore.Client, notification model.Notification, isGroup bool, newStatus string, db *gorm.DB) error {
	ctx := context.Background()

	var docPath string
	updateData := map[string]interface{}{
		"isSend": newStatus,
	}

	if isGroup {
		docPath = fmt.Sprintf("BoardTasks/%d/Notifications/%d", notification.TaskID, notification.NotificationID)

		if newStatus == "1" {
			var boardUsers []model.BoardUser
			var task model.Tasks

			if err := db.First(&task, notification.TaskID).Error; err != nil {
				return fmt.Errorf("failed to find task: %v", err)
			}

			if task.BoardID == nil {
				return fmt.Errorf("task has no board_id")
			}

			if err := db.Where("board_id = ?", *task.BoardID).Find(&boardUsers).Error; err != nil {
				return fmt.Errorf("failed to find board users: %v", err)
			}

			updateData["isNotiRemind"] = true
			updateData["isNotiRemindShow"] = true
			updateData["dueDateOld"] = firestore.Delete
			updateData["remindMeBeforeOld"] = firestore.Delete
			updateData["updatedAt"] = time.Now().UTC()

			userNotifications := make(map[string]interface{})
			for _, boardUser := range boardUsers {
				userIDStr := fmt.Sprintf("%d", boardUser.UserID)
				userNotifications[userIDStr] = map[string]interface{}{
					"isShow":           false,
					"isNotiRemindShow": true,
					"notiCount":        false,
				}
			}
			updateData["userNotifications"] = userNotifications
		} else if newStatus == "2" {
			if notification.RecurringPattern == "onetime" {
				var boardUsers []model.BoardUser
				var task model.Tasks

				if err := db.First(&task, notification.TaskID).Error; err != nil {
					return fmt.Errorf("failed to find task: %v", err)
				}

				if task.BoardID == nil {
					return fmt.Errorf("task has no board_id")
				}

				if err := db.Where("board_id = ?", *task.BoardID).Find(&boardUsers).Error; err != nil {
					return fmt.Errorf("failed to find board users: %v", err)
				}

				updateData["isShow"] = true
				updateData["updatedAt"] = time.Now().UTC()
				updateData["dueDateOld"] = firestore.Delete
				updateData["remindMeBeforeOld"] = firestore.Delete

				userNotifications := make(map[string]interface{})
				for _, boardUser := range boardUsers {
					userIDStr := fmt.Sprintf("%d", boardUser.UserID)
					userNotifications[userIDStr] = map[string]interface{}{
						"isShow":    true,
						"notiCount": false,
					}
				}
				updateData["userNotifications"] = userNotifications
			} else {
				// Recurring task - update to next day
				var boardUsers []model.BoardUser
				var task model.Tasks

				if err := db.First(&task, notification.TaskID).Error; err != nil {
					return fmt.Errorf("failed to find task: %v", err)
				}

				if task.BoardID == nil {
					return fmt.Errorf("task has no board_id")
				}

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

				if notification.BeforeDueDate != nil {
					nextRemindMeBefore := notification.BeforeDueDate.AddDate(0, 0, 1)
					updateData["remindMeBefore"] = nextRemindMeBefore
				} else {
					updateData["remindMeBefore"] = nil
				}

				userNotifications := make(map[string]interface{})
				for _, boardUser := range boardUsers {
					userIDStr := fmt.Sprintf("%d", boardUser.UserID)
					userNotifications[userIDStr] = map[string]interface{}{
						"notiCount":         false,
						"isShow":            true,
						"isNotiRemindShow":  true,
						"dueDateOld":        notification.DueDate,
						"remindMeBeforeOld": notification.BeforeDueDate,
					}
				}
				updateData["userNotifications"] = userNotifications
			}
		} else if newStatus == "3" {
			// Snooze notification
			updateData["isSnooze"] = true
			updateData["updatedAt"] = time.Now().UTC()
		} else if newStatus == "recurring" {
			// Daily recurring reminder - don't change much, just log
			updateData["lastRecurringNotification"] = time.Now().UTC()
		}
	} else {
		email, err := getTaskOwnerEmail(db, notification.TaskID)
		if err != nil {
			return fmt.Errorf("failed to get task owner email: %v", err)
		}
		docPath = fmt.Sprintf("Notifications/%s/Tasks/%d", email, notification.NotificationID)

		if newStatus == "1" {
			updateData["isNotiRemind"] = true
			updateData["isNotiRemindShow"] = true
			updateData["updatedAt"] = time.Now().UTC()
			updateData["dueDateOld"] = firestore.Delete
			updateData["remindMeBeforeOld"] = firestore.Delete
		} else if newStatus == "2" {
			if notification.RecurringPattern == "onetime" {
				updateData["isShow"] = true
				updateData["updatedAt"] = time.Now().UTC()
				updateData["dueDateOld"] = firestore.Delete
				updateData["remindMeBeforeOld"] = firestore.Delete
			} else {
				// Recurring task
				nextDueDate := calculateNextDueDate(notification)

				updateData["dueDate"] = nextDueDate
				updateData["updatedAt"] = time.Now().UTC()
				updateData["dueDateOld"] = notification.DueDate
				updateData["remindMeBeforeOld"] = notification.BeforeDueDate
				updateData["isShow"] = false
				updateData["isNotiRemind"] = false

				if notification.BeforeDueDate != nil {
					nextRemindMeBefore := notification.BeforeDueDate.AddDate(0, 0, 1)
					updateData["remindMeBefore"] = nextRemindMeBefore
				} else {
					updateData["remindMeBefore"] = nil
				}
			}
		} else if newStatus == "3" {
			// Snooze notification
			updateData["isSnooze"] = true
			updateData["updatedAt"] = time.Now().UTC()
		} else if newStatus == "recurring" {
			// Daily recurring reminder
			updateData["lastRecurringNotification"] = time.Now().UTC()
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

func calculateNextDueDate(notification model.Notification) *time.Time {
	if notification.DueDate == nil {
		return nil
	}

	// คำนวณวันถัดไป
	nextDate := notification.DueDate.AddDate(0, 0, 1)
	return &nextDate
}
