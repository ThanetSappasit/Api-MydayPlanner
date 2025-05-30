package todaytasks

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

const (
	DefaultTimeout       = 45 * time.Second
	DefaultBatchSize     = 100
	MaxConcurrency       = 20
	SubcollectionWorkers = 3
)

func DeleteTodayTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/todaytasks", middleware.AccessTokenMiddleware())
	{
		routes.DELETE("/deltoday", func(c *gin.Context) {
			ListDeleteTodayTask(c, db, firestoreClient)
		})
		routes.DELETE("/delidtoday", func(c *gin.Context) {
			IDDeleteTodayTask(c, db, firestoreClient)
		})
	}
}

// User structure for caching
type UserInfo struct {
	UserID int
	Email  string
}

// Task deletion result
type TaskDeletionResult struct {
	TaskID string
	Error  error
}

func ListDeleteTodayTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.DeleteTodayTaskRequest

	// Bind และ validate JSON input
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// ตรวจสอบและทำความสะอาด TaskID
	validTaskIDs := validateAndCleanTaskIDs(req.TaskID)
	if len(validTaskIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "At least one valid task ID is required"})
		return
	}

	// ดึงข้อมูล user
	user, err := getUserInfo(db, userId)
	if err != nil {
		handleUserError(c, err)
		return
	}

	// ลบ tasks แบบ batch processing
	results := deleteTasks(firestoreClient, user.Email, validTaskIDs)

	// สร้าง response
	response := buildResponse(results)
	c.JSON(http.StatusOK, response)
}

func IDDeleteTodayTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.DeleteIDTodayTaskRequest

	// Bind และ validate JSON input
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// ตรวจสอบ TaskID เดียว
	if req.TaskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Task ID cannot be empty"})
		return
	}

	// ดึงข้อมูล user
	user, err := getUserInfo(db, userId)
	if err != nil {
		handleUserError(c, err)
		return
	}

	// ลบ task เดียว
	results := deleteTasks(firestoreClient, user.Email, []string{req.TaskID})

	// สร้าง response
	response := buildResponse(results)
	c.JSON(http.StatusOK, response)
}

// Helper functions

func validateAndCleanTaskIDs(taskIDs []string) []string {
	if len(taskIDs) == 0 {
		return nil
	}

	// ใช้ map เพื่อ deduplicate และ slice เพื่อเก็บลำดับ
	seen := make(map[string]bool, len(taskIDs))
	validIDs := make([]string, 0, len(taskIDs))

	for _, id := range taskIDs {
		// ตัด whitespace และตรวจสอบว่าไม่ว่าง
		trimmedID := strings.TrimSpace(id)
		if trimmedID != "" && !seen[trimmedID] {
			seen[trimmedID] = true
			validIDs = append(validIDs, trimmedID)
		}
	}

	return validIDs
}

func getUserInfo(db *gorm.DB, userId uint) (*UserInfo, error) {
	var user UserInfo
	err := db.Table("user").
		Select("user_id, email").
		Where("user_id = ?", userId).
		First(&user).Error

	if err != nil {
		return nil, err
	}
	return &user, nil
}

func handleUserError(c *gin.Context, err error) {
	if err == gorm.ErrRecordNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
	}
}

func deleteTasks(firestoreClient *firestore.Client, userEmail string, taskIDs []string) []TaskDeletionResult {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	// คำนวณ concurrency ที่เหมาะสม
	concurrency := calculateOptimalConcurrency(len(taskIDs))

	// Channel สำหรับควบคุม concurrency
	semaphore := make(chan struct{}, concurrency)
	results := make(chan TaskDeletionResult, len(taskIDs))

	var wg sync.WaitGroup
	tasksCollection := firestoreClient.Collection("TodayTasks").Doc(userEmail).Collection("tasks")

	// ประมวลผลแต่ละ task แบบ concurrent
	for _, taskID := range taskIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			// รอให้ได้ slot
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			err := deleteTaskOptimized(ctx, tasksCollection.Doc(id), firestoreClient)
			results <- TaskDeletionResult{TaskID: id, Error: err}
		}(taskID)
	}

	// รอให้ทุก goroutine เสร็จแล้วปิด channel
	go func() {
		wg.Wait()
		close(results)
	}()

	// รวบรวมผลลัพธ์
	var allResults []TaskDeletionResult
	for result := range results {
		allResults = append(allResults, result)
	}

	return allResults
}

func calculateOptimalConcurrency(taskCount int) int {
	// ใช้ CPU cores เป็นฐานในการคำนวณ
	numCPU := runtime.NumCPU()
	optimal := numCPU * 2

	// จำกัดตาม task count และ maximum
	if taskCount < optimal {
		optimal = taskCount
	}
	if optimal > MaxConcurrency {
		optimal = MaxConcurrency
	}
	if optimal < 1 {
		optimal = 1
	}

	return optimal
}

func deleteTaskOptimized(ctx context.Context, taskDocRef *firestore.DocumentRef, firestoreClient *firestore.Client) error {
	// ตรวจสอบว่า task มีอยู่จริงและดึงข้อมูลพร้อมกัน
	taskDoc, err := taskDocRef.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return status.Error(codes.NotFound, "task not found")
		}
		return fmt.Errorf("failed to get task: %w", err)
	}

	if !taskDoc.Exists() {
		return status.Error(codes.NotFound, "task not found")
	}

	// ลบ subcollections และ main document แบบ pipeline
	return deleteTaskWithSubcollections(ctx, taskDocRef, firestoreClient)
}

func deleteTaskWithSubcollections(ctx context.Context, taskDocRef *firestore.DocumentRef, firestoreClient *firestore.Client) error {
	// ใช้ error group pattern สำหรับ concurrent deletion
	errChan := make(chan error, SubcollectionWorkers)
	var wg sync.WaitGroup

	// รายการ subcollections ที่ต้องลบ
	subcollections := []string{"Attachments", "Checklists"}

	// ลบ subcollections แบบ concurrent
	for _, subcolName := range subcollections {
		wg.Add(1)
		go func(collectionName string) {
			defer wg.Done()
			if err := deleteCollectionBatched(ctx, taskDocRef.Collection(collectionName), firestoreClient); err != nil {
				errChan <- fmt.Errorf("failed to delete %s: %w", collectionName, err)
			}
		}(subcolName)
	}

	// รอให้ subcollections เสร็จ
	go func() {
		wg.Wait()
		close(errChan)
	}()

	// ตรวจสอบ errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// ลบ main document
	if _, err := taskDocRef.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete main document: %w", err)
	}

	return nil
}

func deleteCollectionBatched(ctx context.Context, collectionRef *firestore.CollectionRef, firestoreClient *firestore.Client) error {
	for {
		// ใช้ batch size ที่ใหญ่ขึ้น
		iter := collectionRef.Limit(DefaultBatchSize).Documents(ctx)

		batch := firestoreClient.Batch()
		docCount := 0

		// รวบรวม documents ใน batch
		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return fmt.Errorf("iterator error: %w", err)
			}

			batch.Delete(doc.Ref)
			docCount++
		}

		// ถ้าไม่มี documents ให้ลบ
		if docCount == 0 {
			break
		}

		// Commit batch
		if _, err := batch.Commit(ctx); err != nil {
			return fmt.Errorf("batch commit error: %w", err)
		}

		// ถ้าลบได้น้อยกว่า batch size แสดงว่าเสร็จแล้ว
		if docCount < DefaultBatchSize {
			break
		}

		// เพิ่ม small delay เพื่อป้องกัน rate limiting
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}

	return nil
}

func buildResponse(results []TaskDeletionResult) gin.H {
	var errors []string

	for _, result := range results {
		if result.Error != nil {
			if status.Code(result.Error) == codes.NotFound {
				errors = append(errors, fmt.Sprintf("Task %s not found", result.TaskID))
			} else {
				errors = append(errors, fmt.Sprintf("Failed to delete task %s: %s", result.TaskID, result.Error.Error()))
			}
		}
	}

	// ถ้ามี error ให้ return errors
	if len(errors) > 0 {
		return gin.H{"errors": errors}
	}

	// ถ้าไม่มี error ให้ return success message
	return gin.H{"message": "delete success"}
}
