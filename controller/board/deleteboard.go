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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func DeleteBoardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.DELETE("/board", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		DeleteBoard(c, db, firestoreClient)
	})
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

// DeleteResult represents the result of a board deletion operation
type DeleteResult struct {
	Status string // "success", "unauthorized", "not_found", "error"
	Error  string
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

	// 3. ตรวจสอบว่าเป็น group board หรือไม่ ก่อนลบข้อมูล
	var boardUserCount int64
	if err := db.Model(&model.BoardUser{}).Where("board_id = ?", boardID).Count(&boardUserCount).Error; err != nil {
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to check board users: %v", err)}
	}
	isGroupBoard := boardUserCount > 0

	// 4. เริ่ม transaction เพื่อลบข้อมูลใน SQL ก่อน
	tx := db.Begin()
	if tx.Error != nil {
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to start transaction: %v", tx.Error)}
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 5. ลบ board หลัก (CASCADE จะลบ related records อัตโนมัติ)
	if err := tx.Delete(&board).Error; err != nil {
		tx.Rollback()
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to delete board: %v", err)}
	}

	// 6. Commit transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return DeleteResult{Status: "error", Error: fmt.Sprintf("Failed to commit transaction: %v", err)}
	}

	// 7. ลบจาก Firestore หลังจากลบ SQL สำเร็จแล้ว (ถ้าเป็น group board)
	if isGroupBoard {
		if err := deleteFromFirestore(firestoreClient, ctx, boardID); err != nil {
			// Log error แต่ไม่ return error เพราะ SQL deletion สำเร็จแล้ว
			fmt.Printf("WARNING: Board %d deleted from SQL but failed to delete from Firestore: %v\n", boardID, err)
			// Optional: เก็บ log นี้ไว้เพื่อ manual cleanup ภายหลัง
		}
	}

	return DeleteResult{Status: "success", Error: ""}
}

// deleteFromFirestore ลบข้อมูลจาก Firestore
func deleteFromFirestore(firestoreClient *firestore.Client, ctx context.Context, boardID int) error {
	boardDoc := firestoreClient.Collection("Boards").Doc(strconv.Itoa(boardID))

	// 1. ตรวจสอบว่า document มีอยู่หรือไม่
	docSnapshot, err := boardDoc.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			fmt.Printf("Board %d not found in Firestore (already deleted or never existed)\n", boardID)
			return nil // ไม่ถือเป็น error เพราะ document ไม่มีอยู่แล้ว
		}
		return fmt.Errorf("failed to check board existence in Firestore: %v", err)
	}

	if !docSnapshot.Exists() {
		fmt.Printf("Board %d document does not exist in Firestore\n", boardID)
		return nil
	}

	// 2. ลบ subcollections ก่อน (Tasks)
	tasksCollection := boardDoc.Collection("Tasks")

	// ใช้ batch delete สำหรับ subcollections
	batch := firestoreClient.Batch()
	taskDocs, err := tasksCollection.Documents(ctx).GetAll()
	if err != nil {
		fmt.Printf("Warning: Failed to get tasks for board %d: %v\n", boardID, err)
	} else {
		fmt.Printf("Found %d tasks to delete for board %d\n", len(taskDocs), boardID)
		for _, taskDoc := range taskDocs {
			batch.Delete(taskDoc.Ref)
		}
	}

	// 3. เพิ่มการลบ main document ลงใน batch
	batch.Delete(boardDoc)

	// 4. Commit batch
	_, err = batch.Commit(ctx)
	if err != nil {
		return fmt.Errorf("failed to commit Firestore batch deletion: %v", err)
	}

	fmt.Printf("Successfully deleted board %d from Firestore\n", boardID)
	return nil
}
