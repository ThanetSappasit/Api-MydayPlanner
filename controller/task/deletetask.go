package task

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func DeleteTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.DELETE("/deltask", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		DeleteTask(c, db, firestoreClient)
	})
	router.DELETE("/deltask/:taskid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		DeleteSingleTask(c, db, firestoreClient)
	})
}

func DeleteTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)

	var req dto.DeletetaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}
	taskIDs := req.TaskID
	if len(taskIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No task IDs provided"})
		return
	}

	// ดึง email ของ user
	var userEmail string
	if err := db.Table("user").
		Select("email").
		Where("user_id = ?", userID).
		Scan(&userEmail).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user email"})
		return
	}

	// ดึงข้อมูล tasks
	var existingTasks []struct {
		TaskID   int  `db:"task_id"`
		BoardID  *int `db:"board_id"`
		CreateBy *int `db:"create_by"`
	}
	if err := db.Table("tasks").
		Select("task_id, board_id, create_by").
		Where("task_id IN ?", taskIDs).
		Find(&existingTasks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch tasks"})
		return
	}

	// ตรวจสอบว่าพบ tasks ครบหรือไม่
	if len(existingTasks) != len(taskIDs) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Some tasks not found"})
		return
	}

	// แยก tasks ตามประเภท
	var deletableTasks []int
	var unauthorizedTasks []int
	taskMeta := make(map[int]string) // taskID -> Type ("today","private","group")
	taskBoardID := make(map[int]int) // taskID -> boardID (ถ้ามี)

	for _, t := range existingTasks {
		if t.BoardID == nil {
			if t.CreateBy != nil && *t.CreateBy == int(userID) {
				deletableTasks = append(deletableTasks, t.TaskID)
				taskMeta[t.TaskID] = "today"
			} else {
				unauthorizedTasks = append(unauthorizedTasks, t.TaskID)
			}
		} else {
			// ตรวจสอบ Board Owner / Board Member
			var count int64
			query := `
				SELECT COUNT(1) FROM (
					SELECT 1 FROM board WHERE board_id = ? AND create_by = ?
					UNION
					SELECT 1 FROM board_user WHERE board_id = ? AND user_id = ?
				) AS authorized
			`
			if err := db.Raw(query, *t.BoardID, userID, *t.BoardID, userID).Count(&count).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board access"})
				return
			}
			if count > 0 {
				// ตรวจสอบว่าเป็น Group หรือ Private
				var memberCount int64
				if err := db.Table("board_user").
					Where("board_id = ?", *t.BoardID).
					Count(&memberCount).Error; err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check board members"})
					return
				}
				taskType := "private"
				if memberCount > 0 {
					taskType = "group"
				}
				deletableTasks = append(deletableTasks, t.TaskID)
				taskMeta[t.TaskID] = taskType
				taskBoardID[t.TaskID] = *t.BoardID
			} else {
				unauthorizedTasks = append(unauthorizedTasks, t.TaskID)
			}
		}
	}

	if len(unauthorizedTasks) > 0 {
		c.JSON(http.StatusForbidden, gin.H{
			"error":             "Access denied for some tasks",
			"unauthorizedTasks": unauthorizedTasks,
		})
		return
	}
	if len(deletableTasks) == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "No tasks can be deleted"})
		return
	}

	// ค้นหา Notifications
	var relatedNotifications []struct {
		NotificationID int `db:"notification_id"`
		TaskID         int `db:"task_id"`
	}
	if err := db.Table("notification").
		Select("notification_id, task_id").
		Where("task_id IN ?", deletableTasks).
		Find(&relatedNotifications).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch notifications"})
		return
	}

	// Transaction ลบใน Database
	if err := db.Transaction(func(tx *gorm.DB) error {
		if len(relatedNotifications) > 0 {
			var notifIDs []int
			for _, n := range relatedNotifications {
				notifIDs = append(notifIDs, n.NotificationID)
			}
			if err := tx.Where("notification_id IN ?", notifIDs).
				Delete(&model.Notification{}).Error; err != nil {
				return fmt.Errorf("failed to delete notifications: %w", err)
			}
		}
		res := tx.Where("task_id IN ?", deletableTasks).
			Delete(&model.Tasks{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != int64(len(deletableTasks)) {
			return fmt.Errorf("Expected to delete %d tasks, but deleted %d", len(deletableTasks), res.RowsAffected)
		}
		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to delete tasks and notifications",
			"details": err.Error(),
		})
		return
	}

	// Firestore ลบ Notifications และ Tasks ตามประเภท
	ctxTimeout := 10 * time.Second
	for _, taskID := range deletableTasks {
		taskType := taskMeta[taskID]
		boardID := taskBoardID[taskID]

		// ลบ Notifications
		for _, notif := range relatedNotifications {
			if notif.TaskID == taskID {
				if taskType == "today" || taskType == "private" {
					// Personal notification
					notifDoc := firestoreClient.Collection("Notifications").Doc(userEmail).
						Collection("Tasks").Doc(fmt.Sprintf("%d", notif.NotificationID))
					ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
					_, err := notifDoc.Delete(ctx)
					cancel()
					if err != nil {
						fmt.Printf("Failed to delete personal notification %d: %v\n", notif.NotificationID, err)
					}
				}
			}
		}

		// ลบ Tasks
		if taskType == "group" {
			// ลบ Subcollections
			subCollections := []string{"Notifications", "Assigned", "Attachments", "Checklist"}
			for _, sub := range subCollections {
				subColRef := firestoreClient.Collection("BoardTasks").
					Doc(fmt.Sprintf("%d", taskID)).Collection(sub)
				ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
				docs, err := subColRef.Documents(ctx).GetAll()
				cancel()
				if err != nil {
					fmt.Printf("Failed to list %s for task %d: %v\n", sub, taskID, err)
					continue
				}
				for _, doc := range docs {
					ctxDel, cancelDel := context.WithTimeout(context.Background(), ctxTimeout)
					_, err := doc.Ref.Delete(ctxDel)
					cancelDel()
					if err != nil {
						fmt.Printf("Failed to delete %s/%s: %v\n", sub, doc.Ref.ID, err)
					}
				}
			}
			// ลบ BoardTasks/{taskID}
			ctxBT, cancelBT := context.WithTimeout(context.Background(), ctxTimeout)
			_, errBT := firestoreClient.Collection("BoardTasks").
				Doc(fmt.Sprintf("%d", taskID)).Delete(ctxBT)
			cancelBT()
			if errBT != nil {
				fmt.Printf("Failed to delete BoardTasks/%d: %v\n", taskID, errBT)
			}
			// ลบ Boards/{boardID}/Tasks/{taskID}
			ctxBoard, cancelBoard := context.WithTimeout(context.Background(), ctxTimeout)
			_, errBoard := firestoreClient.Collection("Boards").
				Doc(fmt.Sprintf("%d", boardID)).Collection("Tasks").
				Doc(fmt.Sprintf("%d", taskID)).Delete(ctxBoard)
			cancelBoard()
			if errBoard != nil {
				fmt.Printf("Failed to delete Boards/%d/Tasks/%d: %v\n", boardID, taskID, errBoard)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":              fmt.Sprintf("%d tasks deleted successfully", len(deletableTasks)),
		"deletedCount":         len(deletableTasks),
		"deletedTasks":         deletableTasks,
		"deletedNotifications": len(relatedNotifications),
	})
}

// DeleteSingleTask - ฟังก์ชันสำหรับลบงานเดี่ยว
func DeleteSingleTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)

	// รับ task_id จาก URL
	taskIDStr := c.Param("taskid")
	if taskIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Task ID is required"})
		return
	}

	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	// ดึง email ของผู้ใช้
	var userEmail string
	if err := db.Table("user").Select("email").Where("user_id = ?", userID).Scan(&userEmail).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user email"})
		return
	}

	// ดึงข้อมูล Task
	var task struct {
		TaskID   int  `db:"task_id"`
		BoardID  *int `db:"board_id"`
		CreateBy *int `db:"create_by"`
	}
	if err := db.Table("tasks").
		Select("task_id, board_id, create_by").
		Where("task_id = ?", taskID).
		First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch task"})
		}
		return
	}

	// ตรวจสอบสิทธิ์ + ประเภท Task
	isToday := task.BoardID == nil
	isPrivate := false
	isGroup := false
	canDelete := false

	if isToday {
		if task.CreateBy != nil && *task.CreateBy == int(userID) {
			canDelete = true
		}
	} else {
		// ตรวจสอบว่าเป็นเจ้าของ board หรือสมาชิก
		var authorizedCount int64
		query := `
			SELECT COUNT(1) FROM (
				SELECT 1 FROM board WHERE board_id = ? AND create_by = ?
				UNION
				SELECT 1 FROM board_user WHERE board_id = ? AND user_id = ?
			) AS authorized
		`
		if err := db.Raw(query, *task.BoardID, userID, *task.BoardID, userID).Count(&authorizedCount).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board access"})
			return
		}
		if authorizedCount > 0 {
			canDelete = true
		}

		// ตรวจสอบว่า task เป็น group หรือ private
		var boardUserCount int64
		if err := db.Table("board_user").
			Where("board_id = ?", *task.BoardID).
			Count(&boardUserCount).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board_user membership"})
			return
		}
		if boardUserCount > 0 {
			isGroup = true
		} else {
			isPrivate = true
		}
	}

	if !canDelete {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// ค้นหา Notifications
	var relatedNotifications []struct {
		NotificationID int `db:"notification_id"`
		TaskID         int `db:"task_id"`
	}
	if err := db.Table("notification").
		Select("notification_id, task_id").
		Where("task_id = ?", taskID).
		Find(&relatedNotifications).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch related notifications"})
		return
	}

	// Transaction: ลบ Notifications + Task
	err = db.Transaction(func(tx *gorm.DB) error {
		if len(relatedNotifications) > 0 {
			var notifIDs []int
			for _, n := range relatedNotifications {
				notifIDs = append(notifIDs, n.NotificationID)
			}
			if err := tx.Where("notification_id IN ?", notifIDs).
				Delete(&model.Notification{}).Error; err != nil {
				return fmt.Errorf("failed to delete notifications: %w", err)
			}
		}
		res := tx.Where("task_id = ?", taskID).Delete(&model.Tasks{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("task not found or already deleted")
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to delete task and notifications",
			"details": err.Error(),
		})
		return
	}

	ctxTimeout := 10 * time.Second

	// Firestore ลบ Notifications: Today และ Private เท่านั้น
	if (isToday || isPrivate) && len(relatedNotifications) > 0 {
		for _, notification := range relatedNotifications {
			docRef := firestoreClient.Collection("Notifications").Doc(userEmail).
				Collection("Tasks").Doc(fmt.Sprintf("%d", notification.NotificationID))
			ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
			_, err := docRef.Delete(ctx)
			cancel()
			if err != nil {
				fmt.Printf("Firestore delete notification %d failed: %v\n", notification.NotificationID, err)
			}
		}
	}

	// Firestore ลบ Group Task (ถ้ามี)
	if isGroup {
		// ลบ Subcollections
		subCollections := []string{"Notifications", "Assigned", "Attachments", "Checklist"}
		for _, subCol := range subCollections {
			subColRef := firestoreClient.Collection("BoardTasks").Doc(fmt.Sprintf("%d", taskID)).Collection(subCol)
			ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
			docs, err := subColRef.Documents(ctx).GetAll()
			cancel()
			if err != nil {
				fmt.Printf("Failed to list documents in %s: %v\n", subCol, err)
				continue
			}
			for _, doc := range docs {
				ctxDel, cancelDel := context.WithTimeout(context.Background(), ctxTimeout)
				_, errDel := doc.Ref.Delete(ctxDel)
				cancelDel()
				if errDel != nil {
					fmt.Printf("Failed to delete %s/%s: %v\n", subCol, doc.Ref.ID, errDel)
				}
			}
		}

		// ลบ BoardTasks/{taskID}
		ctxBT, cancelBT := context.WithTimeout(context.Background(), ctxTimeout)
		_, errBT := firestoreClient.Collection("BoardTasks").Doc(fmt.Sprintf("%d", taskID)).Delete(ctxBT)
		cancelBT()
		if errBT != nil {
			fmt.Printf("Failed to delete BoardTasks/%d: %v\n", taskID, errBT)
		}

		// ลบ Boards/{boardID}/Tasks/{taskID}
		ctxBoard, cancelBoard := context.WithTimeout(context.Background(), ctxTimeout)
		_, errBoard := firestoreClient.Collection("Boards").Doc(fmt.Sprintf("%d", *task.BoardID)).
			Collection("Tasks").Doc(fmt.Sprintf("%d", taskID)).Delete(ctxBoard)
		cancelBoard()
		if errBoard != nil {
			fmt.Printf("Failed to delete Boards/%d/Tasks/%d: %v\n", *task.BoardID, taskID, errBoard)
		}
	}

	// สร้าง response
	c.JSON(http.StatusOK, gin.H{
		"message":              "Task deleted successfully",
		"deletedTaskId":        taskID,
		"deletedNotifications": len(relatedNotifications),
	})
}
