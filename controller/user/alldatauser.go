package user

import (
	"context"
	"fmt"
	"mydayplanner/model"
	"net/http"
	"sort"
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
	Checklists  []model.Checklist
	Attachments []model.Attachment
	Assigned    []struct {
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
	todayTasksChan := make(chan []map[string]interface{}, 1)
	errorChan := make(chan error, 5)

	var wg sync.WaitGroup
	ctx := c.Request.Context()

	// 1. Fetch user data
	wg.Add(1)
	go func() {
		defer wg.Done()
		userData, err := fetchUserData(db, userId)
		if err != nil {
			errorChan <- fmt.Errorf("failed to get user data: %w", err)
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
			errorChan <- fmt.Errorf("failed to get board data: %w", err)
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
			errorChan <- fmt.Errorf("failed to get boardgroup data: %w", err)
			return
		}
		boardGroupChan <- boardGroupData
	}()

	// รอให้ board data เสร็จก่อน เพื่อเอา board IDs
	var userData map[string]interface{}
	var boardData []map[string]interface{}
	var boardGroupData []map[string]interface{}

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
	userData = <-userChan
	boardData = <-boardChan
	boardGroupData = <-boardGroupChan

	// 4. Fetch tasks data (ต้องรอ board data ก่อน)
	allBoardIDs := extractBoardIDs(boardData, boardGroupData)

	var wg2 sync.WaitGroup

	wg2.Add(1)
	go func() {
		defer wg2.Done()
		tasksData, err := fetchTasksDataOptimized(db, allBoardIDs)
		if err != nil {
			errorChan <- fmt.Errorf("failed to get tasks data: %w", err)
			return
		}
		tasksChan <- tasksData
	}()

	// 5. Fetch today tasks from Firestore
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		todayTasks, err := fetchTasks(ctx, firestoreClient, userData["Email"].(string))
		if err != nil {
			errorChan <- fmt.Errorf("failed to fetch today tasks: %w", err)
			return
		}
		todayTasksChan <- todayTasks
	}()

	wg2.Wait()

	// ตรวจสอบ error อีกครั้ง
	select {
	case err := <-errorChan:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	default:
	}

	tasks := <-tasksChan
	todayTasks := <-todayTasksChan

	// Sort today tasks
	sortTasksByCreatedAt(todayTasks)

	c.JSON(http.StatusOK, gin.H{
		"user":       userData,
		"board":      boardData,
		"boardgroup": boardGroupData,
		"tasks":      tasks,
		"todaytasks": todayTasks,
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
	var boardData []model.Board
	if err := db.Raw(`SELECT 
			b.board_id,
			b.board_name,
			b.create_at,
			b.create_by
		FROM board b
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
		})
	}

	return board, nil
}

func fetchBoardGroupData(db *gorm.DB, userId uint) ([]map[string]interface{}, error) {
	var boardGroupData []struct {
		model.Board
		Token     string    `gorm:"column:token"`
		ExpiresAt time.Time `gorm:"column:expires_at"`
	}
	if err := db.Raw(`SELECT 
			b.board_id,
			b.board_name,
			b.create_at,
			b.create_by,
			bu.added_at as joined_at,
			bt.token,
			bt.expires_at
		FROM board b
		INNER JOIN board_user bu ON b.board_id = bu.board_id
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
			"ExpiresAt": bg.ExpiresAt,
		})
	}

	return boardgroup, nil
}

func extractBoardIDs(boardData, boardGroupData []map[string]interface{}) []int {
	allBoardIDs := make([]int, 0, len(boardData)+len(boardGroupData))

	for _, b := range boardData {
		if boardID, ok := b["BoardID"].(int); ok {
			allBoardIDs = append(allBoardIDs, boardID)
		}
	}
	for _, bg := range boardGroupData {
		if boardID, ok := bg["BoardID"].(int); ok {
			allBoardIDs = append(allBoardIDs, boardID)
		}
	}

	return allBoardIDs
}

func fetchTasksDataOptimized(db *gorm.DB, allBoardIDs []int) ([]map[string]interface{}, error) {
	if len(allBoardIDs) == 0 {
		return make([]map[string]interface{}, 0), nil
	}

	// Fetch all tasks
	var tasksData []model.Tasks
	if err := db.Raw(`SELECT 
			task_id, board_id, task_name, description, 
			status, priority, create_by, create_at
		FROM tasks 
		WHERE board_id IN (?)`, allBoardIDs).Scan(&tasksData).Error; err != nil {
		return nil, err
	}

	if len(tasksData) == 0 {
		return make([]map[string]interface{}, 0), nil
	}

	// Extract all task IDs
	taskIDs := make([]int, len(tasksData))
	for i, task := range tasksData {
		taskIDs[i] = task.TaskID
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
			errorChan <- err
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
	assignedByTask := groupAssignedByTask(relatedData.Assigned)

	// Build result
	tasks := make([]map[string]interface{}, 0, len(tasksData))
	for _, task := range tasksData {
		taskMap := map[string]interface{}{
			"TaskID":      task.TaskID,
			"BoardID":     task.BoardID,
			"TaskName":    task.TaskName,
			"Description": task.Description,
			"Status":      task.Status,
			"Priority":    task.Priority,
			"CreateBy":    task.CreateBy,
			"CreatedAt":   task.CreateAt,
			"Checklists":  buildChecklistsMap(checklistsByTask[task.TaskID]),
			"Attachments": buildAttachmentsMap(attachmentsByTask[task.TaskID]),
			"Assigned":    buildAssignedMap(assignedByTask[task.TaskID]),
		}
		tasks = append(tasks, taskMap)
	}

	return tasks, nil
}

func fetchAllRelatedData(db *gorm.DB, taskIDs []int) (TaskRelatedData, error) {
	var wg sync.WaitGroup
	var checklists []model.Checklist
	var attachments []model.Attachment
	var assigned []struct {
		model.Assigned
		UserName string `gorm:"column:user_name"`
		Email    string `gorm:"column:email"`
	}
	errorChan := make(chan error, 3)

	// Fetch checklists
	wg.Add(1)
	go func() {
		defer wg.Done()
		var checklistsData []model.Checklist
		if err := db.Raw(`SELECT checklist_id, task_id, checklist_name, create_at 
			FROM checklists WHERE task_id IN (?)`, taskIDs).Scan(&checklistsData).Error; err != nil {
			errorChan <- fmt.Errorf("failed to fetch checklists: %w", err)
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
			errorChan <- fmt.Errorf("failed to fetch attachments: %w", err)
			return
		}
		attachments = attachmentsData
	}()

	// Fetch assigned users
	wg.Add(1)
	go func() {
		defer wg.Done()
		var assignedData []struct {
			model.Assigned
			UserName string `gorm:"column:user_name"`
			Email    string `gorm:"column:email"`
		}
		if err := db.Raw(`SELECT a.ass_id, a.task_id, a.user_id, a.assign_at, u.name as user_name, u.email
			FROM assigned a
			LEFT JOIN user u ON a.user_id = u.user_id
			WHERE a.task_id IN (?)`, taskIDs).Scan(&assignedData).Error; err != nil {
			errorChan <- fmt.Errorf("failed to fetch assigned users: %w", err)
			return
		}
		assigned = assignedData
	}()

	wg.Wait()

	select {
	case err := <-errorChan:
		return TaskRelatedData{}, err
	default:
	}

	return TaskRelatedData{
		Checklists:  checklists,
		Attachments: attachments,
		Assigned:    assigned,
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

func groupAssignedByTask(assigned []struct {
	model.Assigned
	UserName string `gorm:"column:user_name"`
	Email    string `gorm:"column:email"`
}) map[int][]struct {
	model.Assigned
	UserName string `gorm:"column:user_name"`
	Email    string `gorm:"column:email"`
} {
	result := make(map[int][]struct {
		model.Assigned
		UserName string `gorm:"column:user_name"`
		Email    string `gorm:"column:email"`
	})
	for _, assign := range assigned {
		result[assign.TaskID] = append(result[assign.TaskID], assign)
	}
	return result
}

func buildChecklistsMap(checklists []model.Checklist) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(checklists))
	for _, checklist := range checklists {
		result = append(result, map[string]interface{}{
			"ChecklistID":   checklist.ChecklistID,
			"TaskID":        checklist.TaskID,
			"ChecklistName": checklist.ChecklistName,
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

func buildAssignedMap(assigned []struct {
	model.Assigned
	UserName string `gorm:"column:user_name"`
	Email    string `gorm:"column:email"`
}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(assigned))
	for _, assign := range assigned {
		result = append(result, map[string]interface{}{
			"AssID":    assign.AssID,
			"TaskID":   assign.TaskID,
			"UserID":   assign.UserID,
			"AssignAt": assign.AssignAt,
			"UserName": assign.UserName,
			"Email":    assign.Email,
		})
	}
	return result
}

// Firestore functions remain the same but with minor optimizations
func fetchTasks(ctx context.Context, client *firestore.Client, userEmail string) ([]map[string]interface{}, error) {
	taskDocs, err := client.Collection("TodayTasks").Doc(userEmail).Collection("tasks").Documents(ctx).GetAll()
	if err != nil {
		return make([]map[string]interface{}, 0), nil
	}

	if len(taskDocs) == 0 {
		return make([]map[string]interface{}, 0), nil
	}

	tasks := make([]map[string]interface{}, 0, len(taskDocs))
	var wg sync.WaitGroup
	tasksChan := make(chan map[string]interface{}, len(taskDocs))

	// Use worker pool to limit goroutines
	workerCount := 10
	if len(taskDocs) < workerCount {
		workerCount = len(taskDocs)
	}

	taskQueue := make(chan *firestore.DocumentSnapshot, len(taskDocs))

	// Start workers
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for doc := range taskQueue {
				taskData := processTaskDocument(ctx, client, userEmail, doc)
				tasksChan <- taskData
			}
		}()
	}

	// Send tasks to queue
	for _, doc := range taskDocs {
		taskQueue <- doc
	}
	close(taskQueue)

	wg.Wait()
	close(tasksChan)

	for task := range tasksChan {
		tasks = append(tasks, task)
	}

	return tasks, nil
}

func processTaskDocument(ctx context.Context, client *firestore.Client, userEmail string, doc *firestore.DocumentSnapshot) map[string]interface{} {
	taskData := doc.Data()
	taskID := doc.Ref.ID

	// Fetch subcollections concurrently
	var wg sync.WaitGroup
	attachmentsChan := make(chan []map[string]interface{}, 1)
	checklistsChan := make(chan []map[string]interface{}, 1)

	wg.Add(2)

	// Attachments
	go func() {
		defer wg.Done()
		items := getSubcollectionOptimized(ctx, client, fmt.Sprintf("TodayTasks/%s/tasks/%s/Attachments", userEmail, taskID))
		attachmentsChan <- items
	}()

	// Checklists
	go func() {
		defer wg.Done()
		items := getSubcollectionOptimized(ctx, client, fmt.Sprintf("TodayTasks/%s/tasks/%s/Checklists", userEmail, taskID))
		checklistsChan <- items
	}()

	wg.Wait()

	taskData["Attachments"] = <-attachmentsChan
	taskData["Checklists"] = <-checklistsChan

	return taskData
}

func getSubcollectionOptimized(ctx context.Context, client *firestore.Client, collectionPath string) []map[string]interface{} {
	docs, err := client.Collection(collectionPath).Documents(ctx).GetAll()
	if err != nil {
		return make([]map[string]interface{}, 0)
	}

	if len(docs) == 0 {
		return make([]map[string]interface{}, 0)
	}

	items := make([]map[string]interface{}, 0, len(docs))
	for _, d := range docs {
		items = append(items, d.Data())
	}
	return items
}

func sortTasksByCreatedAt(tasks []map[string]interface{}) {
	sort.Slice(tasks, func(i, j int) bool {
		timeI := getTimeFromInterface(tasks[i]["CreatedAt"])
		timeJ := getTimeFromInterface(tasks[j]["CreatedAt"])
		return timeI.Before(timeJ)
	})

	// Sort subcollections in each task
	for _, task := range tasks {
		if attachments, ok := task["Attachments"].([]map[string]interface{}); ok {
			sortAttachments(attachments)
		}
		if checklists, ok := task["Checklists"].([]map[string]interface{}); ok {
			sortChecklists(checklists)
		}
		if assigned, ok := task["Assigned"].([]map[string]interface{}); ok {
			sortAssigned(assigned)
		}
	}
}

func getTimeFromInterface(timeInterface interface{}) time.Time {
	switch t := timeInterface.(type) {
	case time.Time:
		return t
	case string:
		if parsedTime, err := time.Parse(time.RFC3339, t); err == nil {
			return parsedTime
		}
		return time.Time{}
	default:
		return time.Time{}
	}
}

func sortAttachments(attachments []map[string]interface{}) {
	sort.Slice(attachments, func(i, j int) bool {
		timeI := getTimeFromInterface(attachments[i]["UploadAt"])
		timeJ := getTimeFromInterface(attachments[j]["UploadAt"])
		return timeI.Before(timeJ)
	})
}

func sortChecklists(checklists []map[string]interface{}) {
	sort.Slice(checklists, func(i, j int) bool {
		timeI := getTimeFromInterface(checklists[i]["CreatedAt"])
		timeJ := getTimeFromInterface(checklists[j]["CreatedAt"])
		return timeI.Before(timeJ)
	})
}

func sortAssigned(assigned []map[string]interface{}) {
	sort.Slice(assigned, func(i, j int) bool {
		timeI := getTimeFromInterface(assigned[i]["AssignAt"])
		timeJ := getTimeFromInterface(assigned[j]["AssignAt"])
		return timeI.Before(timeJ)
	})
}
