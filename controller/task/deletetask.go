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

	// ตรวจสอบว่ามี task IDs หรือไม่
	if len(taskIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No task IDs provided"})
		return
	}

	// ดึง email ของ user จาก userID
	var userEmail string
	if err := db.Table("user").Select("email").Where("user_id = ?", userID).Scan(&userEmail).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user email"})
		return
	}

	// ดึงข้อมูล tasks พร้อม board_id ในคำสั่งเดียว
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

	// ตรวจสอบว่าพบ tasks ทั้งหมดที่ร้องขอหรือไม่
	if len(existingTasks) != len(taskIDs) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Some tasks not found"})
		return
	}

	// แยก tasks ออกเป็น 2 กลุ่ม: ที่มี board_id และที่ไม่มี
	var tasksWithBoard []int
	var tasksWithoutBoard []int
	var unauthorizedTasks []int

	for _, task := range existingTasks {
		if task.BoardID == nil {
			// Tasks ที่ไม่มี board_id - ตรวจสอบ create_by
			if task.CreateBy != nil && *task.CreateBy == int(userID) {
				tasksWithoutBoard = append(tasksWithoutBoard, task.TaskID)
			} else {
				unauthorizedTasks = append(unauthorizedTasks, task.TaskID)
			}
		} else {
			// Tasks ที่มี board_id - เก็บไว้ตรวจสอบสิทธิ์ board ทีหลัง
			tasksWithBoard = append(tasksWithBoard, task.TaskID)
		}
	}

	// ตรวจสอบสิทธิ์ในการเข้าถึง board สำหรับ tasks ที่มี board_id (ถ้ามี)
	if len(tasksWithBoard) > 0 {
		// ตรวจสอบว่า user เป็นผู้สร้าง board หรือไม่
		var authorizedTasksWithBoard []int
		query := `
			SELECT DISTINCT t.task_id
			FROM tasks t
			JOIN board b ON t.board_id = b.board_id
			WHERE t.task_id IN ? AND b.create_by = ?
		`

		if err := db.Raw(query, tasksWithBoard, userID).Scan(&authorizedTasksWithBoard).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board ownership"})
			return
		}

		// หา tasks ที่ไม่มีสิทธิ์เข้าถึง
		authorizedSet := make(map[int]bool)
		for _, taskID := range authorizedTasksWithBoard {
			authorizedSet[taskID] = true
		}

		for _, taskID := range tasksWithBoard {
			if !authorizedSet[taskID] {
				unauthorizedTasks = append(unauthorizedTasks, taskID)
			}
		}

		// อัพเดท tasksWithBoard ให้เหลือเฉพาะที่มีสิทธิ์
		tasksWithBoard = authorizedTasksWithBoard
	}

	// ตรวจสอบว่ามี tasks ที่ไม่มีสิทธิ์หรือไม่
	if len(unauthorizedTasks) > 0 {
		c.JSON(http.StatusForbidden, gin.H{
			"error":             "Access denied for some tasks",
			"unauthorizedTasks": unauthorizedTasks,
		})
		return
	}

	// รวม tasks ที่สามารถลบได้
	var deletableTasks []int
	deletableTasks = append(deletableTasks, tasksWithoutBoard...)
	deletableTasks = append(deletableTasks, tasksWithBoard...)

	// ตรวจสอบว่ามี tasks ที่สามารถลบได้หรือไม่
	if len(deletableTasks) == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "No tasks can be deleted"})
		return
	}

	// ค้นหา notifications ที่เกี่ยวข้องกับ tasks ที่จะลบ
	var relatedNotifications []struct {
		NotificationID int `db:"notification_id"`
		TaskID         int `db:"task_id"`
	}

	if err := db.Table("notification").
		Select("notification_id, task_id").
		Where("task_id IN ?", deletableTasks).
		Find(&relatedNotifications).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch related notifications"})
		return
	}

	// ลบ tasks และ notifications ด้วย Transaction
	err := db.Transaction(func(tx *gorm.DB) error {
		// ลบ notifications ก่อน (เพราะมี foreign key constraint)
		if len(relatedNotifications) > 0 {
			var notificationIDs []int
			for _, notif := range relatedNotifications {
				notificationIDs = append(notificationIDs, notif.NotificationID)
			}

			if err := tx.Where("notification_id IN ?", notificationIDs).Delete(&model.Notification{}).Error; err != nil {
				return fmt.Errorf("failed to delete notifications: %w", err)
			}
		}

		// ลบ tasks
		result := tx.Where("task_id IN ?", deletableTasks).Delete(&model.Tasks{})
		if result.Error != nil {
			return result.Error
		}

		// ตรวจสอบว่าลบได้จริงหรือไม่
		if result.RowsAffected != int64(len(deletableTasks)) {
			return fmt.Errorf("expected to delete %d tasks, but deleted %d", len(deletableTasks), result.RowsAffected)
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete tasks and notifications", "details": err.Error()})
		return
	}

	// ลบจาก Firestore - Tasks (เฉพาะ tasks ที่มี board_id)
	for _, taskID := range deletableTasks {
		// หา board_id ที่เกี่ยวข้องกับ task นี้
		var boardID int
		for _, task := range existingTasks {
			if task.TaskID == taskID && task.BoardID != nil {
				boardID = *task.BoardID
				break
			}
		}

		// ลบ task จาก Firestore เฉพาะ tasks ที่มี board_id
		if boardID != 0 {
			taskDocRef := firestoreClient.Collection("Boards").Doc(fmt.Sprintf("%d", boardID)).
				Collection("Tasks").Doc(fmt.Sprintf("%d", taskID))

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := taskDocRef.Delete(ctx)
			cancel()

			if err != nil {
				// fmt ความผิดพลาด แต่ไม่ต้อง return เพราะลบจาก DB แล้ว
				fmt.Printf("Failed to delete task %d from Firestore: %v", taskID, err)
			}
		}
	}

	// ลบ notifications จาก Firestore (ใช้ task_id เป็น document ID)
	if len(relatedNotifications) > 0 {
		for _, notification := range relatedNotifications {
			// ใช้ task_id เป็น document ID ตามที่ใช้ในการสร้าง
			notificationDocRef := firestoreClient.Collection("Notifications").Doc(userEmail).
				Collection("Tasks").Doc(fmt.Sprintf("%d", notification.NotificationID))

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := notificationDocRef.Delete(ctx)
			cancel()

			if err != nil {
				// fmt ความผิดพลาด แต่ไม่ต้อง return เพราะลบจาก DB แล้ว
				fmt.Printf("Failed to delete notification for task %d from Firestore: %v", notification.TaskID, err)
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

	// รับ task_id จาก URL parameter
	taskIDStr := c.Param("taskid")
	if taskIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Task ID is required"})
		return
	}

	// แปลง string ID เป็น int
	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	// ดึงข้อมูล task
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

	// ตรวจสอบสิทธิ์
	canDelete := false

	if task.BoardID == nil {
		// Task ไม่มี board_id - ตรวจสอบ create_by
		if task.CreateBy != nil && *task.CreateBy == int(userID) {
			canDelete = true
		}
	} else {
		// Task มี board_id - ตรวจสอบสิทธิ์ผ่าน board_user
		var count int64
		query := `
			SELECT COUNT(1)
			FROM tasks t
			JOIN board_user bu ON t.board_id = bu.board_id
			WHERE t.task_id = ? AND bu.user_id = ?
		`

		if err := db.Raw(query, taskID, userID).Count(&count).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board access"})
			return
		}

		if count > 0 {
			canDelete = true
		}
	}

	if !canDelete {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// ลบ task ด้วย Transaction
	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("task_id = ?", taskID).Delete(&model.Tasks{})
		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			return fmt.Errorf("task not found or already deleted")
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete task"})
		return
	}

	if task.BoardID != nil {
		docRef := firestoreClient.Collection("Boards").Doc(fmt.Sprintf("%d", *task.BoardID)).
			Collection("Tasks").Doc(fmt.Sprintf("%d", taskID))
		_, err := docRef.Delete(c)
		if err != nil {
			fmt.Printf("Failed to delete task %d from Firestore: %v", taskID, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Task deleted successfully",
		"deletedTaskId": taskID,
	})
}
