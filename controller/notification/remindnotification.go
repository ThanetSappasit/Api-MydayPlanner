package notification

import (
	"errors"
	"mydayplanner/model"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func PushNotificationTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/remindtask", func(c *gin.Context) {
		RemindNotificationTask(c, db, firestoreClient)
	})
}

func GetBoardByTaskID(taskID int, db *gorm.DB) (boardID interface{}, isInBoardUser bool, err error) {
	// ค้นหา task เพื่อเอา board_id
	var task model.Tasks
	if err := db.First(&task, taskID).Error; err != nil {
		return nil, false, err
	}

	// ตรวจสอบว่า task มี board_id หรือไม่
	if task.BoardID == nil {
		// Task ไม่มี board_id แสดงว่าเป็น personal task (today task)
		return "today", false, nil
	}

	boardIDValue := *task.BoardID
	isInBoardUser = false

	// ตรวจสอบใน board_user ก่อน
	var boardUser model.BoardUser
	if err := db.Where("board_id = ?", boardIDValue).First(&boardUser).Error; err == nil {
		// พบใน board_user
		isInBoardUser = true
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		// เกิดข้อผิดพลาดอื่น ๆ (ไม่ใช่ record not found)
		return nil, false, err
	}

	// ถ้าไม่พบใน board_user ให้ตรวจสอบใน board
	if !isInBoardUser {
		var board model.Board
		if err := db.First(&board, boardIDValue).Error; err != nil {
			return nil, false, err
		}
	}

	return boardIDValue, isInBoardUser, nil
}

func RemindNotificationTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var notificationIDs []string
	currentTime := time.Now()

	// Query เฉพาะ notification_id ของ notifications ที่ยังไม่ส่ง (is_send = false) และมี due_date ในวันนี้
	if err := db.Model(&model.Notification{}).
		Where("is_send = ? AND due_date <= ?", false, currentTime).
		Pluck("notification_id", &notificationIDs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch notification IDs"})
		return
	}

	// ตรวจสอบว่ามีข้อมูลหรือไม่
	if len(notificationIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message":          "No notification IDs found for today",
			"count":            0,
			"date":             currentTime.Format("2006-01-02"),
			"notification_ids": []string{},
		})
		return
	}

	// สำหรับแต่ละ notification ให้ดึง task_id และหา board_id
	var results []map[string]interface{}
	for _, notificationID := range notificationIDs {
		// ดึง notification เพื่อเอา task_id
		var notification model.Notification
		if err := db.Where("notification_id = ?", notificationID).First(&notification).Error; err != nil {
			continue // ข้าม notification นี้หากมีปัญหา
		}

		// หา board_id จาก task_id
		boardID, isInBoardUser, err := GetBoardByTaskID(notification.TaskID, db)
		if err != nil {
			// หากมี error ในการ query
			results = append(results, map[string]interface{}{
				"notification_id":  notificationID,
				"task_id":          notification.TaskID,
				"board_id":         "today",
				"is_in_board_user": false,
				"error":            err.Error(),
			})
		} else {
			results = append(results, map[string]interface{}{
				"notification_id":  notificationID,
				"task_id":          notification.TaskID,
				"board_id":         boardID, // จะเป็น "today" หรือ board_id (int)
				"is_in_board_user": isInBoardUser,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Today's unsent notifications with board info retrieved successfully",
		"count":   len(notificationIDs),
		"date":    currentTime.Format("2006-01-02"),
		"results": results,
	})
}

// ตัวอย่างการใช้งานในฟังก์ชันเดิม
// func RemindNotification(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
// 	var notificationIDs []string
// 	currentTime := time.Now()

// 	// หาวันที่เริ่มต้นและสิ้นสุดของวันนี้
// 	startOfDay := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentTime.Location())
// 	endOfDay := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 23, 59, 59, 999999999, currentTime.Location())

// 	// Query เฉพาะ notification_id ของ notifications ที่ยังไม่ส่ง (is_send = false) และมี due_date ในวันนี้
// 	if err := db.Model(&model.Notification{}).
// 		Where("is_send = ? AND due_date >= ? AND due_date <= ?", false, startOfDay, endOfDay).
// 		Pluck("notification_id", &notificationIDs).Error; err != nil {
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch notification IDs"})
// 		return
// 	}

// 	// ตรวจสอบว่ามีข้อมูลหรือไม่
// 	if len(notificationIDs) == 0 {
// 		c.JSON(http.StatusOK, gin.H{
// 			"message":          "No notification IDs found for today",
// 			"count":            0,
// 			"date":             currentTime.Format("2006-01-02"),
// 			"notification_ids": []string{},
// 		})
// 		return
// 	}

// 	// สำหรับแต่ละ notification ให้ดึง task_id และหา board_id
// 	var results []map[string]interface{}
// 	for _, notificationID := range notificationIDs {
// 		// ดึง notification เพื่อเอา task_id
// 		var notification model.Notification
// 		if err := db.Where("notification_id = ?", notificationID).First(&notification).Error; err != nil {
// 			continue // ข้าม notification นี้หากมีปัญหา
// 		}

// 		// หา board_id จาก task_id
// 		boardID, isInBoardUser, err := GetBoardByTaskID(notification.TaskID, db)
// 		if err != nil {
// 			// หากไม่พบ board ให้ใส่ค่า default
// 			results = append(results, map[string]interface{}{
// 				"notification_id":  notificationID,
// 				"task_id":          notification.TaskID,
// 				"board_id":         nil,
// 				"is_in_board_user": false,
// 				"error":            err.Error(),
// 			})
// 			continue
// 		}

// 		// Query ข้อมูลจาก Firestore ตาม path ที่เหมาะสม
// 		var firestoreData map[string]interface{}
// 		var firestorePath string

// 		if isInBoardUser {
// 			// กรณี board_id เป็น true: /BoardTasks/taskid/Notifications/notificationid
// 			firestorePath = fmt.Sprintf("BoardTasks/%d/Notifications/%s", notification.TaskID, notificationID)
// 		} else {
// 			// กรณี board_id เป็น false: /Notifications/user.email/Tasks/notificationid
// 			// ต้องหา user email จาก task's create_by
// 			var task model.Tasks
// 			if err := db.Preload("Creator").First(&task, notification.TaskID).Error; err != nil {
// 				results = append(results, map[string]interface{}{
// 					"notification_id":  notificationID,
// 					"task_id":          notification.TaskID,
// 					"board_id":         boardID,
// 					"is_in_board_user": isInBoardUser,
// 					"error":            "Failed to fetch task creator",
// 				})
// 				continue
// 			}

// 			if task.Creator == nil {
// 				results = append(results, map[string]interface{}{
// 					"notification_id":  notificationID,
// 					"task_id":          notification.TaskID,
// 					"board_id":         boardID,
// 					"is_in_board_user": isInBoardUser,
// 					"error":            "Task creator not found",
// 				})
// 				continue
// 			}

// 			firestorePath = fmt.Sprintf("Notifications/%s/Tasks/%s", task.Creator.Email, notificationID)
// 		}

// 		// Query จาก Firestore
// 		doc, err := firestoreClient.Doc(firestorePath).Get(context.Background())
// 		if err != nil {
// 			results = append(results, map[string]interface{}{
// 				"notification_id":  notificationID,
// 				"task_id":          notification.TaskID,
// 				"board_id":         boardID,
// 				"is_in_board_user": isInBoardUser,
// 				"firestore_path":   firestorePath,
// 				"firestore_data":   nil,
// 				"firestore_error":  err.Error(),
// 			})
// 		} else {
// 			firestoreData = doc.Data()
// 			results = append(results, map[string]interface{}{
// 				"notification_id":  notificationID,
// 				"task_id":          notification.TaskID,
// 				"board_id":         boardID,
// 				"is_in_board_user": isInBoardUser,
// 				"firestore_path":   firestorePath,
// 				"firestore_data":   firestoreData,
// 			})
// 		}
// 	}

// 	c.JSON(http.StatusOK, gin.H{
// 		"message": "Today's unsent notifications with board and firestore data retrieved successfully",
// 		"count":   len(notificationIDs),
// 		"date":    currentTime.Format("2006-01-02"),
// 		"results": results,
// 	})
// }
