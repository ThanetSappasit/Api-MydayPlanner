package notification

import (
	"context"
	"fmt"
	"log"
	"os"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"google.golang.org/api/option"
	"gorm.io/gorm"
)

// NotificationRequest โครงสร้างข้อมูลสำหรับ request
type NotificationRequest struct {
	Token    string            `json:"token" binding:"required"`
	Title    string            `json:"title" binding:"required"`
	Body     string            `json:"body" binding:"required"`
	Data     map[string]string `json:"data,omitempty"`
	ImageURL string            `json:"image_url,omitempty"`
}

// MulticastRequest โครงสร้างข้อมูลสำหรับส่งหลาย token
type MulticastRequest struct {
	Tokens   []string          `json:"tokens" binding:"required"`
	Title    string            `json:"title" binding:"required"`
	Body     string            `json:"body" binding:"required"`
	Data     map[string]string `json:"data,omitempty"`
	ImageURL string            `json:"image_url,omitempty"`
}

func PushNotificationTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	// router.POST("/remindtask", func(c *gin.Context) {
	// 	FCMtask(c, db, firestoreClient)
	// })

	// เพิ่ม endpoints สำหรับส่ง notification
	router.POST("/send-notification", func(c *gin.Context) {
		SendSingleNotification(c, db, firestoreClient)
	})

	router.POST("/send-multicast", func(c *gin.Context) {
		SendMulticastNotification(c, db, firestoreClient)
	})
}

func FCMtask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Warning: No .env file found or failed to load")
	}

	serviceAccountKeyPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_1")
	if serviceAccountKeyPath == "" {
		c.JSON(500, gin.H{"error": "environment variable GOOGLE_APPLICATION_CREDENTIALS_1 is not set"})
		return
	}

	// สร้าง Firebase app
	app, err := initializeFirebaseApp(serviceAccountKeyPath)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to initialize Firebase app: " + err.Error()})
		return
	}

	// ตัวอย่างการส่ง notification
	token := "YOUR_DEVICE_TOKEN_HERE" // ควรได้มาจาก database หรือ request

	err = sendPushNotification(app, token, "Task Reminder", "You have a pending task!", nil)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to send notification: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "Notification sent successfully"})
}

// SendSingleNotification ส่ง notification ไปยัง device เดียว
func SendSingleNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
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
func SendMulticastNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
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

// sendPushNotification ส่ง notification ไปยัง device เดียว
func sendPushNotification(app *firebase.App, token, title, body string, data map[string]string) error {
	ctx := context.Background()

	client, err := app.Messaging(ctx)
	if err != nil {
		return fmt.Errorf("error getting Messaging client: %v", err)
	}

	// สร้าง message
	message := &messaging.Message{
		Data: data,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Token: token,
	}

	// ส่ง message
	response, err := client.Send(ctx, message)
	if err != nil {
		return fmt.Errorf("error sending message: %v", err)
	}

	log.Printf("Successfully sent message: %s", response)
	return nil
}

// sendMulticastNotification ส่ง notification ไปยังหลาย devices
func sendMulticastNotification(app *firebase.App, tokens []string, title, body string, data map[string]string) error {
	ctx := context.Background()

	client, err := app.Messaging(ctx)
	if err != nil {
		return fmt.Errorf("error getting Messaging client: %v", err)
	}

	// สร้าง multicast message
	message := &messaging.MulticastMessage{
		Data: data,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Tokens: tokens,
	}

	// ส่ง multicast message
	response, err := client.SendMulticast(ctx, message)
	if err != nil {
		return fmt.Errorf("error sending multicast message: %v", err)
	}

	log.Printf("Successfully sent multicast message. Success: %d, Failure: %d",
		response.SuccessCount, response.FailureCount)

	// แสดงรายละเอียดของ failures
	if response.FailureCount > 0 {
		for idx, resp := range response.Responses {
			if !resp.Success {
				log.Printf("Failed to send to token %s: %v", tokens[idx], resp.Error)
			}
		}
	}

	return nil
}

// sendNotificationWithImage ส่ง notification พร้อมรูปภาพ
func sendNotificationWithImage(app *firebase.App, token, title, body, imageURL string, data map[string]string) error {
	ctx := context.Background()

	client, err := app.Messaging(ctx)
	if err != nil {
		return fmt.Errorf("error getting Messaging client: %v", err)
	}

	// สร้าง message พร้อม image
	message := &messaging.Message{
		Data: data,
		Notification: &messaging.Notification{
			Title:    title,
			Body:     body,
			ImageURL: imageURL,
		},
		Token: token,
	}

	// ส่ง message
	response, err := client.Send(ctx, message)
	if err != nil {
		return fmt.Errorf("error sending message: %v", err)
	}

	log.Printf("Successfully sent message with image: %s", response)
	return nil
}

// sendToTopic ส่ง notification ไปยัง topic
func sendToTopic(app *firebase.App, topic, title, body string, data map[string]string) error {
	ctx := context.Background()

	client, err := app.Messaging(ctx)
	if err != nil {
		return fmt.Errorf("error getting Messaging client: %v", err)
	}

	// สร้าง message สำหรับ topic
	message := &messaging.Message{
		Data: data,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Topic: topic,
	}

	// ส่ง message
	response, err := client.Send(ctx, message)
	if err != nil {
		return fmt.Errorf("error sending message to topic: %v", err)
	}

	log.Printf("Successfully sent message to topic %s: %s", topic, response)
	return nil
}
