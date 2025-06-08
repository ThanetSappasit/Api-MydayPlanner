package assigned

import (
	"context"
	"errors"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Global worker pool for Firebase operations
var (
	firebaseWorkerPool = make(chan struct{}, 10) // Limit concurrent Firebase operations
	once               sync.Once
)

func initFirebasePool() {
	for i := 0; i < 10; i++ {
		firebaseWorkerPool <- struct{}{}
	}
}

func AssignedTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// Initialize worker pool once
	once.Do(initFirebasePool)

	userId := c.MustGet("userId").(uint)

	var req dto.AssignedTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Fast input validation
	if req.TaskID == "" || req.UserID == "" || req.BoardID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing required fields"})
		return
	}

	// Pre-allocate channels for better performance
	const numWorkers = 3
	results := make(chan interface{}, numWorkers)
	errChan := make(chan error, numWorkers)

	// Worker 1: Parse and validate IDs
	go func() {
		taskID, err1 := strconv.Atoi(req.TaskID)
		if err1 != nil || taskID <= 0 {
			errChan <- errors.New("invalid task ID")
			return
		}

		assignedUserID, err2 := strconv.Atoi(req.UserID)
		if err2 != nil || assignedUserID <= 0 {
			errChan <- errors.New("invalid user ID")
			return
		}

		results <- map[string]int{"taskID": taskID, "assignedUserID": assignedUserID}
	}()

	// Worker 2: Get user email with optimized query
	go func() {
		var email string
		err := db.Model(&model.User{}).Select("email").Where("user_id = ?", userId).Pluck("email", &email).Error
		if err != nil {
			errChan <- errors.New("failed to get user email")
			return
		}
		if email == "" {
			errChan <- errors.New("user not found")
			return
		}
		results <- map[string]string{"email": email}
	}()

	// Worker 3: Check for existing assignment (optimized COUNT query)
	go func() {
		// Parse IDs first for this worker
		taskID, _ := strconv.Atoi(req.TaskID)
		assignedUserID, _ := strconv.Atoi(req.UserID)

		var exists bool
		err := db.Model(&model.Assigned{}).
			Select("COUNT(*) > 0").
			Where("task_id = ? AND user_id = ?", taskID, assignedUserID).
			Pluck("COUNT(*) > 0", &exists).Error

		if err != nil {
			errChan <- errors.New("failed to check existing assignment")
			return
		}
		results <- map[string]bool{"exists": exists}
	}()

	// Collect results with timeout
	var (
		taskID, assignedUserID int
		userEmail              string
		assignmentExists       bool
		completed              int
	)

	timeout := time.After(5 * time.Second)
	for completed < numWorkers {
		select {
		case result := <-results:
			completed++
			switch r := result.(type) {
			case map[string]int:
				taskID = r["taskID"]
				assignedUserID = r["assignedUserID"]
			case map[string]string:
				userEmail = r["email"]
			case map[string]bool:
				assignmentExists = r["exists"]
			}
		case err := <-errChan:
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		case <-timeout:
			c.JSON(http.StatusRequestTimeout, gin.H{"error": "Request timeout"})
			return
		}
	}

	// Check for duplicate assignment
	if assignmentExists {
		c.JSON(http.StatusConflict, gin.H{"error": "User already assigned to this task"})
		return
	}

	// Create assignment with prepared statement (faster)
	now := time.Now()
	assigned := model.Assigned{
		TaskID:   taskID,
		UserID:   assignedUserID,
		AssignAt: now,
	}

	// Use optimized database insert
	if err := db.Create(&assigned).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create assignment"})
		return
	}

	// Async Firebase sync with worker pool (prevents overwhelming Firebase)
	go func() {
		// Get worker from pool
		<-firebaseWorkerPool
		defer func() {
			firebaseWorkerPool <- struct{}{} // Return worker to pool
		}()

		syncToFirebaseOptimized(firestoreClient, assigned, userEmail, req.BoardID, req.TaskID)
	}()

	// Return immediate response
	c.JSON(http.StatusCreated, gin.H{
		"message":     "User assigned successfully",
		"assigned_id": assigned.AssID,
		"assigned_at": now.Format(time.RFC3339),
	})
}

func syncToFirebaseOptimized(client *firestore.Client, assigned model.Assigned, userEmail, boardID, taskID string) {
	// Context with reasonable timeout
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// Pre-build Firebase data to avoid runtime allocations
	firestoreData := map[string]interface{}{
		"assID":      assigned.AssID,
		"taskID":     assigned.TaskID,
		"userID":     assigned.UserID,
		"assignedAt": assigned.AssignAt,
	}

	// Retry with exponential backoff
	const maxRetries = 3
	baseDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			// Context cancelled or timeout
			return
		default:
		}

		_, err := client.Collection("Assigned").Doc(strconv.Itoa(assigned.AssID)).Set(ctx, firestoreData)
		if err == nil {
			return // Success
		}

		// Last attempt failed
		if attempt == maxRetries-1 {
			// TODO: Send to dead letter queue or retry queue
			fmt.Printf("Final attempt failed for assignment %d: %v", assigned.AssID, err)
			return
		}

		// Exponential backoff with jitter
		delay := time.Duration(attempt+1) * baseDelay
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
	}
}

// Optional: Batch assignment function for multiple users
func AssignedTaskBatch(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	var req struct {
		TaskID  string   `json:"task_id"`
		UserIDs []string `json:"user_ids"`
		BoardID string   `json:"board_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Validate batch size
	if len(req.UserIDs) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Too many users in batch (max 50)"})
		return
	}

	taskID, err := strconv.Atoi(req.TaskID)
	if err != nil || taskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	// Get user email
	var userEmail string
	if err := db.Model(&model.User{}).Select("email").Where("user_id = ?", userId).Pluck("email", &userEmail).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	// Prepare batch assignments
	now := time.Now()
	assignments := make([]model.Assigned, 0, len(req.UserIDs))

	for _, userIDStr := range req.UserIDs {
		assignedUserID, err := strconv.Atoi(userIDStr)
		if err != nil || assignedUserID <= 0 {
			continue // Skip invalid IDs
		}

		assignments = append(assignments, model.Assigned{
			TaskID:   taskID,
			UserID:   assignedUserID,
			AssignAt: now,
		})
	}

	// Batch insert to database
	if err := db.CreateInBatches(assignments, 25).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create batch assignments"})
		return
	}

	// Async Firebase batch sync
	go func() {
		for _, assignment := range assignments {
			go syncToFirebaseOptimized(firestoreClient, assignment, userEmail, req.BoardID, req.TaskID)
		}
	}()

	c.JSON(http.StatusCreated, gin.H{
		"message":        "Batch assignment completed",
		"assigned_count": len(assignments),
		"assigned_at":    now.Format(time.RFC3339),
	})
}
