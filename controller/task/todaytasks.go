package task

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"gorm.io/gorm"
)

func TodayTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/todaytasks", middleware.AccessTokenMiddleware())
	{
		routes.POST("/alltoday", func(c *gin.Context) {
			DataTodayTask(c, db, firestoreClient)
		})
		routes.POST("/allarchivetoday", func(c *gin.Context) {
			DataArchiveTodayTask(c, db, firestoreClient)
		})
		routes.POST("/data", func(c *gin.Context) {
			DataTodayTaskByName(c, firestoreClient)
		})
		routes.POST("/create", func(c *gin.Context) {
			CreateTodayTaskFirebase(c, firestoreClient)
		})
		routes.PUT("/finish", func(c *gin.Context) {
			FinishTodayTaskFirebase(c, firestoreClient)
		})
		routes.PUT("/adjusttask", func(c *gin.Context) {
			UpdateTodayTaskFirebase(c, db, firestoreClient)
		})
	}
}

func DataTodayTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	// Query the email from the database using userId
	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userId).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	iter := firestoreClient.
		Collection("TodayTasks").
		Doc(email).
		Collection("tasks").
		Where("archive", "==", false).
		Documents(c)

	defer iter.Stop()

	// Ensure tasks is a non-nil empty slice
	todaytasks := make([]map[string]interface{}, 0)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch tasks"})
			return
		}

		todaytasks = append(todaytasks, doc.Data())
	}

	c.JSON(http.StatusOK, gin.H{"tasks": todaytasks})
}

func DataArchiveTodayTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	// Query the email from the database using userId
	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userId).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	iter := firestoreClient.
		Collection("TodayTasks").
		Doc(email).
		Collection("tasks").
		Where("archive", "==", true).
		Documents(c)

	defer iter.Stop()

	// Ensure tasks is a non-nil empty slice
	tasks := make([]map[string]interface{}, 0)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch tasks"})
			return
		}

		tasks = append(tasks, doc.Data())
	}

	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

func DataTodayTaskByName(c *gin.Context, firestoreClient *firestore.Client) {
	var req dto.DataTodayTaskByNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	docRef := firestoreClient.
		Collection("TodayTasks").
		Doc(req.Email).
		Collection("tasks").
		Doc(req.TaskName)

	// ดึงข้อมูลจาก Firestore
	docSnap, err := docRef.Get(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task"})
		return
	}

	// แปลงข้อมูลให้อยู่ในรูป map[string]interface{}
	data := docSnap.Data()

	c.JSON(http.StatusOK, data)
}

func CreateTodayTaskFirebase(c *gin.Context, firestoreClient *firestore.Client) {
	var req dto.CreateTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// นับจำนวนเอกสารที่มีอยู่แล้วใน collection
	tasksIter := firestoreClient.Collection("TodayTasks").Doc(req.Email).Collection("tasks").Documents(c)
	defer tasksIter.Stop()

	count := 0
	for {
		_, err := tasksIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count tasks"})
			return
		}
		count++
	}

	taskID := fmt.Sprintf("%d_%s", count+1, req.TaskName)

	taskData := map[string]interface{}{
		"taskname":   req.TaskName,
		"status":     req.Status,
		"created_at": time.Now(),
		"archive":    false,
	}

	// ตรวจสอบและเพิ่ม description (รองรับทั้งกรณี nil และ empty string)
	if req.Description != nil {
		taskData["description"] = *req.Description
	} else {
		taskData["description"] = ""
	}

	// ตรวจสอบและเพิ่ม priority (รองรับทั้งกรณี nil และ empty string)
	if req.Priority != nil {
		taskData["priority"] = *req.Priority
	} else {
		taskData["priority"] = ""
	}

	_, err := firestoreClient.Collection("TodayTasks").Doc(req.Email).Collection("tasks").Doc(taskID).Set(c, taskData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create task in Firestore"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task created successfully"})
}

func FinishTodayTaskFirebase(c *gin.Context, firestoreClient *firestore.Client) {
	var req dto.FinishTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	docRef := firestoreClient.Collection("TodayTasks").Doc(req.Email).Collection("tasks").Doc(req.TaskName)

	_, err := docRef.Update(c, []firestore.Update{
		{Path: "archive", Value: true},
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task archive status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task archived successfully"})
}

func UpdateTodayTaskFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.AdjustTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Validate required fields
	if req.FirebaseTaskName == nil || *req.FirebaseTaskName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Firebase task name is required"})
		return
	}

	// Query the email from the database using userId
	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userId).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	ctx := context.Background()

	// Reference to the original document
	originalDocRef := firestoreClient.Collection("TodayTasks").Doc(email).Collection("tasks").Doc(*req.FirebaseTaskName)

	// Get the current document data
	docSnapshot, err := originalDocRef.Get(ctx)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Get current data
	currentData := docSnapshot.Data()

	// Prepare update data
	updateData := make(map[string]interface{})
	isTaskNameChanged := false
	newTaskName := ""

	// Check and update fields
	if req.TaskName != nil {
		updateData["taskname"] = *req.TaskName
		newTaskName = *req.TaskName
		// Check if task name actually changed
		if currentTaskName, exists := currentData["taskname"].(string); exists && currentTaskName != *req.TaskName {
			isTaskNameChanged = true
		}
	}

	if req.Description != nil {
		updateData["description"] = *req.Description
	}

	if req.Status != nil {
		updateData["status"] = *req.Status
	}

	if req.Priority != nil {
		updateData["priority"] = *req.Priority
	}

	// If no fields to update
	if len(updateData) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	// If task name changed, we need to create new document and delete old one
	if isTaskNameChanged {
		// Extract task ID from original document name (format: {id}_{taskname})
		originalDocName := *req.FirebaseTaskName
		parts := strings.SplitN(originalDocName, "_", 2)
		if len(parts) != 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid document name format"})
			return
		}

		taskId := parts[0] // Keep the original task ID

		// Generate new document name with same ID but new task name
		newDocName := taskId + "_" + newTaskName
		newDocRef := firestoreClient.Collection("TodayTasks").Doc(email).Collection("tasks").Doc(newDocName)

		// Start transaction to ensure data consistency
		err = firestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
			// Get current data again in transaction
			currentDoc, err := tx.Get(originalDocRef)
			if err != nil {
				return err
			}

			// Merge current data with updates
			newData := currentDoc.Data()
			for key, value := range updateData {
				newData[key] = value
			}

			// Create new document
			if err := tx.Set(newDocRef, newData); err != nil {
				return err
			}

			// Delete old document
			if err := tx.Delete(originalDocRef); err != nil {
				return err
			}

			return nil
		})

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task with name change"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":           "Task updated successfully with name change",
			"new_document_name": newDocName,
		})

	} else {
		var updates []firestore.Update
		for key, value := range updateData {
			updates = append(updates, firestore.Update{
				Path:  key,
				Value: value,
			})
		}

		_, err = originalDocRef.Update(ctx, updates)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Task updated successfully"})
	}
}
