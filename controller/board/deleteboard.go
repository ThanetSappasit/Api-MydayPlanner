package board

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

func DeleteBoardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.DELETE("/board", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		DeleteBoard(c, db, firestoreClient)
	})

}

type ErrorResponse struct {
	Error   string            `json:"error"`
	Details map[string]string `json:"details,omitempty"`
}

func DeleteBoard(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	var boardIDreq dto.DeleteBoardRequest
	if err := c.ShouldBindJSON(&boardIDreq); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Details: map[string]string{"validation": err.Error()},
		})
		return
	}

	var groupBoardIDs []int   // Board IDs ที่มีใน BoardUser (Group Board)
	var privateBoardIDs []int // Board IDs ที่ไม่มีใน BoardUser (Private Board)

	// วนลูปตรวจสอบแต่ละ Board ID
	for _, boardIDStr := range boardIDreq.BoardID {
		// แปลง string เป็น int
		boardID, err := strconv.Atoi(boardIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "Invalid board ID format",
				Details: map[string]string{"board_id": boardIDStr},
			})
			return
		}

		// ตรวจสอบว่า Board ID นี้มีใน BoardUser หรือไม่
		var count int64
		if err := db.Model(&model.BoardUser{}).Where("board_id = ?", boardID).Count(&count).Error; err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error:   "Database error",
				Details: map[string]string{"error": err.Error()},
			})
			return
		}

		// แยกประเภท Board ตามผลการตรวจสอบ
		if count > 0 {
			// มีข้อมูลใน BoardUser = Group Board
			groupBoardIDs = append(groupBoardIDs, boardID)
		} else {
			// ไม่มีข้อมูลใน BoardUser = Private Board
			privateBoardIDs = append(privateBoardIDs, boardID)
		}
	}

	// เรียกใช้ฟังก์ชันตามประเภท Board
	if len(groupBoardIDs) > 0 {
		if err := deleteGroupBoard(db, firestoreClient, groupBoardIDs); err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error:   "Failed to delete group boards",
				Details: map[string]string{"error": err.Error()},
			})
			return
		}
	}

	if len(privateBoardIDs) > 0 {
		if err := deletePrivateBoard(db, firestoreClient, privateBoardIDs, userID); err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error:   "Failed to delete private boards",
				Details: map[string]string{"error": err.Error()},
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":                "Boards deleted successfully",
		"deleted_group_boards":   len(groupBoardIDs),
		"deleted_private_boards": len(privateBoardIDs),
	})
}

func deleteGroupBoard(db *gorm.DB, firestoreClient *firestore.Client, boardIDs []int) error {
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()
	for _, boardID := range boardIDs {
		// Delete task กับ subtask
		tasks, err := queryTaskByBoardID(db, boardID)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if err := deleteSubCollectionByTaskID(db, firestoreClient, task.TaskID); err != nil {
				return err
			}
		}
		// ลบ boarduser
		if err := deleteMainPathByBoardID(db, firestoreClient, boardID); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to delete main path for board %d: %w", boardID, err)
		}

		// ลบ board ออกจาก sql
		if err := tx.Where("board_id = ?", boardID).Delete(&model.Board{}).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to delete board %d: %w", boardID, err)
		}

		if err := tx.Commit().Error; err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
	}
	return nil
}

func deletePrivateBoard(db *gorm.DB, firestoreClient *firestore.Client, boardIDs []int, userid uint) error {
	var user model.User
	if err := db.First(&user, userid).Error; err != nil {
		return err
	}
	email := user.Email
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	for _, boardID := range boardIDs {
		// Delete tasks associated with the board
		tasks, err := queryTaskByBoardID(db, boardID)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if err := deleteNotificationsByTaskID(db, firestoreClient, task.TaskID, email); err != nil {
				return err
			}
		}
		if err := tx.Where("board_id = ? AND create_by = ?", boardID, userid).Delete(&model.Board{}).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to delete board %d: %w", boardID, err)
		}

		if err := tx.Commit().Error; err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
	}
	return nil
}

func queryTaskByBoardID(db *gorm.DB, boardID int) ([]model.Tasks, error) {
	var tasks []model.Tasks
	if err := db.Where("board_id = ?", boardID).Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func deleteNotificationsByTaskID(db *gorm.DB, fb *firestore.Client, taskID int, email string) error {
	var notifications []model.Notification
	if err := db.Where("task_id = ?", taskID).Find(&notifications).Error; err != nil {
		return fmt.Errorf("failed to find notifications: %w", err)
	}

	if len(notifications) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()
	batch := fb.Batch()

	// Add all deletions to batch
	for _, notification := range notifications {
		notificationIDStr := strconv.Itoa(notification.NotificationID)
		firestorePath := fmt.Sprintf("Notifications/%s/Tasks/%s", email, notificationIDStr)
		docRef := fb.Doc(firestorePath)
		batch.Delete(docRef)
	}

	// Execute Firestore batch operation
	if _, err := batch.Commit(ctx); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete notifications from Firestore: %w", err)
	}

	// Commit database transaction
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit database transaction: %w", err)
	}

	return nil
}

func deleteSubCollectionByTaskID(db *gorm.DB, fb *firestore.Client, taskID int) error {
	ctx := context.Background()

	// Delete Notifications
	var notifications []model.Notification
	if err := db.Where("task_id = ?", taskID).Find(&notifications).Error; err != nil {
		return fmt.Errorf("failed to find notifications: %w", err)
	}
	for _, notification := range notifications {
		docPath := fmt.Sprintf("BoardTasks/%d/Notifications/%d", taskID, notification.NotificationID) // หรือ notification.NotificationID ขึ้นอยู่กับ field name
		if _, err := fb.Doc(docPath).Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete notification from Firestore: %w", err)
		}
	}

	// Delete Assigned
	var assigned []model.Assigned
	if err := db.Where("task_id = ?", taskID).Find(&assigned).Error; err != nil {
		return fmt.Errorf("failed to find assigned: %w", err)
	}
	for _, ass := range assigned {
		docPath := fmt.Sprintf("BoardTasks/%d/Assigned/%d", taskID, ass.AssID) // หรือ ass.AssID
		if _, err := fb.Doc(docPath).Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete assigned from Firestore: %w", err)
		}
	}

	// Delete Attachments
	var attachments []model.Attachment
	if err := db.Where("tasks_id = ?", taskID).Find(&attachments).Error; err != nil {
		return fmt.Errorf("failed to find attachments: %w", err)
	}
	for _, attachment := range attachments {
		docPath := fmt.Sprintf("BoardTasks/%d/Attachments/%d", taskID, attachment.AttachmentID) // หรือ attachment.AttachmentID
		if _, err := fb.Doc(docPath).Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete attachment from Firestore: %w", err)
		}
	}

	// Delete Checklist
	var checklists []model.Checklist
	if err := db.Where("task_id = ?", taskID).Find(&checklists).Error; err != nil {
		return fmt.Errorf("failed to find checklists: %w", err)
	}
	for _, checklist := range checklists {
		docPath := fmt.Sprintf("BoardTasks/%d/Checklist/%d", taskID, checklist.ChecklistID) // หรือ checklist.ChecklistID
		if _, err := fb.Doc(docPath).Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete checklist from Firestore: %w", err)
		}
	}

	docPath := fmt.Sprintf("BoardTasks/%d", taskID)
	if _, err := fb.Doc(docPath).Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete boardtask from Firestore: %w", err)
	}

	return nil
}

func queryBoarduserByBoardID(db *gorm.DB, boardID int) ([]model.BoardUser, error) {
	var boardUsers []model.BoardUser
	if err := db.Where("board_id = ?", boardID).Find(&boardUsers).Error; err != nil {
		return nil, fmt.Errorf("failed to query board users: %w", err)
	}
	return boardUsers, nil
}

func deleteMainPathByBoardID(db *gorm.DB, fb *firestore.Client, boardID int) error {
	ctx := context.Background()
	// หาBoarduser
	boardUsers, err := queryBoarduserByBoardID(db, boardID)
	if err != nil {
		return fmt.Errorf("failed to get board users for board %d: %w", boardID, err)
	}
	// ลบBoarduser
	for _, bu := range boardUsers {
		docPath := fmt.Sprintf("Boards/%d/BoardUsers/%d", boardID, bu.BoardUserID) // หรือ notification.NotificationID ขึ้นอยู่กับ field name
		if _, err := fb.Doc(docPath).Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete notification from Firestore: %w", err)
		}
	}
	taskid, err := queryTaskByBoardID(db, boardID)
	if err != nil {
		return fmt.Errorf("failed to get board users for board %d: %w", boardID, err)
	}
	for _, task := range taskid {
		docPath := fmt.Sprintf("Boards/%d/Tasks/%d", boardID, task.TaskID) // หรือ notification.NotificationID ขึ้นอยู่กับ field name
		if _, err := fb.Doc(docPath).Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete notification from Firestore: %w", err)
		}
	}
	// ลบmainboard
	docPath := fmt.Sprintf("Boards/%d", boardID)
	if _, err := fb.Doc(docPath).Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete main path for board %d: %w", boardID, err)
	}
	return nil
}
