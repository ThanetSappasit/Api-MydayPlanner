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
		DeleteFirebaseBoard(c, db, firestoreClient)
	})
}

func DeleteFirebaseBoard(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// Get user ID from token and board ID from path parameter
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

	ctx := context.Background()
	var errors []string

	// Delete each board
	for _, boardID := range dataID.BoardID {
		if err := deleteBoardRecursively(ctx, firestoreClient, user.Email, boardID); err != nil {
			errors = append(errors, fmt.Sprintf("Failed to delete board %s: %v", boardID, err))
		}
	}

	if len(errors) > 0 {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Some boards failed to delete",
			"details": errors,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "Boards deleted successfully",
		"deleted_boards": dataID.BoardID,
	})
}

// deleteBoardRecursively deletes a board and all its subcollections
func deleteBoardRecursively(ctx context.Context, client *firestore.Client, userEmail, boardID string) error {
	// Reference to the board document
	boardRef := client.Collection("Boards").Doc(userEmail).Collection("Boards").Doc(boardID)

	// Delete all tasks and their subcollections first
	if err := deleteTasksRecursively(ctx, client, boardRef); err != nil {
		return fmt.Errorf("failed to delete tasks: %w", err)
	}

	// Delete any other subcollections at board level (add more as needed)
	boardSubcollections := []string{"members", "activities", "labels"} // Add other subcollections if exist
	for _, subcollection := range boardSubcollections {
		if err := deleteCollection(ctx, client, boardRef.Collection(subcollection)); err != nil {
			return fmt.Errorf("failed to delete board subcollection %s: %w", subcollection, err)
		}
	}

	// Finally delete the board document itself
	if _, err := boardRef.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete board document: %w", err)
	}

	return nil
}

// deleteTasksRecursively deletes all tasks and their subcollections
func deleteTasksRecursively(ctx context.Context, client *firestore.Client, boardRef *firestore.DocumentRef) error {
	tasksCollection := boardRef.Collection("tasks")

	// Get all task documents
	taskDocs, err := tasksCollection.Documents(ctx).GetAll()
	if err != nil {
		return fmt.Errorf("failed to get task documents: %w", err)
	}

	// Delete each task and its subcollections
	for _, taskDoc := range taskDocs {
		taskRef := taskDoc.Ref

		// Delete task subcollections
		taskSubcollections := []string{"Assigned", "Attachments", "Checklists"} // Add more if needed
		for _, subcollection := range taskSubcollections {
			if err := deleteCollection(ctx, client, taskRef.Collection(subcollection)); err != nil {
				return fmt.Errorf("failed to delete task subcollection %s: %w", subcollection, err)
			}
		}

		// Delete the task document
		if _, err := taskRef.Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete task document %s: %w", taskDoc.Ref.ID, err)
		}
	}

	return nil
}

// deleteCollection deletes all documents in a collection
func deleteCollection(ctx context.Context, client *firestore.Client, collectionRef *firestore.CollectionRef) error {
	// Get all documents in the collection
	docs, err := collectionRef.Documents(ctx).GetAll()
	if err != nil {
		return fmt.Errorf("failed to get documents: %w", err)
	}

	// Delete documents in batches (Firestore batch limit is 500)
	batchSize := 500
	for i := 0; i < len(docs); i += batchSize {
		end := i + batchSize
		if end > len(docs) {
			end = len(docs)
		}

		batch := client.Batch()
		for j := i; j < end; j++ {
			batch.Delete(docs[j].Ref)
		}

		if _, err := batch.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit batch delete: %w", err)
		}
	}

	return nil
}
