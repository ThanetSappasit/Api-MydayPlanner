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

func GetAllDataFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	// === ดึงข้อมูล User ===
	var user model.User
	if err := db.Raw("SELECT user_id, email, name, role, profile, create_at FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	userData := map[string]interface{}{
		"UserID":    user.UserID,
		"Email":     user.Email,
		"Name":      user.Name,
		"Profile":   user.Profile,
		"Role":      user.Role,
		"CreatedAt": user.CreatedAt,
	}

	// === Concurrent Data Fetching ===
	var wg sync.WaitGroup
	results := make(chan BatchResult, 2)
	ctx := c.Request.Context()

	// Goroutine 1: Tasks
	wg.Add(1)
	go func() {
		defer wg.Done()
		tasks, err := fetchTasks(ctx, firestoreClient, user.Email)
		results <- BatchResult{Tasks: tasks, Error: err}
	}()

	// Goroutine 2: All Boards (will be separated by Type later)
	wg.Add(1)
	go func() {
		defer wg.Done()
		groupBoards, privateBoards, err := fetchAllBoards(ctx, firestoreClient, user.Email)
		results <- BatchResult{GroupBoards: groupBoards, PrivateBoards: privateBoards, Error: err}
	}()

	// รอให้ทุก goroutine เสร็จ
	go func() {
		wg.Wait()
		close(results)
	}()

	// รวบรวมผลลัพธ์ - Initialize เป็น empty array แทน nil
	tasks := make([]map[string]interface{}, 0)
	groupBoards := make([]map[string]interface{}, 0)
	privateBoards := make([]map[string]interface{}, 0)

	for result := range results {
		if result.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to fetch data: %v", result.Error),
			})
			return
		}
		if result.Tasks != nil {
			tasks = result.Tasks
		}
		if result.GroupBoards != nil {
			groupBoards = result.GroupBoards
		}
		if result.PrivateBoards != nil {
			privateBoards = result.PrivateBoards
		}
	}

	// === เรียงลำดับข้อมูล ===
	sortTasksByCreatedAt(tasks)
	sortBoardsByCreatedAt(groupBoards)
	sortBoardsByCreatedAt(privateBoards)

	// === Response ===
	c.JSON(http.StatusOK, gin.H{
		"user":       userData,
		"todaytasks": tasks,
		"boardgroup": groupBoards,
		"board":      privateBoards,
	})
}

// ฟังก์ชันเรียงลำดับ Tasks ตาม CreatedAt (เก่าสุดก่อน)
func sortTasksByCreatedAt(tasks []map[string]interface{}) {
	sort.Slice(tasks, func(i, j int) bool {
		timeI := getTimeFromInterface(tasks[i]["CreatedAt"])
		timeJ := getTimeFromInterface(tasks[j]["CreatedAt"])
		return timeI.Before(timeJ)
	})

	// เรียงลำดับ subcollections ในแต่ละ task
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

// ฟังก์ชันเรียงลำดับ Boards ตาม CreatedAt (เก่าสุดก่อน)
func sortBoardsByCreatedAt(boards []map[string]interface{}) {
	sort.Slice(boards, func(i, j int) bool {
		timeI := getTimeFromInterface(boards[i]["CreatedAt"])
		timeJ := getTimeFromInterface(boards[j]["CreatedAt"])
		return timeI.Before(timeJ)
	})

	// เรียงลำดับ Tasks ในแต่ละ Board
	for _, board := range boards {
		if tasks, ok := board["Tasks"].([]map[string]interface{}); ok {
			sortTasksByCreatedAt(tasks)
		}
	}
}

// ฟังก์ชันเรียงลำดับ Attachments ตาม UploadAt (เก่าสุดก่อน)
func sortAttachments(attachments []map[string]interface{}) {
	sort.Slice(attachments, func(i, j int) bool {
		timeI := getTimeFromInterface(attachments[i]["UploadAt"])
		timeJ := getTimeFromInterface(attachments[j]["UploadAt"])
		return timeI.Before(timeJ)
	})
}

// ฟังก์ชันเรียงลำดับ Checklists ตาม CreatedAt (เก่าสุดก่อน)
func sortChecklists(checklists []map[string]interface{}) {
	sort.Slice(checklists, func(i, j int) bool {
		timeI := getTimeFromInterface(checklists[i]["CreatedAt"])
		timeJ := getTimeFromInterface(checklists[j]["CreatedAt"])
		return timeI.Before(timeJ)
	})
}

// ฟังก์ชันเรียงลำดับ Assigned ตาม assignedAt (เก่าสุดก่อน)
func sortAssigned(assigned []map[string]interface{}) {
	sort.Slice(assigned, func(i, j int) bool {
		timeI := getTimeFromInterface(assigned[i]["assignedAt"])
		timeJ := getTimeFromInterface(assigned[j]["assignedAt"])
		return timeI.Before(timeJ)
	})
}

// ฟังก์ชันแปลง interface{} เป็น time.Time
func getTimeFromInterface(timeInterface interface{}) time.Time {
	if timeInterface == nil {
		return time.Time{} // return zero time if nil
	}

	switch t := timeInterface.(type) {
	case string:
		// พยายามแปลง string เป็น time
		if parsedTime, err := time.Parse(time.RFC3339, t); err == nil {
			return parsedTime
		}
		// ถ้าแปลงไม่ได้ให้ลองรูปแบบอื่น
		if parsedTime, err := time.Parse("2006-01-02T15:04:05.999999Z", t); err == nil {
			return parsedTime
		}
		// ถ้าแปลงไม่ได้เลย return zero time
		return time.Time{}
	case time.Time:
		return t
	default:
		return time.Time{} // return zero time for unknown types
	}
}

func fetchTasks(ctx context.Context, client *firestore.Client, userEmail string) ([]map[string]interface{}, error) {
	taskDocs, err := client.Collection("TodayTasks").Doc(userEmail).Collection("tasks").Documents(ctx).GetAll()
	if err != nil {
		return make([]map[string]interface{}, 0), nil // Return empty array instead of nil
	}

	if len(taskDocs) == 0 {
		return make([]map[string]interface{}, 0), nil
	}

	tasks := make([]map[string]interface{}, 0, len(taskDocs))
	var wg sync.WaitGroup
	tasksChan := make(chan map[string]interface{}, len(taskDocs))

	// Concurrent subcollection fetching
	for _, doc := range taskDocs {
		wg.Add(1)
		go func(doc *firestore.DocumentSnapshot) {
			defer wg.Done()

			taskData := doc.Data()
			taskID := doc.Ref.ID

			// Fetch subcollections concurrently
			var subWg sync.WaitGroup
			attachmentsChan := make(chan []map[string]interface{}, 1)
			checklistsChan := make(chan []map[string]interface{}, 1)

			subWg.Add(2)

			// Attachments
			go func() {
				defer subWg.Done()
				items := getSubcollectionOptimized(ctx, client, fmt.Sprintf("TodayTasks/%s/tasks/%s/Attachments", userEmail, taskID))
				attachmentsChan <- items
			}()

			// Checklists
			go func() {
				defer subWg.Done()
				items := getSubcollectionOptimized(ctx, client, fmt.Sprintf("TodayTasks/%s/tasks/%s/Checklists", userEmail, taskID))
				checklistsChan <- items
			}()

			subWg.Wait()
			close(attachmentsChan)
			close(checklistsChan)

			taskData["Attachments"] = <-attachmentsChan
			taskData["Checklists"] = <-checklistsChan
			tasksChan <- taskData
		}(doc)
	}

	wg.Wait()
	close(tasksChan)

	for task := range tasksChan {
		tasks = append(tasks, task)
	}

	return tasks, nil
}

// แก้ไขฟังก์ชันใหม่: ดึงบอร์ดทั้งหมดแล้วแยกตาม Type
func fetchAllBoards(ctx context.Context, client *firestore.Client, userEmail string) ([]map[string]interface{}, []map[string]interface{}, error) {
	// ดึง Boards ทั้งหมดจาก collection เดียว - ใช้ path ที่ถูกต้อง
	boardDocs, err := client.Collection("Boards").Doc(userEmail).Collection("Boards").Documents(ctx).GetAll()
	if err != nil {
		return make([]map[string]interface{}, 0), make([]map[string]interface{}, 0), err
	}

	if len(boardDocs) == 0 {
		return make([]map[string]interface{}, 0), make([]map[string]interface{}, 0), nil
	}

	var wg sync.WaitGroup
	boardsChan := make(chan BoardWithType, len(boardDocs))

	// Process แต่ละ board concurrently
	for _, boardDoc := range boardDocs {
		wg.Add(1)
		go func(boardDoc *firestore.DocumentSnapshot) {
			defer wg.Done()

			boardData := boardDoc.Data()
			boardID := boardDoc.Ref.ID

			// ตรวจสอบ Type field
			boardType, ok := boardData["Type"].(string)
			if !ok {
				boardType = "Private" // default เป็น Private ถ้าไม่มี Type
			}

			// Fetch board tasks - ใช้ path ที่ถูกต้อง
			boardTasks := fetchBoardTasks(ctx, client, userEmail, boardID, boardType)
			boardData["Tasks"] = boardTasks

			// เอา Type ออกจาก response data
			delete(boardData, "Type")

			boardsChan <- BoardWithType{
				Data: boardData,
				Type: boardType,
			}
		}(boardDoc)
	}

	wg.Wait()
	close(boardsChan)

	// แยกบอร์ดตาม Type - Initialize เป็น empty arrays
	groupBoards := make([]map[string]interface{}, 0)
	privateBoards := make([]map[string]interface{}, 0)

	for boardWithType := range boardsChan {
		if boardWithType.Type == "Group" {
			groupBoards = append(groupBoards, boardWithType.Data)
		} else {
			privateBoards = append(privateBoards, boardWithType.Data)
		}
	}

	return groupBoards, privateBoards, nil
}

func fetchBoardTasks(ctx context.Context, client *firestore.Client, userEmail, boardID, boardType string) []map[string]interface{} {
	// ใช้ path ที่ถูกต้องตามที่คุณกำหนด: /Boards/{userEmail}/Boards/{boardID}/Tasks
	taskDocs, err := client.Collection("Boards").Doc(userEmail).Collection("Boards").Doc(boardID).Collection("Tasks").Documents(ctx).GetAll()
	if err != nil {
		return make([]map[string]interface{}, 0) // Return empty array instead of nil
	}

	if len(taskDocs) == 0 {
		return make([]map[string]interface{}, 0)
	}

	tasks := make([]map[string]interface{}, 0, len(taskDocs))
	var wg sync.WaitGroup
	tasksChan := make(chan map[string]interface{}, len(taskDocs))

	for _, taskDoc := range taskDocs {
		wg.Add(1)
		go func(taskDoc *firestore.DocumentSnapshot) {
			defer wg.Done()

			taskData := taskDoc.Data()
			taskID := taskDoc.Ref.ID

			// Concurrent subcollection fetching
			var subWg sync.WaitGroup
			attachmentsChan := make(chan []map[string]interface{}, 1)
			checklistsChan := make(chan []map[string]interface{}, 1)

			subWg.Add(2)

			// Attachments - ใช้ path ที่ถูกต้อง
			go func() {
				defer subWg.Done()
				// Path: /Boards/{userEmail}/Boards/{boardID}/Tasks/{taskID}/Attachments
				items := getSubcollectionOptimized(ctx, client, fmt.Sprintf("Boards/%s/Boards/%s/Tasks/%s/Attachments", userEmail, boardID, taskID))
				attachmentsChan <- items
			}()

			// Checklists - ใช้ path ที่ถูกต้อง
			go func() {
				defer subWg.Done()
				// Path: /Boards/{userEmail}/Boards/{boardID}/Tasks/{taskID}/Checklists
				items := getSubcollectionOptimized(ctx, client, fmt.Sprintf("Boards/%s/Boards/%s/Tasks/%s/Checklists", userEmail, boardID, taskID))
				checklistsChan <- items
			}()

			// เพิ่ม Assigned สำหรับ Group Boards
			var assignedChan chan []map[string]interface{}
			if boardType == "Group" {
				assignedChan = make(chan []map[string]interface{}, 1)
				subWg.Add(1)
				go func() {
					defer subWg.Done()
					// Path: /Boards/{userEmail}/Boards/{boardID}/Tasks/{taskID}/Assigned
					items := getSubcollectionOptimized(ctx, client, fmt.Sprintf("Boards/%s/Boards/%s/Tasks/%s/Assigned", userEmail, boardID, taskID))
					assignedChan <- items
				}()
			}

			subWg.Wait()

			taskData["Attachments"] = <-attachmentsChan
			taskData["Checklists"] = <-checklistsChan
			if assignedChan != nil {
				taskData["Assigned"] = <-assignedChan
				close(assignedChan)
			}

			close(attachmentsChan)
			close(checklistsChan)

			tasksChan <- taskData
		}(taskDoc)
	}

	wg.Wait()
	close(tasksChan)

	for task := range tasksChan {
		tasks = append(tasks, task)
	}

	return tasks
}

func getSubcollectionOptimized(ctx context.Context, client *firestore.Client, collectionPath string) []map[string]interface{} {
	docs, err := client.Collection(collectionPath).Documents(ctx).GetAll()
	if err != nil {
		return make([]map[string]interface{}, 0) // Return empty array instead of nil
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

type BatchResult struct {
	Tasks         []map[string]interface{} `json:"tasks"`
	GroupBoards   []map[string]interface{} `json:"groupBoards"`
	PrivateBoards []map[string]interface{} `json:"privateBoards"`
	Error         error                    `json:"error,omitempty"`
}

type BoardWithType struct {
	Data map[string]interface{}
	Type string
}
