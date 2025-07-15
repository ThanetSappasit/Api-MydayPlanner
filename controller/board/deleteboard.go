package board

import (
	"context"
	"fmt"
	"log"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func DeleteBoardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.DELETE("/board", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		DeleteBoard(c, db, firestoreClient)
	})
}

// DeleteResult represents the result of a board deletion operation
type DeleteResult struct {
	Status string // "success", "unauthorized", "not_found", "error"
	Error  string
}

func DeleteBoard(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// Get user ID from token and board ID from request body
	userID := c.MustGet("userId").(uint)
	var dataID dto.DeleteBoardRequest
	if err := c.ShouldBindJSON(&dataID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var user struct {
		UserID int
		Email  string
	}
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userID).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	var errors []string
	var deletedBoards []string
	var unauthorizedBoards []string
	var notFoundBoards []string

	// สร้าง context สำหรับ Firestore operations
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// Delete each board
	for _, boardIDStr := range dataID.BoardID {
		boardID, err := strconv.Atoi(boardIDStr)
		if err != nil {
			errors = append(errors, fmt.Sprintf("Invalid board ID format: %s", boardIDStr))
			continue
		}

		// ตรวจสอบสิทธิ์และลบ board
		deleteResult := deleteBoardWithPermissionCheck(db, firestoreClient, ctx, user.UserID, boardID)

		switch deleteResult.Status {
		case "success":
			deletedBoards = append(deletedBoards, boardIDStr)
		case "unauthorized":
			unauthorizedBoards = append(unauthorizedBoards, boardIDStr)
		case "not_found":
			notFoundBoards = append(notFoundBoards, boardIDStr)
		case "error":
			errors = append(errors, fmt.Sprintf("Board %s: %s", boardIDStr, deleteResult.Error))
		}
	}

	// สร้าง response
	response := gin.H{}

	if len(deletedBoards) > 0 {
		response["deleted_boards"] = deletedBoards
	}

	if len(unauthorizedBoards) > 0 {
		response["unauthorized_boards"] = unauthorizedBoards
		response["unauthorized_message"] = "You don't have permission to delete these boards"
	}

	if len(notFoundBoards) > 0 {
		response["not_found_boards"] = notFoundBoards
		response["not_found_message"] = "These boards were not found"
	}

	if len(errors) > 0 {
		response["errors"] = errors
	}

	// กำหนด HTTP status และ message
	if len(deletedBoards) == len(dataID.BoardID) {
		// ลบสำเร็จทั้งหมด
		response["message"] = "All boards deleted successfully"
		c.JSON(http.StatusOK, response)
	} else if len(deletedBoards) > 0 {
		// ลบสำเร็จบางส่วน
		response["message"] = "Some boards were deleted successfully"
		c.JSON(http.StatusPartialContent, response)
	} else {
		// ลบไม่สำเร็จเลย
		if len(unauthorizedBoards) > 0 {
			response["message"] = "No boards were deleted due to permission issues"
			c.JSON(http.StatusForbidden, response)
		} else if len(notFoundBoards) > 0 {
			response["message"] = "No boards were deleted - boards not found"
			c.JSON(http.StatusNotFound, response)
		} else {
			response["message"] = "No boards were deleted due to errors"
			c.JSON(http.StatusInternalServerError, response)
		}
	}
}

// deleteBoardWithPermissionCheck ตรวจสอบสิทธิ์และลบ board
func deleteBoardWithPermissionCheck(db *gorm.DB, firestoreClient *firestore.Client, ctx context.Context, userID int, boardID int) DeleteResult {
	// 1. ตรวจสอบว่า board มีอยู่และ user เป็นเจ้าของหรือไม่
	var board model.Board
	if err := db.Where("board_id = ?", boardID).First(&board).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return DeleteResult{Status: "not_found", Error: "Board not found"}
		}
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Database error: %v", err)}
	}

	// 2. ตรวจสอบสิทธิ์ - เฉพาะเจ้าของเท่านั้นที่ลบได้
	if board.CreatedBy != userID {
		return DeleteResult{Status: "unauthorized", Error: "You are not the owner of this board"}
	}

	// 3. ดึงข้อมูลที่จำเป็นสำหรับ Firestore ก่อนลบ SQL
	var boardUsers []model.BoardUser
	if err := db.Where("board_id = ?", boardID).Find(&boardUsers).Error; err != nil {
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to get board users: %v", err)}
	}

	var taskIDs []int
	if err := db.Raw("SELECT task_id FROM tasks WHERE board_id = ?", boardID).Scan(&taskIDs).Error; err != nil {
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to get tasks: %v", err)}
	}

	var notifications []struct {
		NotificationID int
		TaskID         int
	}
	if len(taskIDs) > 0 {
		if err := db.Raw(`
			SELECT notification_id, task_id
			FROM notifications
			WHERE task_id IN ?
		`, taskIDs).Scan(&notifications).Error; err != nil {
			return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to get notifications: %v", err)}
		}
	}

	var userEmail string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userID).Scan(&userEmail).Error; err != nil {
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to get user email: %v", err)}
	}

	isGroupBoard := len(boardUsers) > 0

	// 4. ลบจาก Firestore ก่อน (ขณะที่ข้อมูลใน SQL ยังคงอยู่)
	if err := deleteFromFirestore(firestoreClient, ctx, boardID, userID, userEmail, isGroupBoard, boardUsers, taskIDs, notifications); err != nil {
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to delete from Firestore: %v", err)}
	}

	// 5. เริ่ม transaction เพื่อลบข้อมูลใน SQL
	tx := db.Begin()
	if tx.Error != nil {
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to start transaction: %v", tx.Error)}
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 6. ลบ board หลัก (CASCADE จะลบ related records อัตโนมัติ)
	if err := tx.Delete(&board).Error; err != nil {
		tx.Rollback()
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to delete board: %v", err)}
	}

	// 7. Commit transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to commit transaction: %v", err)}
	}

	return DeleteResult{Status: "success", Error: ""}
}

// deleteFromFirestore ลบข้อมูลจาก Firestore
func deleteFromFirestore(
	firestoreClient *firestore.Client,
	ctx context.Context,
	boardID int,
	userID int,
	userEmail string,
	isGroup bool,
	boardUsers []model.BoardUser,
	taskIDs []int,
	notifications []struct {
		NotificationID int
		TaskID         int
	},
) error {
	boardDoc := firestoreClient.Collection("Boards").Doc(strconv.Itoa(boardID))

	docSnapshot, err := boardDoc.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			fmt.Printf("Board %d not found in Firestore (already deleted or never existed)\n", boardID)
			return nil
		}
		return fmt.Errorf("failed to check board existence in Firestore: %v", err)
	}

	if !docSnapshot.Exists() {
		fmt.Printf("Board %d document does not exist in Firestore\n", boardID)
		return nil
	}

	batch := firestoreClient.Batch()

	// ดึงเฉพาะ BoardUserID ออกมาเป็น []int
	var boarduserIDs []int
	for _, bu := range boardUsers {
		boarduserIDs = append(boarduserIDs, bu.BoardUserID)
	}

	log.Printf("📋 Found %d board_user_ids for board %d: %v\n", len(boarduserIDs), boardID, boarduserIDs)

	// ===== ลบ BoardUser =====
	for _, boardusersid := range boarduserIDs {
		boarduserDoc := boardDoc.Collection("BoardUsers").Doc(strconv.Itoa(boardusersid))
		batch.Delete(boarduserDoc)
	}

	fmt.Printf("Found %d tasks for board %d\n", len(taskIDs), boardID)

	// ===== ลบ Tasks ใน Firestore: /Boards/{boardID}/Tasks/{taskID} =====
	for _, taskID := range taskIDs {
		taskDoc := boardDoc.Collection("Tasks").Doc(strconv.Itoa(taskID))
		batch.Delete(taskDoc)
	}

	// ===== ลบ Notifications ตามโครงสร้างใหม่ =====
	if isGroup {
		for _, n := range notifications {
			notiDoc := firestoreClient.
				Collection("BoardTasks").
				Doc(strconv.Itoa(n.TaskID)).
				Collection("Notifications").
				Doc(strconv.Itoa(n.NotificationID))
			batch.Delete(notiDoc)
		}
	} else {
		for _, n := range notifications {
			notiDoc := firestoreClient.
				Collection("Notifications").
				Doc(userEmail).
				Collection("Tasks").
				Doc(strconv.Itoa(n.NotificationID))
			batch.Delete(notiDoc)
		}
	}

	// ===== ลบ Subcollections อื่น ๆ ที่อยู่ใต้ /BoardTasks/{taskID} =====
	subCollections := []string{"Assigned", "Attachments", "Checklist"}

	for _, taskID := range taskIDs {
		for _, sub := range subCollections {
			iter := firestoreClient.
				Collection("BoardTasks").
				Doc(strconv.Itoa(taskID)).
				Collection(sub).
				Documents(ctx)

			for {
				doc, err := iter.Next()
				if err == iterator.Done {
					break
				}
				if err != nil {
					log.Printf("Error reading subcollection '%s' for task %d: %v", sub, taskID, err)
					break
				}
				batch.Delete(doc.Ref)
			}
		}
	}

	// ===== ลบ document หลักของ board =====
	batch.Delete(boardDoc)

	// ===== Commit การลบทั้งหมด =====
	_, err = batch.Commit(ctx)
	if err != nil {
		return fmt.Errorf("failed to commit Firestore batch deletion: %v", err)
	}

	fmt.Printf("✅ Successfully deleted board %d and related data from Firestore\n", boardID)
	return nil
}
