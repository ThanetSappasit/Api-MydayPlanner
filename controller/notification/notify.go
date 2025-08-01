package notification

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"mydayplanner/services"
	"os"
	"strconv"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

func RemindNotificationTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/inviteboardNotify", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		InviteBoardNotify(c, db, firestoreClient)
	})
	router.POST("/acceptinviteboardNotify/:boardid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		AcceptInviteNotify(c, db, firestoreClient)
	})
	router.POST("/assignedtaskNotify", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		AssignedTaskNotify(c, db, firestoreClient)
	})
	router.POST("/unassignedtaskNotify", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		UnAssignedTaskNotify(c, db, firestoreClient)
	})
}

func InviteBoardNotify(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req dto.InviteNotify
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid input"})
		return
	}

	// Query board information using req.BoardID
	var board model.Board
	if err := db.Where("board_id = ?", req.BoardID).First(&board).Error; err != nil {
		c.JSON(404, gin.H{"error": "Board not found"})
		return
	}

	// Get FCM token from Firestore
	doc, err := firestoreClient.Collection("usersLogin").Doc(req.RecieveEmail).Get(context.Background())
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to get user token from Firestore: " + err.Error()})
		return
	}

	// Check if document exists
	if !doc.Exists() {
		c.JSON(404, gin.H{"error": "User login data not found"})
		return
	}

	// Extract FCM token
	data := doc.Data()
	fcmTokenInterface, exists := data["FMCToken"]
	if !exists {
		c.JSON(404, gin.H{"error": "FCM token not found for user"})
		return
	}

	fcmToken, ok := fcmTokenInterface.(string)
	if !ok || fcmToken == "" {
		c.JSON(400, gin.H{"error": "Invalid or empty FCM token"})
		return
	}

	// Load environment variables (consider moving this to initialization)
	err = godotenv.Load()
	if err != nil {
		fmt.Println("Warning: No .env file found or failed to load")
	}

	serviceAccountKeyPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_1")
	if serviceAccountKeyPath == "" {
		c.JSON(500, gin.H{"error": "Firebase credentials not configured"})
		return
	}

	// Initialize Firebase app
	app, err := initializeFirebaseApp(serviceAccountKeyPath)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to initialize Firebase app: " + err.Error()})
		return
	}

	// Send push notification
	title := "คำเชิญเข้าร่วมบอร์ดงาน"
	body := fmt.Sprintf("คุณได้รับคำเชิญเข้าร่วมบอร์ดงาน: %s", board.BoardName)
	datasend := map[string]string{
		"payload": "notification",
	}

	err = sendPushNotification(app, fcmToken, title, body, datasend)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to send notification: " + err.Error()})
		return
	}

	updateFirestoreInviteNotification(firestoreClient, req.RecieveEmail, req.SendingEmail, req.BoardID)

	c.JSON(200, gin.H{
		"message": "Notification sent successfully",
	})
}

func AcceptInviteNotify(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	boardID := c.Param("boardid")

	user, err := services.GetUserdata(db, fmt.Sprintf("%d", userId))
	if err != nil {
		c.JSON(404, gin.H{
			"error": "User not found",
		})
		return
	}

	var board model.Board
	if err := db.Where("board_id = ?", boardID).First(&board).Error; err != nil {
		c.JSON(404, gin.H{
			"error": "Board not found",
		})
		return
	}

	// 1. ค้นหา UserID ทุกคนใน BoardUser จาก BoardID
	var boardUsers []model.BoardUser
	if err := db.Where("board_id = ?", boardID).Find(&boardUsers).Error; err != nil {
		c.JSON(500, gin.H{
			"error": "Failed to fetch board users",
		})
		return
	}

	// 2. เก็บ UserID ทั้งหมด
	var userIDs []int
	for _, boardUser := range boardUsers {
		// ตัดคนที่เข้าร่วม (userId) ออกจากรายชื่อผู้รับ notification
		if boardUser.UserID != int(userId) {
			userIDs = append(userIDs, boardUser.UserID)
		}
	}

	if len(userIDs) == 0 {
		c.JSON(200, gin.H{
			"message": "No users found in this board",
		})
		return
	}

	// 3. ค้นหา Email จาก User model
	var users []model.User
	if err := db.Where("user_id IN ?", userIDs).Find(&users).Error; err != nil {
		c.JSON(500, gin.H{
			"error": "Failed to fetch users",
		})
		return
	}

	// 4. ดึง FCM Token จาก Firestore
	ctx := context.Background()
	var fcmTokens []string

	for _, user := range users {
		// เข้าถึง document ใน path /usersLogin/{email}
		doc, err := firestoreClient.Collection("usersLogin").Doc(user.Email).Get(ctx)
		if err != nil {
			fmt.Printf("Error getting document for email %s: %v", user.Email, err)
			continue
		}

		// ดึงข้อมูล FMC_Token
		data := doc.Data()
		if fmcToken, exists := data["FMCToken"]; exists {
			if tokenStr, ok := fmcToken.(string); ok && tokenStr != "" {
				fcmTokens = append(fcmTokens, tokenStr)
			}
		}
	}

	if len(fcmTokens) == 0 {
		c.JSON(200, gin.H{
			"message": "No FCM tokens found",
		})
		return
	}

	// 5. สร้าง Firebase App (สมมติว่ามี app instance อยู่แล้ว)
	// Load environment variables (consider moving this to initialization)
	err = godotenv.Load()
	if err != nil {
		fmt.Println("Warning: No .env file found or failed to load")
	}

	serviceAccountKeyPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_1")
	if serviceAccountKeyPath == "" {
		c.JSON(500, gin.H{"error": "Firebase credentials not configured"})
		return
	}

	// Initialize Firebase app
	app, err := initializeFirebaseApp(serviceAccountKeyPath)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to initialize Firebase app: " + err.Error()})
		return
	}

	// 6. ส่ง notification
	title := "เข้าร่วมกลุ่มงานแล้ว"
	body := fmt.Sprintf("ผู้ใช้ %s เข้าร่วมกลุ่มงาน %s แล้ว", user.Name, board.BoardName)
	data := map[string]string{
		"payload": "notification",
	}

	// หมายเหตุ: คุณต้องส่ง firebase.App เป็น parameter หรือสร้างที่นี่
	if err := sendMulticastNotification(app, fcmTokens, title, body, data); err != nil {
		fmt.Printf("Error sending notification: %v", err)
		c.JSON(500, gin.H{
			"error": "Failed to send notification",
		})
		return
	}

	c.JSON(200, gin.H{
		"message":      "Notification sent successfully",
		"tokens_count": len(fcmTokens),
	})
}

func AssignedTaskNotify(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// userID := c.MustGet("userId").(uint)
	var req dto.AssignedNotify
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid input"})
		return
	}

	// Query task information using req.TaskID
	task, err := services.GetTaskData(db, req.TaskID)
	if err != nil {
		c.JSON(404, gin.H{"error": "Task not found"})
		return
	}

	recieveUSER, err := services.GetUserdata(db, req.RecieveID)
	if err != nil {
		c.JSON(404, gin.H{"error": "User not found"})
		return
	}

	// Get FCM token from Firestore
	fcmToken, err := services.GetFMCTokenData(firestoreClient, recieveUSER.Email)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// สร้าง Firebase app
	app, err := services.GetFirebaseApp()
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to initialize Firebase app: " + err.Error()})
		return
	}

	// Send push notification
	title := "งานที่ได้รับมอบหมาย"
	body := fmt.Sprintf("คุณได้รับมอบหมายงาน: %s", task.TaskName)
	datasend := map[string]string{
		"payload": "notification",
	}

	err = sendPushNotification(app, fcmToken, title, body, datasend)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to send notification: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"message": "Notification sent successfully",
	})
}

func UnAssignedTaskNotify(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req dto.UnAssignedNotify
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid input"})
		return
	}

	var assign model.Assigned
	if err := db.Where("ass_id = ?", req.AssignID).First(&assign).Error; err != nil {
		c.JSON(404, gin.H{"error": "Assignment not found"})
		return
	}
	// Query task information using req.TaskID
	task, err := services.GetTaskData(db, strconv.Itoa(assign.TaskID))
	if err != nil {
		c.JSON(404, gin.H{"error": "Task not found"})
		return
	}

	recieveUSER, err := services.GetUserdata(db, req.RecieveID)
	if err != nil {
		c.JSON(404, gin.H{"error": "User not found"})
		return
	}

	// Get FCM token from Firestore
	fcmToken, err := services.GetFMCTokenData(firestoreClient, recieveUSER.Email)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// Initialize Firebase app
	app, err := services.GetFirebaseApp()
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to initialize Firebase app: " + err.Error()})
		return
	}

	// Send push notification
	title := "ยกเลิกการมอบหมายงาน"
	body := fmt.Sprintf("งานที่คุณได้รับ: '%s' ถูกยกเลิกแล้ว", task.TaskName)
	datasend := map[string]string{
		"payload": "notification",
	}

	err = sendPushNotification(app, fcmToken, title, body, datasend)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to send notification: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"message": "Notification sent successfully",
	})
}

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

	fmt.Printf("Successfully sent message: %s", response)
	return nil
}

func updateFirestoreInviteNotification(firestoreClient *firestore.Client, RecieveEmail string, Sendingemail string, boardid string) {
	ctx := context.Background()
	var docPath string

	docname := fmt.Sprintf("%sfrom-%s", boardid, Sendingemail)
	docPath = fmt.Sprintf("Notifications/%s/InviteJoin/%s", RecieveEmail, docname)

	updateData := map[string]interface{}{
		"notiCount": false,
		"updatedAt": firestore.ServerTimestamp,
	}

	_, err := firestoreClient.Doc(docPath).Set(ctx, updateData, firestore.MergeAll)
	if err != nil {
		fmt.Printf("Error updating Firestore document: %v\n", err)
	} else {
		fmt.Println("Firestore document updated successfully")
	}
}
