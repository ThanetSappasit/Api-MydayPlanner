package checklist

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

func ChecklistController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/checklist", middleware.AccessTokenMiddleware())
	{
		routes.POST("/create", func(c *gin.Context) {
			Checklist(c, db, firestoreClient)
		})
	}
}

func Checklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.CreateChecklistTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Get user email for Firebase path
	var user model.User
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	// Convert string IDs to integers
	taskID, err := strconv.Atoi(req.TaskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	// Create checklist record
	checklist := model.Checklist{
		TaskID:        taskID,
		ChecklistName: req.ChecklistName,
		CreateAt:      time.Now(),
	}

	// Save to SQL database
	if err := db.Create(&checklist).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create checklist"})
		return
	}

	// Get the generated ChecklistID
	checklistID := checklist.ChecklistID

	// Prepare Firebase document data
	firestoreData := map[string]interface{}{
		"ChecklistID":   checklistID,
		"TaskID":        taskID,
		"ChecklistName": checklist.ChecklistName,
		"CreatedAt":     checklist.CreateAt,
	}

	// Determine board type for path
	var boardType string
	if req.Isgroup == "1" {
		boardType = "Group_Boards"
	} else {
		boardType = "Private_Boards"
	}

	// Create Firebase document path using ChecklistID
	docPath := fmt.Sprintf("Boards/%s/%s/%s/tasks/%s/Checklists/%d",
		user.Email, boardType, req.BoardID, req.TaskID, checklistID)

	// Save to Firebase document
	ctx := context.Background()
	_, err = firestoreClient.Doc(docPath).Set(ctx, firestoreData)
	if err != nil {
		// Log the error but don't fail the request since SQL save was successful
		// You might want to implement a retry mechanism or queue for failed Firebase writes
		c.JSON(http.StatusPartialContent, gin.H{
			"message":      "Checklist created in database but failed to sync with Firebase",
			"checklist_id": checklistID,
			"error":        err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Checklist created successfully",
		"checklist_id": checklistID,
	})
}
