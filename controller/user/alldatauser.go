package user

import (
	"fmt"
	"mydayplanner/model"
	"net/http"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type BatchResult struct {
	TodayTasks []map[string]interface{}
	Error      error
}

type TaskRelatedData struct {
	Checklists    []model.Checklist
	Attachments   []model.Attachment
	Notifications []model.Notification
	Assigned      []struct {
		model.Assigned
		UserName string `gorm:"column:user_name"`
		Email    string `gorm:"column:email"`
	}
}

func AllDataUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	// Channel สำหรับรับผลลัพธ์จาก goroutines
	userChan := make(chan map[string]interface{}, 1)
	boardChan := make(chan []map[string]interface{}, 1)
	boardGroupChan := make(chan []map[string]interface{}, 1)
	tasksChan := make(chan []map[string]interface{}, 1)
	errorChan := make(chan error, 5)

	var wg sync.WaitGroup

	// 1. Fetch user data
	wg.Add(1)
	go func() {
		defer wg.Done()
		userData, err := fetchUserData(db, userId)
		if err != nil {
			select {
			case errorChan <- fmt.Errorf("failed to get user data: %w", err):
			default:
			}
			return
		}
		userChan <- userData
	}()

	// 2. Fetch board data
	wg.Add(1)
	go func() {
		defer wg.Done()
		boardData, err := fetchBoardData(db, userId)
		if err != nil {
			select {
			case errorChan <- fmt.Errorf("failed to get board data: %w", err):
			default:
			}
			return
		}
		boardChan <- boardData
	}()

	// 3. Fetch board group data
	wg.Add(1)
	go func() {
		defer wg.Done()
		boardGroupData, err := fetchBoardGroupData(db, userId)
		if err != nil {
			select {
			case errorChan <- fmt.Errorf("failed to get boardgroup data: %w", err):
			default:
			}
			return
		}
		boardGroupChan <- boardGroupData
	}()

	// รอผลลัพธ์
	wg.Wait()

	// ตรวจสอบ error
	select {
	case err := <-errorChan:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	default:
	}

	// รับผลลัพธ์
	userData := <-userChan
	boardData := <-boardChan
	boardGroupData := <-boardGroupChan

	// 4. Fetch tasks data (ต้องรอ board data ก่อน)
	allBoardIDs := extractBoardIDs(boardData, boardGroupData)
	fmt.Printf("Debug: Found %d board IDs: %v\n", len(allBoardIDs), allBoardIDs) // Debug log

	var wg2 sync.WaitGroup

	wg2.Add(1)
	go func() {
		defer wg2.Done()
		tasksData, err := fetchTasksDataOptimized(db, allBoardIDs, userId)
		if err != nil {
			select {
			case errorChan <- fmt.Errorf("failed to get tasks data: %w", err):
			default:
			}
			return
		}
		fmt.Printf("Debug: Found %d tasks\n", len(tasksData)) // Debug log
		tasksChan <- tasksData
	}()

	wg2.Wait()

	// ตรวจสอบ error จาก task fetching
	select {
	case err := <-errorChan:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	default:
	}

	tasks := <-tasksChan

	c.JSON(http.StatusOK, gin.H{
		"user":       userData,
		"board":      boardData,
		"boardgroup": boardGroupData,
		"tasks":      tasks,
	})
}

func fetchUserData(db *gorm.DB, userId uint) (map[string]interface{}, error) {
	var user model.User
	if err := db.Raw("SELECT user_id, email, name, role, profile, is_verify, is_active, create_at FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"UserID":    user.UserID,
		"Email":     user.Email,
		"Name":      user.Name,
		"Profile":   user.Profile,
		"Role":      user.Role,
		"IsVerify":  user.IsVerify,
		"IsActive":  user.IsActive,
		"CreatedAt": user.CreatedAt,
	}, nil
}

func fetchBoardData(db *gorm.DB, userId uint) ([]map[string]interface{}, error) {
	var boardData []struct {
		BoardID     uint      `gorm:"column:board_id"`
		BoardName   string    `gorm:"column:board_name"`
		CreatedAt   time.Time `gorm:"column:create_at"`
		CreatedBy   int       `gorm:"column:create_by"`
		UserName    string    `gorm:"column:name"`
		UserEmail   string    `gorm:"column:email"`
		UserProfile string    `gorm:"column:profile"`
	}

	if err := db.Raw(`SELECT 
			b.board_id,
			b.board_name,
			b.create_at,
			b.create_by,
			u.name,
			u.email,
			u.profile
		FROM board b
		INNER JOIN user u ON b.create_by = u.user_id
		WHERE 
			b.create_by = ?
			AND NOT EXISTS (
				SELECT 1 FROM board_user bu 
				WHERE bu.board_id = b.board_id AND bu.user_id = ?
			)`, userId, userId).Scan(&boardData).Error; err != nil {
		return nil, err
	}

	board := make([]map[string]interface{}, 0, len(boardData))
	for _, b := range boardData {
		board = append(board, map[string]interface{}{
			"BoardID":   b.BoardID,
			"BoardName": b.BoardName,
			"CreatedAt": b.CreatedAt,
			"CreatedBy": b.CreatedBy,
			"CreatedByUser": map[string]interface{}{
				"UserID":  b.CreatedBy,
				"Name":    b.UserName,
				"Email":   b.UserEmail,
				"Profile": b.UserProfile,
			},
		})
	}

	return board, nil
}

func fetchBoardGroupData(db *gorm.DB, userId uint) ([]map[string]interface{}, error) {
	var boardGroupData []struct {
		BoardID     uint      `gorm:"column:board_id"`
		BoardName   string    `gorm:"column:board_name"`
		CreatedAt   time.Time `gorm:"column:create_at"`
		CreatedBy   int       `gorm:"column:create_by"`
		Token       string    `gorm:"column:token"`
		UserName    string    `gorm:"column:name"`
		UserEmail   string    `gorm:"column:email"`
		UserProfile string    `gorm:"column:profile"`
	}

	if err := db.Raw(`SELECT 
			b.board_id,
			b.board_name,
			b.create_at,
			b.create_by,
			bt.token,
			u.name,
			u.email,
			u.profile
		FROM board b
		INNER JOIN board_user bu ON b.board_id = bu.board_id
		INNER JOIN user u ON b.create_by = u.user_id
		LEFT JOIN board_token bt ON b.board_id = bt.board_id
		WHERE bu.user_id = ?`, userId).Scan(&boardGroupData).Error; err != nil {
		return nil, err
	}

	boardgroup := make([]map[string]interface{}, 0, len(boardGroupData))
	for _, bg := range boardGroupData {
		boardgroup = append(boardgroup, map[string]interface{}{
			"BoardID":   bg.BoardID,
			"BoardName": bg.BoardName,
			"CreatedAt": bg.CreatedAt,
			"CreatedBy": bg.CreatedBy,
			"Token":     bg.Token,
			"CreatedByUser": map[string]interface{}{
				"UserID":  bg.CreatedBy,
				"Name":    bg.UserName,
				"Email":   bg.UserEmail,
				"Profile": bg.UserProfile,
			},
		})
	}

	return boardgroup, nil
}

func extractBoardIDs(boardData, boardGroupData []map[string]interface{}) []uint {
	allBoardIDs := make([]uint, 0, len(boardData)+len(boardGroupData))

	for _, b := range boardData {
		if boardID, ok := b["BoardID"].(uint); ok {
			allBoardIDs = append(allBoardIDs, boardID)
		}
	}
	for _, bg := range boardGroupData {
		if boardID, ok := bg["BoardID"].(uint); ok {
			allBoardIDs = append(allBoardIDs, boardID)
		}
	}

	return allBoardIDs
}

// Updated function to include user tasks and handle null board_id
func fetchTasksDataOptimized(db *gorm.DB, allBoardIDs []uint, userId uint) ([]map[string]interface{}, error) {
	var tasksData []struct {
		TaskID      int       `gorm:"column:task_id"`
		BoardID     *int      `gorm:"column:board_id"`
		TaskName    string    `gorm:"column:task_name"`
		Description *string   `gorm:"column:description"`
		Status      string    `gorm:"column:status"`
		Priority    *string   `gorm:"column:priority"`
		CreateBy    *int      `gorm:"column:create_by"`
		CreateAt    time.Time `gorm:"column:create_at"`
	}

	query := `SELECT 
		task_id, board_id, task_name, description, 
		status, priority, create_by, create_at
	FROM tasks 
	WHERE `

	var args []interface{}

	if len(allBoardIDs) > 0 {
		// กรณี Group Boards: แสดงทั้ง tasks ใน boards และ Today tasks ของ user
		query += `(board_id IN (?) OR (board_id IS NULL AND create_by = ?))`
		args = append(args, allBoardIDs, userId)
	} else {
		// กรณี Private/Today: แสดงเฉพาะ Today tasks ของ user เท่านั้น
		query += `(board_id IS NULL AND create_by = ?)`
		args = append(args, userId)
	}

	if err := db.Raw(query, args...).Scan(&tasksData).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch tasks: %w", err)
	}

	if len(tasksData) == 0 {
		return make([]map[string]interface{}, 0), nil
	}

	// Extract all task IDs
	taskIDs := make([]uint, len(tasksData))
	for i, task := range tasksData {
		taskIDs[i] = uint(task.TaskID)
	}

	// Fetch all related data in parallel
	var wg sync.WaitGroup
	relatedDataChan := make(chan TaskRelatedData, 1)
	errorChan := make(chan error, 1)

	wg.Add(1)
	go func() {
		defer wg.Done()
		relatedData, err := fetchAllRelatedData(db, taskIDs)
		if err != nil {
			select {
			case errorChan <- err:
			default:
			}
			return
		}
		relatedDataChan <- relatedData
	}()

	wg.Wait()

	select {
	case err := <-errorChan:
		return nil, err
	default:
	}

	relatedData := <-relatedDataChan

	// Group related data by task ID
	checklistsByTask := groupChecklistsByTask(relatedData.Checklists)
	attachmentsByTask := groupAttachmentsByTask(relatedData.Attachments)
	notificationsByTask := groupNotificationsByTask(relatedData.Notifications)

	// Build result
	tasks := make([]map[string]interface{}, 0, len(tasksData))
	for _, task := range tasksData {
		// Handle null board_id - convert to "Today"
		var boardDisplay interface{}
		if task.BoardID == nil {
			boardDisplay = "Today"
		} else {
			boardDisplay = *task.BoardID
		}

		// Handle null Description and Priority - convert to empty string
		var description string
		if task.Description == nil {
			description = ""
		} else {
			description = *task.Description
		}

		var priority string
		if task.Priority == nil {
			priority = ""
		} else {
			priority = *task.Priority
		}

		taskMap := map[string]interface{}{
			"TaskID":        task.TaskID,
			"BoardID":       boardDisplay, // This will be "Today" if board_id is null
			"TaskName":      task.TaskName,
			"Description":   description, // Empty string if null
			"Status":        task.Status,
			"Priority":      priority, // Empty string if null
			"CreateBy":      task.CreateBy,
			"CreatedAt":     task.CreateAt,
			"Checklists":    buildChecklistsMap(checklistsByTask[task.TaskID]),
			"Attachments":   buildAttachmentsMap(attachmentsByTask[task.TaskID]),
			"Notifications": buildNotificationsMap(notificationsByTask[task.TaskID]),
		}
		tasks = append(tasks, taskMap)
	}

	return tasks, nil
}

func buildChecklistsMap(checklists []model.Checklist) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(checklists))
	for _, checklist := range checklists {
		result = append(result, map[string]interface{}{
			"ChecklistID":   checklist.ChecklistID,
			"TaskID":        checklist.TaskID,
			"ChecklistName": checklist.ChecklistName,
			"Status":        checklist.Status,
			"CreatedAt":     checklist.CreateAt,
		})
	}
	return result
}

func buildAttachmentsMap(attachments []model.Attachment) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(attachments))
	for _, attachment := range attachments {
		result = append(result, map[string]interface{}{
			"AttachmentID": attachment.AttachmentID,
			"TasksID":      attachment.TasksID,
			"FileName":     attachment.FileName,
			"FilePath":     attachment.FilePath,
			"FileType":     attachment.FileType,
			"UploadAt":     attachment.UploadAt,
		})
	}
	return result
}

func buildNotificationsMap(notifications []model.Notification) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(notifications))
	for _, notification := range notifications {
		// Handle null BeforeDueDate - convert to "none"
		var beforeDueDate interface{}
		if notification.BeforeDueDate == nil {
			beforeDueDate = "none"
		} else {
			beforeDueDate = *notification.BeforeDueDate
		}

		// Keep IsSend as string but handle empty values
		isSend := notification.IsSend
		if isSend == "" {
			isSend = "0" // default value
		}

		result = append(result, map[string]interface{}{
			"NotificationID":   notification.NotificationID,
			"TaskID":           notification.TaskID,
			"DueDate":          notification.DueDate,
			"BeforeDueDate":    beforeDueDate,
			"RecurringPattern": notification.RecurringPattern,
			"IsSend":           isSend, // ยังคงเป็น string
			"CreatedAt":        notification.CreatedAt,
		})
	}
	return result
}

func fetchAllRelatedData(db *gorm.DB, taskIDs []uint) (TaskRelatedData, error) {
	var wg sync.WaitGroup
	var checklists []model.Checklist
	var attachments []model.Attachment
	var notifications []model.Notification
	var assigned []struct {
		model.Assigned
		UserName string `gorm:"column:user_name"`
		Email    string `gorm:"column:email"`
	}
	errorChan := make(chan error, 4)

	// Fetch checklists
	wg.Add(1)
	go func() {
		defer wg.Done()
		var checklistsData []model.Checklist
		if err := db.Raw(`SELECT checklist_id, task_id, checklist_name, status, create_at 
			FROM checklists WHERE task_id IN (?)`, taskIDs).Scan(&checklistsData).Error; err != nil {
			select {
			case errorChan <- fmt.Errorf("failed to fetch checklists: %w", err):
			default:
			}
			return
		}
		checklists = checklistsData
	}()

	// Fetch attachments
	wg.Add(1)
	go func() {
		defer wg.Done()
		var attachmentsData []model.Attachment
		if err := db.Raw(`SELECT attachment_id, tasks_id, file_name, file_path, file_type, upload_at 
			FROM attachments WHERE tasks_id IN (?)`, taskIDs).Scan(&attachmentsData).Error; err != nil {
			select {
			case errorChan <- fmt.Errorf("failed to fetch attachments: %w", err):
			default:
			}
			return
		}
		attachments = attachmentsData
	}()

	// Fetch notifications
	wg.Add(1)
	go func() {
		defer wg.Done()
		var notificationsData []model.Notification
		if err := db.Raw(`SELECT notification_id, task_id, due_date, beforedue_date, recurring_pattern, is_send, created_at 
        FROM notification WHERE task_id IN (?)`, taskIDs).Scan(&notificationsData).Error; err != nil {
			select {
			case errorChan <- fmt.Errorf("failed to fetch notifications: %w", err):
			default:
			}
			return
		}
		notifications = notificationsData
	}()

	wg.Wait()

	select {
	case err := <-errorChan:
		return TaskRelatedData{}, err
	default:
	}

	return TaskRelatedData{
		Checklists:    checklists,
		Attachments:   attachments,
		Notifications: notifications,
		Assigned:      assigned,
	}, nil
}

func groupChecklistsByTask(checklists []model.Checklist) map[int][]model.Checklist {
	result := make(map[int][]model.Checklist)
	for _, checklist := range checklists {
		result[checklist.TaskID] = append(result[checklist.TaskID], checklist)
	}
	return result
}

func groupAttachmentsByTask(attachments []model.Attachment) map[int][]model.Attachment {
	result := make(map[int][]model.Attachment)
	for _, attachment := range attachments {
		result[attachment.TasksID] = append(result[attachment.TasksID], attachment)
	}
	return result
}

func groupNotificationsByTask(notifications []model.Notification) map[int][]model.Notification {
	result := make(map[int][]model.Notification)
	for _, notification := range notifications {
		result[notification.TaskID] = append(result[notification.TaskID], notification)
	}
	return result
}
