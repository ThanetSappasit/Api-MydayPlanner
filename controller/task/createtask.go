package task

import (
	"context"
	"errors"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type TaskService struct {
	db              *gorm.DB
	firestoreClient *firestore.Client
}

func NewTaskService(db *gorm.DB, firestoreClient *firestore.Client) *TaskService {
	return &TaskService{
		db:              db,
		firestoreClient: firestoreClient,
	}
}

func CreateTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	service := NewTaskService(db, firestoreClient)
	router.POST("/task", middleware.AccessTokenMiddleware(), service.CreateTaskHandler)
}

func TodayTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	service := NewTaskService(db, firestoreClient)
	routes := router.Group("/todaytasks", middleware.AccessTokenMiddleware())
	{
		routes.POST("/create", service.CreateTodayTaskHandler)
	}
}

func (s *TaskService) CreateTaskHandler(c *gin.Context) {
	userId := c.MustGet("userId").(uint)

	var taskReq dto.CreateTaskRequest
	if err := c.ShouldBindJSON(&taskReq); err != nil {
		respondWithError(c, http.StatusBadRequest, "Invalid input", err)
		return
	}

	// ดึงข้อมูลผู้ใช้
	user, err := s.getUserByID(userId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondWithError(c, http.StatusNotFound, "User not found", nil)
		} else {
			respondWithError(c, http.StatusInternalServerError, "Failed to fetch user", err)
		}
		return
	}

	// ตรวจสอบว่าอยู่บอร์ดกลุ่มหรือไม่
	shouldSaveToFirestore, err := s.validateBoardAccess(taskReq.BoardID, userId)
	if err != nil {
		respondWithError(c, http.StatusForbidden, "Access denied: not a board member or board owner", err)
		return
	}

	// สร้างงาน
	task, notification, err := s.createTaskWithTransaction(&taskReq, user)
	if err != nil {
		respondWithError(c, http.StatusInternalServerError, "Failed to create task", err)
		return
	}

	// สร้างการแจ้งเตือนใน Firestore
	go s.handleFirestoreOperations(task, notification, user.Email, shouldSaveToFirestore)

	// Prepare response
	response := gin.H{
		"message": "Task created successfully",
		"taskID":  task.TaskID,
	}

	if notification != nil {
		response["notificationID"] = notification.NotificationID
	}

	c.JSON(http.StatusCreated, response)
}
func (s *TaskService) CreateTodayTaskHandler(c *gin.Context) {
	userId := c.MustGet("userId").(uint)

	var taskReq dto.CreateTodayTaskRequest
	if err := c.ShouldBindJSON(&taskReq); err != nil {
		respondWithError(c, http.StatusBadRequest, "Invalid input", err)
		return
	}

	// Get user information
	user, err := s.getUserByID(userId)
	if err != nil {
		respondWithError(c, http.StatusNotFound, "User not found", err)
		return
	}

	// Create today task with transaction
	task, notification, err := s.createTodayTaskWithTransaction(&taskReq, user)
	if err != nil {
		respondWithError(c, http.StatusInternalServerError, "Failed to create task", err)
		return
	}

	// Prepare response
	response := gin.H{
		"message": "Task created successfully",
		"taskID":  task.TaskID,
	}

	if notification != nil {
		response["notificationID"] = notification.NotificationID
	}

	c.JSON(http.StatusCreated, response)
}

func (s *TaskService) getUserByID(userID uint) (*model.User, error) {
	var user model.User
	err := s.db.Where("user_id = ?", userID).First(&user).Error
	return &user, err
}

func (s *TaskService) validateBoardAccess(boardID int, userID uint) (bool, error) {
	// Check if user is a board member
	var boardUser model.BoardUser
	err := s.db.Where("board_id = ?", boardID).First(&boardUser).Error
	if err == nil {
		return true, nil // User is board member, should save to Firestore
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err // Database error
	}

	// Check if user is board owner
	var board model.Board
	err = s.db.Where("board_id = ? AND create_by = ?", boardID, userID).First(&board).Error
	if err != nil {
		return false, err // Not found or database error
	}

	return false, nil // User is board owner, don't save to Firestore
}

func (s *TaskService) createTaskWithTransaction(taskReq *dto.CreateTaskRequest, user *model.User) (*model.Tasks, *model.Notification, error) {
	tx := s.db.Begin()
	if tx.Error != nil {
		return nil, nil, tx.Error
	}
	defer tx.Rollback()

	// Create task
	task := &model.Tasks{
		BoardID:     &taskReq.BoardID,
		TaskName:    taskReq.TaskName,
		Description: stringToPtr(taskReq.Description),
		Status:      taskReq.Status,
		Priority:    stringToPtr(taskReq.Priority),
		CreateBy:    intToPtr(user.UserID),
		CreateAt:    time.Now(),
	}

	if err := tx.Create(task).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to create task: %w", err)
	}

	// Handle notification if provided and DueDate is not empty
	var notification *model.Notification
	if taskReq.Reminder != nil && strings.TrimSpace(taskReq.Reminder.DueDate) != "" {
		notif, err := s.createNotificationInTx(tx, uint(task.TaskID), taskReq.Reminder)
		if err != nil {
			return nil, nil, err
		}
		notification = notif
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return nil, nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return task, notification, nil
}

func (s *TaskService) createTodayTaskWithTransaction(taskReq *dto.CreateTodayTaskRequest, user *model.User) (*model.Tasks, *model.Notification, error) {
	tx := s.db.Begin()
	if tx.Error != nil {
		return nil, nil, tx.Error
	}
	defer tx.Rollback()

	// Create task
	task := &model.Tasks{
		BoardID:     nil, // Today tasks don't belong to a board
		TaskName:    taskReq.TaskName,
		Description: stringToPtr(taskReq.Description),
		Status:      taskReq.Status,
		Priority:    stringToPtr(taskReq.Priority),
		CreateBy:    intToPtr(user.UserID),
		CreateAt:    time.Now(),
	}

	if err := tx.Create(task).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to create task: %w", err)
	}

	// Handle notification only if reminder exists and DueDate is not empty
	var notification *model.Notification
	if taskReq.Reminder != nil && strings.TrimSpace(taskReq.Reminder.DueDate) != "" {
		notif, err := s.createNotificationInTx(tx, uint(task.TaskID), taskReq.Reminder)
		if err != nil {
			return nil, nil, err
		}
		notification = notif
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return nil, nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return task, notification, nil
}

func (s *TaskService) createNotificationInTx(tx *gorm.DB, taskID uint, reminder *dto.Reminder) (*model.Notification, error) {
	parsedDueDate, err := parseDateTime(reminder.DueDate)
	if err != nil {
		return nil, fmt.Errorf("invalid DueDate format: %w", err)
	}

	notification := &model.Notification{
		TaskID:           int(taskID),
		DueDate:          parsedDueDate,
		RecurringPattern: reminder.RecurringPattern,
		IsSend: func() string {
			if parsedDueDate.Before(time.Now()) {
				return "2"
			}
			return "0"
		}(),
		CreatedAt: time.Now(),
	}

	if err := tx.Create(notification).Error; err != nil {
		return nil, fmt.Errorf("failed to create notification: %w", err)
	}

	return notification, nil
}

func (s *TaskService) handleFirestoreOperations(task *model.Tasks, notification *model.Notification, userEmail string, shouldSaveToFirestore bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Save task to Firestore if needed
	if shouldSaveToFirestore && task.BoardID != nil {
		if err := s.saveTaskToFirestore(ctx, task, int(*task.BoardID)); err != nil {
			fmt.Printf("Warning: Failed to save task to Firestore: %v", err)
		}
	}

}

func (s *TaskService) saveTaskToFirestore(ctx context.Context, task *model.Tasks, boardID int) error {
	taskPath := fmt.Sprintf("Boards/%d/Tasks/%d", boardID, task.TaskID)
	boardPath := fmt.Sprintf("BoardTasks/%d", task.TaskID)

	boardData := map[string]interface{}{
		"createAt": task.CreateAt,
	}
	if _, err := s.firestoreClient.Doc(boardPath).Set(ctx, boardData, firestore.MergeAll); err != nil {
		return err
	}

	taskData := map[string]interface{}{
		"taskID":      task.TaskID,
		"boardID":     boardID,
		"taskName":    task.TaskName,
		"description": task.Description,
		"status":      task.Status,
		"priority":    task.Priority,
		"createBy":    task.CreateBy,
		"createAt":    task.CreateAt,
		"updatedAt":   time.Now(),
	}

	// Add optional fields safely
	if task.Description != nil {
		taskData["description"] = *task.Description
	}
	if task.Priority != nil {
		taskData["priority"] = *task.Priority
	}
	if task.CreateBy != nil {
		taskData["createBy"] = *task.CreateBy
	}

	_, err := s.firestoreClient.Doc(taskPath).Set(ctx, taskData)
	return err
}

func respondWithError(c *gin.Context, statusCode int, message string, err error) {
	response := gin.H{"error": message}

	if err != nil {
		// In development, you might want to include error details
		// In production, log the error and return generic message
		fmt.Printf("Error: %v", err)
		response["details"] = err.Error()
	}

	c.JSON(statusCode, response)
}

func stringToPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intToPtr(i int) *int {
	return &i
}

func parseDateTime(dateStr string) (time.Time, error) {
	// ตรวจสอบ empty string ก่อน
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("due date is required")
	}

	// ตัด whitespace ออก
	dateStr = strings.TrimSpace(dateStr)
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("due date cannot be empty")
	}

	formats := []string{
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported date format: %s", dateStr)
}
