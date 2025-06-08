package checklist

import (
	"context"
	"errors"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

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
	boardID := req.BoardID

	// Prepare Firebase document data
	firestoreData := map[string]interface{}{
		"ChecklistID":   checklistID,
		"BoardID":       boardID,
		"TaskID":        taskID,
		"ChecklistName": checklist.ChecklistName,
		"CreatedAt":     checklist.CreateAt,
		"Archived":      false,
	}

	// Save to Firebase document
	ctx := context.Background()
	_, err = firestoreClient.Collection("Checklists").Doc(strconv.Itoa(checklistID)).Set(ctx, firestoreData)
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

func UpdateChecklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)

	var adjustData dto.AdjustChecklistRequest
	if err := c.ShouldBindJSON(&adjustData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Convert string IDs to integers
	taskIDInt, err := strconv.Atoi(adjustData.TaskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid TaskID format"})
		return
	}

	checklistIDInt, err := strconv.Atoi(adjustData.ChecklistID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ChecklistID format"})
		return
	}

	// Validate checklist name
	if strings.TrimSpace(adjustData.ChecklistName) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ChecklistName cannot be empty"})
		return
	}

	// Get user email
	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userID).Scan(&email).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		}
		return
	}

	ctx := c.Request.Context()

	// Get current checklist data
	var currentChecklist model.Checklist
	if err := db.Where("checklist_id = ? AND task_id = ?", checklistIDInt, taskIDInt).First(&currentChecklist).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Checklist not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get checklist data"})
		}
		return
	}

	// Get task to find board information
	var task model.Tasks
	if err := db.Where("task_id = ?", taskIDInt).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task data"})
		}
		return
	}

	// Check if there are any changes
	trimmedName := strings.TrimSpace(adjustData.ChecklistName)
	if trimmedName == currentChecklist.ChecklistName {
		c.JSON(http.StatusOK, gin.H{
			"message":      "No changes detected",
			"checklist_id": checklistIDInt,
		})
		return
	}

	// Prepare update data
	updateData := map[string]interface{}{
		"checklist_name": trimmedName,
	}

	// Prepare Firestore updates
	firestoreUpdates := []firestore.Update{
		{
			Path:  "ChecklistName",
			Value: trimmedName,
		},
	}

	// Start database transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database transaction error"})
		return
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Update in SQL Database
	result := tx.Model(&model.Checklist{}).Where("checklist_id = ? AND task_id = ?", checklistIDInt, taskIDInt).Updates(updateData)
	if result.Error != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to update checklist in database: %v", result.Error),
		})
		return
	}

	// Check if any rows were actually updated
	if result.RowsAffected == 0 {
		tx.Rollback()
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Checklist not found or no changes made",
		})
		return
	}

	// Update in Firestore asynchronously
	errChan := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					errChan <- err
				} else {
					errChan <- fmt.Errorf("panic in Firestore operation: %v", r)
				}
			}
		}()

		checklistDocRef := firestoreClient.
			Collection("Checklists").
			Doc(strconv.Itoa(checklistIDInt))

		_, err := checklistDocRef.Update(ctx, firestoreUpdates)
		errChan <- err
	}()

	// Wait for Firestore result with timeout
	select {
	case firestoreErr := <-errChan:
		if firestoreErr != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to update checklist in Firestore: %v", firestoreErr),
			})
			return
		}
	case <-time.After(10 * time.Second):
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Firestore update timeout",
		})
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to commit transaction: %v", err),
		})
		return
	}

	// Get updated checklist data for response
	var updatedChecklist model.Checklist
	if err := db.Where("checklist_id = ? AND task_id = ?", checklistIDInt, taskIDInt).First(&updatedChecklist).Error; err != nil {
		// Log error but still return success since update was successful
		fmt.Printf("Warning: Could not fetch updated checklist data: %v\n", err)
	}

	// Prepare response data
	responseData := gin.H{
		"message":      "Checklist updated successfully",
		"checklist_id": checklistIDInt,
	}

	c.JSON(http.StatusOK, responseData)
}
