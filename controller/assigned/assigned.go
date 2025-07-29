package assigned

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

func AssignedController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/assigned", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		AddAssignedTask(c, db, firestoreClient)
	})
	router.DELETE("/assigned/:assignid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		DelAssignedTask(c, db, firestoreClient)
	})
}

func AddAssignedTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var assignedTask dto.AssignedTaskRequest
	if err := c.ShouldBindJSON(&assignedTask); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request data"})
		return
	}

	// Convert string IDs to integers
	taskID, err := strconv.Atoi(assignedTask.TaskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID format"})
		return
	}

	userID, err := strconv.Atoi(assignedTask.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID format"})
		return
	}

	// Validate that the task exists
	var task model.Tasks
	if err := db.Where("task_id = ?", taskID).First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate task"})
		}
		return
	}

	// Validate that the user exists
	var user model.User
	if err := db.Where("user_id = ?", userID).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate user"})
		}
		return
	}

	// Check if the assignment already exists
	var existingAssignment model.Assigned
	if err := db.Where("task_id = ? AND user_id = ?", taskID, userID).First(&existingAssignment).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Task is already assigned to this user"})
		return
	}

	// Create new assignment
	newAssignment := model.Assigned{
		TaskID:   taskID,
		UserID:   userID,
		AssignAt: time.Now(),
	}

	// Begin transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}

	// Insert into database
	if err := tx.Create(&newAssignment).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create assignment"})
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	// Add to Firebase
	if err := createFirebaseAssignment(firestoreClient, newAssignment, task, user); err != nil {
		// Log the error but don't fail the request since DB creation succeeded
		fmt.Printf("Warning: Failed to create Firebase assignment: %v\n", err)
	}

	// Return success response
	c.JSON(http.StatusCreated, gin.H{
		"message": "Task assigned successfully",
		"assignment": map[string]interface{}{
			"ass_id":    newAssignment.AssID,
			"task_id":   newAssignment.TaskID,
			"user_id":   newAssignment.UserID,
			"assign_at": newAssignment.AssignAt,
		},
	})
}

func createFirebaseAssignment(firestoreClient *firestore.Client, assignment model.Assigned, task model.Tasks, user model.User) error {
	ctx := context.Background()

	// Construct the Firebase path: /Assigned/{newAssignedID}
	docPath := fmt.Sprintf("BoardTasks/%d/Assigned/%d", task.TaskID, assignment.AssID)

	// Create Firebase document data
	firebaseData := map[string]interface{}{
		"assId":     assignment.AssID,
		"taskId":    assignment.TaskID,
		"userId":    assignment.UserID,
		"assignAt":  assignment.AssignAt,
		"createdAt": time.Now(),
		"taskName":  task.TaskName, // Include task name for reference
		"userName":  user.Name,     // Include user name for reference
		"userEmail": user.Email,    // Include user email for reference
		"update_at": time.Now(),    // Add update timestamp
	}

	// Create the document in Firebase
	_, err := firestoreClient.Doc(docPath).Set(ctx, firebaseData)
	if err != nil {
		return fmt.Errorf("failed to create Firebase document: %v", err)
	}

	return nil
}

func DelAssignedTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	assignIDStr := c.Param("assignid")
	assignID, err := strconv.Atoi(assignIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid assignment ID format"})
		return
	}

	// Find the assignment in the database
	var assignment model.Assigned
	if err := db.Where("ass_id = ?", assignID).First(&assignment).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Assignment not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch assignment"})
		}
		return
	}

	// Begin transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}

	// Delete from database
	if err := tx.Where("ass_id = ?", assignID).Delete(&model.Assigned{}).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete assignment"})
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	// Delete from Firebase
	if err := deleteFirebaseAssignment(firestoreClient, assignment); err != nil {
		// Log the error but don't fail the request since DB deletion succeeded
		fmt.Printf("Warning: Failed to delete Firebase assignment: %v\n", err)
	}

	// Return success response
	c.JSON(http.StatusOK, gin.H{
		"message": "Assignment deleted successfully",
		"deleted_assignment": map[string]interface{}{
			"ass_id":    assignment.AssID,
			"task_id":   assignment.TaskID,
			"user_id":   assignment.UserID,
			"assign_at": assignment.AssignAt,
		},
	})
}

func deleteFirebaseAssignment(firestoreClient *firestore.Client, assign model.Assigned) error {
	ctx := context.Background()

	// Construct the Firebase path: /Assigned/{assignID}
	docPath := fmt.Sprintf("BoardTasks/%d/Assigned/%d", assign.TaskID, assign.AssID)

	// Delete the document from Firebase
	_, err := firestoreClient.Doc(docPath).Delete(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete Firebase document: %v", err)
	}

	return nil
}
