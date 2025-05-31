package assigned

import (
	"context"
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

func AssignedController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/assigned", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		AssignedTask(c, db, firestoreClient)
	})
}

func AssignedTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.AssignedTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Convert string IDs to integers
	taskID, err := strconv.Atoi(req.TaskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	assignedUserID, err := strconv.Atoi(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	// Get user email for Firebase path
	var user model.User
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	// Create assigned record
	assigned := model.Assigned{
		TaskID:   taskID,
		UserID:   assignedUserID,
		AssignAt: time.Now(),
	}

	// Save to SQL database
	if err := db.Create(&assigned).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create assignment"})
		return
	}

	// Get the generated AssID
	assID := assigned.AssID

	// Prepare Firebase document data
	firestoreData := map[string]interface{}{
		"assID":      assID,
		"taskID":     taskID,
		"userID":     assignedUserID,
		"assignedAt": assigned.AssignAt,
	}

	// Create Firebase document path
	docPath := "Boards/" + user.Email + "/Boards/" + req.BoardID + "/Tasks/" + req.TaskID + "/Assigned/" + strconv.Itoa(assID)

	// Save to Firebase
	ctx := context.Background()
	_, err = firestoreClient.Doc(docPath).Set(ctx, firestoreData)
	if err != nil {
		// Log the error but don't fail the request since SQL save was successful
		// You might want to implement a retry mechanism or queue for failed Firebase writes
		c.JSON(http.StatusPartialContent, gin.H{
			"message":     "Assignment created in database but failed to sync with Firebase",
			"assigned_id": assID,
			"error":       err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "Assigned user successfully",
		"assigned_id": assID,
	})
}
