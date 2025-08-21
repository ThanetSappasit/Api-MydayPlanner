package notification

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"mydayplanner/services"
	"net/http"
	"os"
	"strconv"
	"time"

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
	router.PUT("/snoozeNotify/:taskid", func(c *gin.Context) {
		SnoozeNotification(c, db, firestoreClient)
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
	app, err := services.InitializeFirebaseApp(serviceAccountKeyPath)
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
	app, err := services.InitializeFirebaseApp(serviceAccountKeyPath)
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
	if err := services.SendMulticastNotification(app, fcmTokens, title, body, data); err != nil {
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
	body := fmt.Sprintf("งานที่คุณได้รับ: '%s' ถูกยกเลิกแล้ว", req.TaskName)
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

func SnoozeNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var taskid = c.Param("taskid")

	// แปลง taskid เป็น int
	taskID, err := strconv.Atoi(taskid)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid task ID",
			"message": "Task ID must be a number",
		})
		return
	}

	// ค้นหา task เพื่อเอา BoardID
	var task model.Tasks
	if err := db.Where("task_id = ?", taskID).First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "Task not found",
				"message": "Task with given ID does not exist",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Database error",
			"message": err.Error(),
		})
		return
	}

	// ตรวจสอบว่า task มี BoardID หรือไม่
	if task.BoardID == nil {
		// หาก BoardID เป็น null ให้ดำเนินการแบบ private
		snoozePrivate(c, db, firestoreClient, taskID)
		return
	}

	// ตรวจสอบว่า BoardID นี้มีในตาราง BoardUser หรือไม่
	var boardUser model.BoardUser
	err = db.Where("board_id = ?", *task.BoardID).First(&boardUser).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// ไม่พบใน BoardUser ให้ดำเนินการแบบ private
			snoozePrivate(c, db, firestoreClient, taskID)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Database error",
			"message": err.Error(),
		})
		return
	}

	// พบใน BoardUser ให้ดำเนินการแบบ group
	snoozeGroup(c, db, firestoreClient, taskID, *task.BoardID)
}

func snoozePrivate(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client, taskID int) {
	// ค้นหา task เพื่อเอา CreateBy
	var task model.Tasks
	if err := db.Where("task_id = ?", taskID).Preload("Creator").First(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Task not found",
			"message": err.Error(),
		})
		return
	}

	// ตรวจสอบว่ามี CreateBy หรือไม่
	if task.CreateBy == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Task creator not found",
			"message": "Cannot snooze notification without task creator",
		})
		return
	}

	// ดึง email ของผู้สร้าง task จาก user_id (task.CreateBy)
	var creator model.User
	if err := db.Where("user_id = ?", *task.CreateBy).First(&creator).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Task creator not found",
			"message": err.Error(),
		})
		return
	}
	task.Creator = &creator

	// ค้นหา notification ของ task นี้
	var notification model.Notification
	if err := db.Where("task_id = ?", taskID).First(&notification).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "Notification not found",
				"message": "No notification found for this task",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Database error",
			"message": err.Error(),
		})
		return
	}

	// คำนวณเวลา snooze (due_date + 10 นาที)
	var newSnooze *time.Time
	now := time.Now()
	if notification.Snooze != nil {
		snoozeTime := now.Add(10 * time.Minute)
		newSnooze = &snoozeTime
	} else if notification.DueDate != nil && notification.IsSend == "4" {
		// ถ้าไม่มี snooze → ใช้ due_date แล้วบวก 10 นาที
		snoozeTime := notification.DueDate.Add(10 * time.Minute)
		newSnooze = &snoozeTime
	}

	// อัปเดทข้อมูลในฐานข้อมูล - บันทึกใน snooze field
	if err := db.Model(&notification).Updates(map[string]interface{}{
		"snooze":  newSnooze,
		"is_send": "3", // รีเซ็ตสถานะการส่ง
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Update failed",
			"message": err.Error(),
		})
		return
	}

	// อัปเดท Firestore: /Notifications/{email}/Tasks/{notificationid}
	if task.Creator != nil && task.Creator.Email != "" {
		err := updatePrivateFirestore(firestoreClient, task.Creator.Email, notification.NotificationID, newSnooze)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"message": "Private task notification snoozed successfully (Firestore update failed)",
				"task_id": taskID,
				"snooze_time": func() string {
					if newSnooze != nil {
						return newSnooze.Format("2006-01-02 15:04:05")
					}
					return ""
				}(),
				"type":    "private",
				"warning": "Firestore update failed: " + err.Error(),
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Private task notification snoozed successfully",
		"task_id": taskID,
		"snooze_time": func() string {
			if newSnooze != nil {
				return newSnooze.Format("2006-01-02 15:04:05")
			}
			return ""
		}(),
		"type": "private",
	})
}

func snoozeGroup(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client, taskID int, boardID int) {
	// ค้นหา notification ของ task นี้
	var notification model.Notification
	if err := db.Where("task_id = ?", taskID).First(&notification).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "Notification not found",
				"message": "No notification found for this task",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Database error",
			"message": err.Error(),
		})
		return
	}

	// คำนวณเวลา snooze (due_date + 10 นาที)
	var newSnooze *time.Time
	if notification.Snooze != nil {
		// ถ้ามี snooze เดิม → เอามาบวก 10 นาที
		snoozeTime := notification.Snooze.Add(10 * time.Minute)
		newSnooze = &snoozeTime
	} else if notification.DueDate != nil {
		// ถ้าไม่มี snooze → ใช้ due_date แล้วบวก 10 นาที
		snoozeTime := notification.DueDate.Add(10 * time.Minute)
		newSnooze = &snoozeTime
	}

	// อัปเดทข้อมูลในฐานข้อมูล - บันทึกใน snooze field
	if err := db.Model(&notification).Updates(map[string]interface{}{
		"snooze":  newSnooze,
		"is_send": "3", // รีเซ็ตสถานะการส่ง
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Update failed",
			"message": err.Error(),
		})
		return
	}

	// อัปเดท Firestore: /BoardTasks/{taskid}/Notifications/{notificationid}
	err := updateGroupFirestore(firestoreClient, taskID, notification.NotificationID, newSnooze)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"message":  "Group task notification snoozed successfully (Firestore update failed)",
			"task_id":  taskID,
			"board_id": boardID,
			"snooze_time": func() string {
				if newSnooze != nil {
					return newSnooze.Format("2006-01-02 15:04:05")
				}
				return ""
			}(),
			"type":    "group",
			"warning": "Firestore update failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Group task notification snoozed successfully",
		"task_id":  taskID,
		"board_id": boardID,
		"snooze_time": func() string {
			if newSnooze != nil {
				return newSnooze.Format("2006-01-02 15:04:05")
			}
			return ""
		}(),
		"type": "group",
	})
}

// ฟังก์ชันอัปเดท Firestore สำหรับ Private Task
func updatePrivateFirestore(firestoreClient *firestore.Client, email string, notificationID int, newSnooze *time.Time) error {
	ctx := context.Background()

	// สร้าง document reference: /Notifications/{email}/Tasks/{notificationid}
	docRef := firestoreClient.Collection("Notifications").Doc(email).Collection("Tasks").Doc(strconv.Itoa(notificationID))

	// เตรียมข้อมูลที่จะอัปเดท
	updateData := map[string]interface{}{
		"isSend": "3",
	}

	// เพิ่ม snooze หากมี
	if newSnooze != nil {
		updateData["snooze"] = *newSnooze
	} else {
		updateData["snooze"] = nil
	}

	// อัปเดทข้อมูลใน Firestore
	_, err := docRef.Update(ctx, []firestore.Update{
		{Path: "snooze", Value: updateData["snooze"]},
		{Path: "isSend", Value: updateData["isSend"]},
	})

	return err
}

// ฟังก์ชันอัปเดท Firestore สำหรับ Group Task
func updateGroupFirestore(firestoreClient *firestore.Client, taskID int, notificationID int, newSnooze *time.Time) error {
	ctx := context.Background()

	// สร้าง document reference: /BoardTasks/{taskid}/Notifications/{notificationid}
	docRef := firestoreClient.Collection("BoardTasks").Doc(strconv.Itoa(taskID)).Collection("Notifications").Doc(strconv.Itoa(notificationID))

	// เตรียมข้อมูลที่จะอัปเดท
	updateData := map[string]interface{}{
		"isSend": "3",
	}

	// เพิ่ม snooze หากมี
	if newSnooze != nil {
		updateData["snooze"] = *newSnooze
	} else {
		updateData["snooze"] = nil
	}

	// อัปเดทข้อมูลใน Firestore
	_, err := docRef.Update(ctx, []firestore.Update{
		{Path: "snooze", Value: updateData["snooze"]},
		{Path: "isSend", Value: updateData["isSend"]},
	})

	return err
}
