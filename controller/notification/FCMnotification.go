package notification

import (
	"fmt"
	"os"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

// // NotificationRequest โครงสร้างข้อมูลสำหรับ request
// type NotificationRequest struct {
// 	Token    string            `json:"token" binding:"required"`
// 	Title    string            `json:"title" binding:"required"`
// 	Body     string            `json:"body" binding:"required"`
// 	Data     map[string]string `json:"data,omitempty"`
// 	ImageURL string            `json:"image_url,omitempty"`
// }

// // MulticastRequest โครงสร้างข้อมูลสำหรับส่งหลาย token
// type MulticastRequest struct {
// 	Tokens   []string          `json:"tokens" binding:"required"`
// 	Title    string            `json:"title" binding:"required"`
// 	Body     string            `json:"body" binding:"required"`
// 	Data     map[string]string `json:"data,omitempty"`
// 	ImageURL string            `json:"image_url,omitempty"`
// }

// NotificationProcessor handles notification processing fmtic
type NotificationProcessor struct {
	db              *gorm.DB
	firestoreClient *firestore.Client
}

// // NotificationResult represents the result of sending a notification
// type NotificationResult struct {
// 	UserID           int    `json:"user_id"`
// 	Email            string `json:"email"`
// 	FCMToken         string `json:"fcm_token"`
// 	NotificationSent bool   `json:"notification_sent"`
// 	Error            string `json:"error,omitempty"`
// }

// // TaskNotificationData represents notification data for a task
// type TaskNotificationData struct {
// 	NotificationID    int                  `json:"notification_id"`
// 	TaskID            int                  `json:"task_id"`
// 	BoardID           int                  `json:"board_id"`
// 	TaskName          string               `json:"task_name"`
// 	DueDate           string               `json:"due_date"`
// 	BeforeDueDate     string               `json:"before_due_date,omitempty"`
// 	RecurringPattern  *string              `json:"recurring_pattern"`
// 	IsGroup           bool                 `json:"is_group"`
// 	TotalUsers        int                  `json:"total_users"`
// 	NotificationsSent int                  `json:"notifications_sent"`
// 	UserDetails       []NotificationResult `json:"user_details"`
// 	CurrentStatus     string               `json:"current_status,omitempty"`
// }

func RemindNotificationTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/single_notification", func(c *gin.Context) {
		SendPushSingleNotification(c, db, firestoreClient)
	})
	router.POST("/multi_notification", func(c *gin.Context) {
		SendPushMulticastNotification(c, db, firestoreClient)
	})
}

// SendSingleNotification ส่ง notification ไปยัง device เดียว
func SendPushSingleNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req NotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	err := godotenv.Load()
	if err != nil {
		fmt.Println("Warning: No .env file found or failed to load")
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

// SendMulticastNotification ส่ง notification ไปยังหลาย devices
func SendPushMulticastNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req MulticastRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	err := godotenv.Load()
	if err != nil {
		fmt.Println("Warning: No .env file found or failed to load")
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
