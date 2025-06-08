package board

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
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

	// Delete each board
	for _, boardID := range dataID.BoardID {
		// First delete from SQL
		sqlDeleteErr := deleteBoardFromSQL(db, user.UserID, boardID)
		if sqlDeleteErr != nil {
			errors = append(errors, fmt.Sprintf("Failed to delete board %s from SQL: %v", boardID, sqlDeleteErr))
			continue // Skip Firebase deletion if SQL deletion fails
		}

		// Then delete from Firebase
		ctx := context.Background()
		firebaseDeleteErr := deleteBoardFromFirebase(ctx, firestoreClient, boardID)
		if firebaseDeleteErr != nil {
			errors = append(errors, fmt.Sprintf("Failed to delete board %s from Firebase: %v", boardID, firebaseDeleteErr))
			// Note: SQL deletion already succeeded, you might want to handle rollback here
		} else {
			deletedBoards = append(deletedBoards, boardID)
		}
	}

	if len(errors) > 0 {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":          "Some boards failed to delete",
			"details":        errors,
			"deleted_boards": deletedBoards,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "Boards deleted successfully",
		"deleted_boards": deletedBoards,
	})
}

// deleteBoardFromSQL deletes board data from SQL database
func deleteBoardFromSQL(db *gorm.DB, userID int, boardID string) error {
	// Delete board from SQL (adjust table name and column names as needed)
	result := db.Exec("DELETE FROM board WHERE create_by = ? AND board_id = ?", userID, boardID)
	if result.Error != nil {
		return fmt.Errorf("failed to delete from database: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("board not found or not owned by user")
	}

	return nil
}

// deleteBoardFromFirebase deletes board and its tasks from Firebase
func deleteBoardFromFirebase(ctx context.Context, client *firestore.Client, boardID string) error {
	// Reference to the board document
	boardRef := client.Collection("Boards").Doc(boardID)

	// First delete all tasks in the Tasks subcollection
	if err := deleteTasksCollection(ctx, client, boardRef); err != nil {
		return fmt.Errorf("failed to delete tasks: %w", err)
	}

	// Then delete the board document
	if _, err := boardRef.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete board document: %w", err)
	}

	return nil
}

// deleteTasksCollection deletes all task documents from the Tasks subcollection
func deleteTasksCollection(ctx context.Context, client *firestore.Client, boardRef *firestore.DocumentRef) error {
	tasksCollection := boardRef.Collection("Tasks")

	// Get all task documents
	taskDocs, err := tasksCollection.Documents(ctx).GetAll()
	if err != nil {
		return fmt.Errorf("failed to get task documents: %w", err)
	}

	// Delete documents in batches (Firestore batch limit is 500)
	batchSize := 500
	for i := 0; i < len(taskDocs); i += batchSize {
		end := i + batchSize
		if end > len(taskDocs) {
			end = len(taskDocs)
		}

		batch := client.Batch()
		for j := i; j < end; j++ {
			batch.Delete(taskDocs[j].Ref)
		}

		if _, err := batch.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit batch delete for tasks: %w", err)
		}
	}

	return nil
}
