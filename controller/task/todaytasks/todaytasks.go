package todaytasks

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

func TodayTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/todaytasks", middleware.AccessTokenMiddleware())
	{
		routes.GET("/allarchivetoday", func(c *gin.Context) {
			DataArchivedTodayTask(c, db, firestoreClient)
		})
		routes.POST("/databyid", func(c *gin.Context) {
			TodayTaskByID(c, db, firestoreClient)
		})
		routes.POST("/create", func(c *gin.Context) {
			CreateTodayTaskFirebase(c, db, firestoreClient)
		})
		routes.PUT("/finish", func(c *gin.Context) {
			FinishTodayTaskFirebase(c, db, firestoreClient)
		})
		routes.PUT("/adjusttask", func(c *gin.Context) {
			UpdateTodayTask(c, db, firestoreClient)
		})
	}
}

// หากต้องการเฉพาะ tasks ที่ archived แล้ว ให้ใช้ฟังก์ชันนี้แทน
func DataArchivedTodayTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userId).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	ctx := context.WithValue(c.Request.Context(), "timeout", 30*time.Second)

	iter := firestoreClient.
		Collection("TodayTasks").
		Doc(email).
		Collection("tasks").
		Where("Archived", "==", true).
		Documents(ctx)

	defer iter.Stop()

	tasks := make([]map[string]interface{}, 0)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error fetching archived document: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch archived tasks"})
			return
		}

		taskData := doc.Data()
		tasks = append(tasks, taskData)
	}

	c.JSON(http.StatusOK, gin.H{
		"Archivedtasks": tasks,
	})
}

func TodayTaskByID(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	var req dto.DataTodayTaskByNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Validate TaskID
	if req.TaskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "TaskID is required"})
		return
	}

	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userId).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	// Create context with timeout for Firestore operations
	ctx := context.WithValue(c.Request.Context(), "timeout", 30*time.Second)

	docRef := firestoreClient.
		Collection("TodayTasks").
		Doc(email).
		Collection("tasks").
		Doc(req.TaskID)

	// ดึงข้อมูลจาก Firestore
	docSnap, err := docRef.Get(ctx) // ใช้ proper context แทน gin context
	if err != nil {
		// Check if document doesn't exist
		if status.Code(err) == codes.NotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
			return
		}
		log.Printf("Error getting task: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task"})
		return
	}

	// Check if document exists
	if !docSnap.Exists() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// แปลงข้อมูลให้อยู่ในรูป map[string]interface{}
	data := docSnap.Data()

	// Query Attachments subcollection
	attachmentsIter := firestoreClient.
		Collection("TodayTasks").
		Doc(email).
		Collection("tasks").
		Doc(req.TaskID).
		Collection("Attachments").
		Documents(ctx)

	attachments := make([]map[string]interface{}, 0)
	for {
		doc, err := attachmentsIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error fetching attachments: %v", err)
			break
		}
		attachmentData := doc.Data()
		attachments = append(attachments, attachmentData)
	}
	attachmentsIter.Stop()

	// Query Checklists subcollection
	checklistsIter := firestoreClient.
		Collection("TodayTasks").
		Doc(email).
		Collection("tasks").
		Doc(req.TaskID).
		Collection("Checklists").
		Documents(ctx)

	checklists := make([]map[string]interface{}, 0)
	for {
		doc, err := checklistsIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error fetching checklists: %v", err)
			break
		}
		checklistData := doc.Data()
		checklists = append(checklists, checklistData)
	}
	checklistsIter.Stop()

	// Add subcollections to main data
	data["Attachments"] = attachments
	data["Checklists"] = checklists

	c.JSON(http.StatusOK, gin.H{
		"task": data,
	})
}

func FinishTodayTaskFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.FinishTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// ดึงข้อมูล user จากฐานข้อมูล
	var user struct {
		UserID int
		Email  string
	}
	if err := db.Table("user").Select("user_id, email").Where("user_id = ?", userId).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	docRef := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Doc(req.TaskID)

	// ดึงข้อมูล task ปัจจุบันเพื่อเช็คสถานะ Archived
	doc, err := docRef.Get(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task"})
		return
	}

	// เช็คสถานะ Archived ปัจจุบัน
	var currentArchived bool
	if archivedValue, exists := doc.Data()["Archived"]; exists {
		if archived, ok := archivedValue.(bool); ok {
			currentArchived = archived
		}
	}

	// สลับค่า Archived
	newArchivedValue := !currentArchived

	// อัพเดทสถานะ Archived
	_, err = docRef.Update(c, []firestore.Update{
		{Path: "Archived", Value: newArchivedValue},
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task archive status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task archived successfully"})
}

func UpdateTodayTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.AdjustTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
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
	DocRef := firestoreClient.Collection("TodayTasks").Doc(email).Collection("tasks").Doc(req.DocumentID)

	// Get the current document data to verify it exists
	docSnapshot, err := DocRef.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve task"})
		return
	}

	// Check if document exists
	if !docSnapshot.Exists() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Prepare update data - only include fields that are not empty
	updateData := make(map[string]interface{})

	if req.TaskName != "" {
		updateData["TaskName"] = req.TaskName
	}
	if req.Description != "" {
		updateData["Description"] = req.Description
	}
	if req.Status != "" {
		updateData["Status"] = req.Status
	}
	if req.Priority != "" {
		updateData["Priority"] = req.Priority
	}

	// Add updated timestamp
	updateData["updated_at"] = firestore.ServerTimestamp

	// Check if there's anything to update
	if len(updateData) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	// Update the document using Set with merge option
	_, err = DocRef.Set(ctx, updateData, firestore.MergeAll)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task"})
		return
	}

	// Return success response
	c.JSON(http.StatusOK, gin.H{
		"message":     "Task updated successfully",
		"document_id": req.DocumentID,
	})
}

func CreateTodayTaskFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.CreateTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var user model.User
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	// ดึงข้อมูล taskID ทั้งหมดที่มีอยู่
	tasksIter := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Documents(c)
	defer tasksIter.Stop()

	existingTaskIDs := make(map[int]bool)
	maxTaskID := 0

	for {
		doc, err := tasksIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch existing tasks"})
			return
		}

		// แปลง taskID จาก string เป็น int
		if taskIDStr, ok := doc.Data()["TaskID"].(string); ok {
			if taskIDInt, parseErr := strconv.Atoi(taskIDStr); parseErr == nil {
				existingTaskIDs[taskIDInt] = true
				if taskIDInt > maxTaskID {
					maxTaskID = taskIDInt
				}
			}
		}
	}

	// หา taskID ที่ว่างที่เล็กที่สุด
	var newTaskID int
	found := false

	// ตรวจสอบจาก 1 ไปจนถึง maxTaskID เพื่อหาช่องว่าง
	for i := 1; i <= maxTaskID; i++ {
		if !existingTaskIDs[i] {
			newTaskID = i
			found = true
			break
		}
	}

	// หากไม่มีช่องว่าง ให้ใช้เลขถัดไป
	if !found {
		newTaskID = maxTaskID + 1
	}

	taskID := fmt.Sprintf("%d", newTaskID)

	taskData := map[string]interface{}{
		"TaskID":    taskID,
		"TaskName":  req.TaskName,
		"CreatedBy": user.UserID,
		"CreatedAt": time.Now(),
		"Status":    req.Status,
		"Archived":  false,
	}

	// ตรวจสอบและเพิ่ม description (รองรับทั้งกรณี nil และ empty string)
	if req.Description != nil {
		taskData["Description"] = *req.Description
	} else {
		taskData["Description"] = ""
	}

	// ตรวจสอบและเพิ่ม priority (รองรับทั้งกรณี nil และ empty string)
	if req.Priority != nil {
		taskData["Priority"] = *req.Priority
	} else {
		taskData["Priority"] = ""
	}

	_, err := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Doc(taskID).Set(c, taskData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create task in Firestore"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Task created successfully",
		"taskID":  taskID,
	})
}
