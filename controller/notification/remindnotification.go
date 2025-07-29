package notification

import (
	"context"
	"fmt"
	"log"
	"mydayplanner/model"
	"os"
	"strconv"
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

func PushNotificationTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/remindtask", func(c *gin.Context) {
		RemindTask(c, db, firestoreClient)
	})
}

func RemindTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// โหลด env ไว้ครั้งเดียว
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: No .env file found or failed to load")
	}

	now := time.Now().UTC()

	var notifications []model.Notification

	// Query notification ที่ยังไม่ส่ง และถึงเวลาตาม beforedue_date หรือ due_date
	err := db.
		Preload("Task").
		Where("is_send = ?", "0").
		Where(
			db.
				Where("beforedue_date IS NOT NULL AND beforedue_date <= ?", now).
				Or("due_date <= ?", now),
		).
		Find(&notifications).Error

	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to fetch notifications", "details": err.Error()})
		return
	}

	type BoardType string
	const (
		BoardToday   BoardType = "today"
		BoardPrivate BoardType = "private"
		BoardGroup   BoardType = "group"
	)

	var beforeDueList, dueList []model.Notification
	notificationUserMap := make(map[int][]int)          // NotificationID -> []UserID
	notificationBoardTypeMap := make(map[int]BoardType) // NotificationID -> BoardType

	for _, n := range notifications {
		// แยกประเภท notification ตามเวลา
		if n.BeforeDueDate != nil && now.After(*n.BeforeDueDate) {
			beforeDueList = append(beforeDueList, n)
		} else if now.After(n.DueDate) {
			dueList = append(dueList, n)
		}

		task := n.Task
		var userIDs []int
		var boardType BoardType

		// กรณี BoardID เป็น nil => today board
		if task.BoardID == nil {
			boardType = BoardToday
			if task.CreateBy != nil {
				userIDs = append(userIDs, *task.CreateBy)
			}
		} else {
			// เช็คจำนวน user ใน board ว่ากลุ่มหรือไม่
			var count int64
			if err := db.Model(&model.BoardUser{}).
				Where("board_id = ?", *task.BoardID).
				Count(&count).Error; err != nil {
				log.Println("Error checking board user count:", err)
				continue
			}

			if count > 1 {
				// group board
				boardType = BoardGroup
				var boardUsers []model.BoardUser
				if err := db.Where("board_id = ?", *task.BoardID).Find(&boardUsers).Error; err == nil {
					for _, bu := range boardUsers {
						userIDs = append(userIDs, bu.UserID)
					}
				} else {
					log.Println("Error loading board users:", err)
				}
			} else {
				// private board (count == 1)
				boardType = BoardPrivate
				var board model.Board
				if err := db.Where("board_id = ?", *task.BoardID).First(&board).Error; err == nil {
					userIDs = append(userIDs, board.CreatedBy)
				} else {
					log.Println("Error loading board:", err)
				}
			}
		}

		notificationUserMap[n.NotificationID] = userIDs
		notificationBoardTypeMap[n.NotificationID] = boardType

		log.Printf("Notification %d (boardType: %s) → UserIDs: %v\n", n.NotificationID, boardType, userIDs)
	}

	// รวม userID ทั้งหมดที่ต้องส่ง notification
	userIDSet := make(map[int]struct{})
	for _, uids := range notificationUserMap {
		for _, uid := range uids {
			userIDSet[uid] = struct{}{}
		}
	}

	var users []model.User
	userIDs := make([]int, 0, len(userIDSet))
	for uid := range userIDSet {
		userIDs = append(userIDs, uid)
	}

	if err := db.Where("user_id IN ?", userIDs).Find(&users).Error; err != nil {
		c.JSON(500, gin.H{"error": "Failed to load user emails", "details": err.Error()})
		return
	}

	userEmailMap := make(map[int]string)
	for _, u := range users {
		userEmailMap[u.UserID] = u.Email
	}

	ctx := context.Background()
	userFCMMap := make(map[int]string)

	// ดึง FCM token จาก Firestore
	for userID, email := range userEmailMap {
		doc, err := firestoreClient.Collection("usersLogin").Doc(email).Get(ctx)
		if err != nil {
			log.Printf("Firestore error for %s: %v\n", email, err)
			continue
		}
		tokenRaw, err := doc.DataAt("FMCToken")
		if err != nil {
			log.Printf("Missing FMCToken for %s\n", email)
			continue
		}
		if token, ok := tokenRaw.(string); ok && token != "" {
			userFCMMap[userID] = token
			log.Printf("✅ Loaded token for userID %d: %s", userID, token)
		} else {
			log.Printf("❌ Invalid or missing token for userID %d (email: %s)", userID, email)
		}
	}

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

	// ส่ง notification ตามประเภท board
	for _, n := range notifications {
		userIDs := notificationUserMap[n.NotificationID]
		boardType := notificationBoardTypeMap[n.NotificationID]
		message := buildNotificationMessage(n, now)

		var tokens []string
		for _, uid := range userIDs {
			if token, ok := userFCMMap[uid]; ok {
				tokens = append(tokens, token)
			}
		}

		if len(tokens) == 0 {
			log.Printf("No valid FCM tokens for Notification %d", n.NotificationID)
			continue
		}

		data := map[string]string{
			"task_id": strconv.Itoa(n.TaskID),
			"noti_id": strconv.Itoa(n.NotificationID),
		}

		if boardType == BoardGroup {
			// ส่ง multicast notification
			err := sendMulticastNotification(app, tokens, "แจ้งเตือนงาน", message, data)
			if err != nil {
				log.Printf("❌ Failed multicast for Noti %d: %v\n", n.NotificationID, err)
			}
		} else {
			// ส่งทีละ token แบบ concurrent
			var wg sync.WaitGroup
			for _, token := range tokens {
				wg.Add(1)
				go func(t string, notiID int) {
					defer wg.Done()
					err := sendPushNotification(app, t, "แจ้งเตือนงาน", message, data)
					if err != nil {
						log.Printf("❌ Failed single for Noti %d token %s: %v\n", notiID, t, err)
					}
				}(token, n.NotificationID)
			}
			wg.Wait()
		}
	}

	c.JSON(200, gin.H{
		"message":               "Remind task notification triggered",
		"before_due_count":      len(beforeDueList),
		"due_now_or_late_count": len(dueList),
		"notified_users":        notificationUserMap,
	})
}

// buildNotificationMessage สร้างข้อความแจ้งเตือนจาก notification และเวลาปัจจุบัน
func buildNotificationMessage(noti model.Notification, now time.Time) string {
	taskName := noti.Task.TaskName
	if noti.BeforeDueDate != nil && now.After(*noti.BeforeDueDate) {
		return fmt.Sprintf("⏰ ใกล้ถึงเวลา: %s", taskName)
	} else if now.After(noti.DueDate) {
		return fmt.Sprintf("📌 ถึงกำหนดแล้ว: %s", taskName)
	}
	return ""
}

// SendSingleNotification ส่ง notification device เดียว (ใช้กับ API)
func SendSingleNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req NotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := godotenv.Load(); err != nil {
		log.Println("Warning: No .env file found or failed to load")
	}

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

	err = sendPushNotification(app, req.Token, req.Title, req.Body, req.Data)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to send notification: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "Notification sent successfully"})
}

// SendMulticastNotification ส่ง notification หลาย devices (ใช้กับ API)
func SendMulticastNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req MulticastRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := godotenv.Load(); err != nil {
		log.Println("Warning: No .env file found or failed to load")
	}

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

	err = sendMulticastNotification(app, req.Tokens, req.Title, req.Body, req.Data)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to send multicast notification: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "Multicast notification sent successfully"})
}

// initializeFirebaseApp เริ่มต้น Firebase app
func initializeFirebaseApp(serviceAccountKeyPath string) (*firebase.App, error) {
	ctx := context.Background()
	opt := option.WithCredentialsFile(serviceAccountKeyPath)
	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		return nil, fmt.Errorf("error initializing app: %v", err)
	}
	return app, nil
}

// sendPushNotification ส่ง notification device เดียว
func sendPushNotification(app *firebase.App, token, title, body string, data map[string]string) error {
	ctx := context.Background()

	client, err := app.Messaging(ctx)
	if err != nil {
		return fmt.Errorf("error getting Messaging client: %v", err)
	}

	message := &messaging.Message{
		Data: data,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Token: token,
	}

	response, err := client.Send(ctx, message)
	if err != nil {
		return fmt.Errorf("error sending message: %v", err)
	}

	log.Printf("Successfully sent message: %s", response)
	return nil
}

// sendMulticastNotification ส่ง notification หลาย devices
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

// NotificationRequest โครงสร้างข้อมูลสำหรับส่ง notification device เดียว
type NotificationRequest struct {
	Token    string            `json:"token" binding:"required"`
	Title    string            `json:"title" binding:"required"`
	Body     string            `json:"body" binding:"required"`
	Data     map[string]string `json:"data,omitempty"`
	ImageURL string            `json:"image_url,omitempty"`
}

// MulticastRequest โครงสร้างข้อมูลสำหรับส่ง notification หลาย devices
type MulticastRequest struct {
	Tokens   []string          `json:"tokens" binding:"required"`
	Title    string            `json:"title" binding:"required"`
	Body     string            `json:"body" binding:"required"`
	Data     map[string]string `json:"data,omitempty"`
	ImageURL string            `json:"image_url,omitempty"`
}
