package todaytasks

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"sync"
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
		routes.POST("/allarchivetoday", func(c *gin.Context) {
			DataArchiveTodayTask(c, db, firestoreClient)
		})
		routes.POST("/data", func(c *gin.Context) {
			DataTodayTaskByName(c, firestoreClient)
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
		routes.DELETE("/deltoday", func(c *gin.Context) {
			DeleteTodayTask(c, db, firestoreClient)
		})
	}
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

	_, err := docRef.Update(c, []firestore.Update{
		{Path: "Archived", Value: true},
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

func DeleteTodayTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.DeleteTodayTaskRequest

	// Bind และ validate JSON input
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// ตรวจสอบว่ามี TaskID ส่งมา
	if len(req.TaskID) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "At least one task ID is required"})
		return
	}

	// ตรวจสอบว่าไม่มี TaskID ที่เป็นค่าว่าง
	for _, taskID := range req.TaskID {
		if taskID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Task ID cannot be empty"})
			return
		}
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

	ctx := context.Background()
	tasksCollection := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks")

	// ใช้ goroutines แบบ controlled concurrency
	const maxConcurrency = 5 // จำกัดจำนวน goroutines พร้อมกัน
	semaphore := make(chan struct{}, maxConcurrency)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var deletedTasks []string
	var errors []string

	// สร้าง context พร้อม timeout
	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()

	// ประมวลผลแต่ละ task แบบ concurrent
	for _, taskID := range req.TaskID {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			// รอให้ได้ slot
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if err := deleteTaskConcurrent(ctxWithTimeout, tasksCollection.Doc(id), firestoreClient); err != nil {
				mu.Lock()
				if status.Code(err) == codes.NotFound {
					errors = append(errors, "Task "+id+" not found")
				} else {
					errors = append(errors, "Failed to delete task "+id+": "+err.Error())
				}
				mu.Unlock()
			} else {
				mu.Lock()
				deletedTasks = append(deletedTasks, id)
				mu.Unlock()
			}
		}(taskID)
	}

	// รอให้ทุก goroutine เสร็จ
	wg.Wait()

	// สร้าง response
	response := gin.H{
		"deleted_tasks":   deletedTasks,
		"deleted_count":   len(deletedTasks),
		"total_requested": len(req.TaskID),
	}

	if len(errors) > 0 {
		response["errors"] = errors
	}

	c.JSON(http.StatusOK, response)
}

// ฟังก์ชันลบ task แบบ concurrent
func deleteTaskConcurrent(ctx context.Context, taskDocRef *firestore.DocumentRef, firestoreClient *firestore.Client) error {
	// ตรวจสอบว่า task มีอยู่จริงหรือไม่
	taskDoc, err := taskDocRef.Get(ctx)
	if err != nil {
		return err
	}
	if !taskDoc.Exists() {
		return status.Error(codes.NotFound, "task not found")
	}

	// ลบ subcollections และ main document แบบ concurrent
	var wg sync.WaitGroup
	errChan := make(chan error, 3)

	// ลบ Attachments subcollection
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := deleteCollectionOptimized(ctx, taskDocRef.Collection("Attachments"), firestoreClient, 50); err != nil {
			errChan <- err
		}
	}()

	// ลบ Checklists subcollection
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := deleteCollectionOptimized(ctx, taskDocRef.Collection("Checklists"), firestoreClient, 50); err != nil {
			errChan <- err
		}
	}()

	// รอให้ subcollections ถูกลบเสร็จ
	wg.Wait()
	close(errChan)

	// ตรวจสอบ errors จาก subcollections
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// ลบ main document
	_, err = taskDocRef.Delete(ctx)
	return err
}

// ฟังก์ชันลบ collection ที่ปรับปรุงแล้ว
func deleteCollectionOptimized(ctx context.Context, collectionRef *firestore.CollectionRef, firestoreClient *firestore.Client, batchSize int) error {
	for {
		// ใช้ batch size ที่ใหญ่ขึ้นเพื่อลดจำนวน round trips
		iter := collectionRef.Limit(batchSize).Documents(ctx)

		// สร้าง batch สำหรับลบ
		batch := firestoreClient.Batch()
		numDeleted := 0

		// เก็บ document references ทั้งหมดก่อน
		var docRefs []*firestore.DocumentRef
		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return err
			}
			docRefs = append(docRefs, doc.Ref)
			numDeleted++
		}

		// ถ้าไม่มี documents ให้ลบแล้ว ให้หยุด
		if numDeleted == 0 {
			break
		}

		// เพิ่ม delete operations ทั้งหมดใน batch
		for _, docRef := range docRefs {
			batch.Delete(docRef)
		}

		// Commit batch
		_, err := batch.Commit(ctx)
		if err != nil {
			return err
		}

		// ถ้าลบได้น้อยกว่า batch size แสดงว่าเสร็จแล้ว
		if numDeleted < batchSize {
			break
		}
	}

	return nil
}
