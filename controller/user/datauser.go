package user

// import (
// 	"fmt"
// 	"mydayplanner/model"
// 	"net/http"
// 	"sync"
// 	"time"

// 	"cloud.google.com/go/firestore"
// 	"github.com/gin-gonic/gin"
// 	"gorm.io/gorm"
// )

// func Datauser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
// 	userId := c.MustGet("userId").(uint)

// 	userChan := make(chan map[string]interface{}, 1)
// 	boardChan := make(chan []map[string]interface{}, 1)
// 	boardGroupChan := make(chan []map[string]interface{}, 1)
// 	tasksChan := make(chan []map[string]interface{}, 1)
// 	errorChan := make(chan error, 5)
// 	var wg sync.WaitGroup

// 	wg.Add(1)
// 	go func() {
// 		defer wg.Done()
// 		userData, err := fetchUserData(db, userId)
// 		if err != nil {
// 			select {
// 			case errorChan <- fmt.Errorf("failed to get user data: %w", err):
// 			default:
// 			}
// 			return
// 		}
// 		userChan <- userData
// 	}()

// 	// รอผลลัพธ์
// 	wg.Wait()

// 	// ตรวจสอบ error
// 	select {
// 	case err := <-errorChan:
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
// 		return
// 	default:
// 	}

// 	wg.Add(1)
// 	go func() {
// 		defer wg.Done()
// 		boardData, err := fetchBoardData(db, userId)
// 		if err != nil {
// 			select {
// 			case errorChan <- fmt.Errorf("failed to get board data: %w", err):
// 			default:
// 			}
// 			return
// 		}
// 		boardChan <- boardData
// 	}()

// 	wg.Add(1)
// 	go func() {
// 		defer wg.Done()
// 		boardGroupData, err := fetchBoardGroupData(db, userId)
// 		if err != nil {
// 			select {
// 			case errorChan <- fmt.Errorf("failed to get boardgroup data: %w", err):
// 			default:
// 			}
// 			return
// 		}
// 		boardGroupChan <- boardGroupData
// 	}()

// 	// รับผลลัพธ์
// 	userData := <-userChan
// 	boardData := <-boardChan
// 	boardGroupData := <-boardGroupChan

// 	allBoardIDs := extractedBoardIDs(boardData, boardGroupData)

// 	var wg2 sync.WaitGroup
// 	wg2.Add(1)
// 	go func() {
// 		defer wg2.Done()
// 		tasksData, err := TasksData(db, allBoardIDs, userId)
// 		if err != nil {
// 			select {
// 			case errorChan <- fmt.Errorf("failed to get tasks data: %w", err):
// 			default:
// 			}
// 			return
// 		}
// 		fmt.Printf("Debug: Found %d tasks\n", len(tasksData)) // Debug log
// 		tasksChan <- tasksData
// 	}()

// 	wg2.Wait()

// 	// ตรวจสอบ error จาก task fetching
// 	select {
// 	case err := <-errorChan:
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
// 		return
// 	default:
// 	}

// 	tasks := <-tasksChan

// 	c.JSON(http.StatusOK, gin.H{
// 		"user":       userData,
// 		"board":      boardData,
// 		"boardgroup": boardGroupData,
// 		"tasks":      tasks,
// 	})
// }

// func UserData(db *gorm.DB, userId uint) (map[string]interface{}, error) {
// 	var user model.User
// 	if err := db.Raw("SELECT user_id, email, name, role, profile, is_verify, is_active, create_at FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
// 		return nil, err
// 	}

// 	return map[string]interface{}{
// 		"UserID":    user.UserID,
// 		"Email":     user.Email,
// 		"Name":      user.Name,
// 		"Profile":   user.Profile,
// 		"Role":      user.Role,
// 		"IsVerify":  user.IsVerify,
// 		"IsActive":  user.IsActive,
// 		"CreatedAt": user.CreatedAt,
// 	}, nil
// }

// func BoardData(db *gorm.DB, userId uint) ([]map[string]interface{}, error) {
// 	var boardData []struct {
// 		BoardID     uint      `gorm:"column:board_id"`
// 		BoardName   string    `gorm:"column:board_name"`
// 		CreatedAt   time.Time `gorm:"column:create_at"`
// 		CreatedBy   int       `gorm:"column:create_by"`
// 		UserName    string    `gorm:"column:name"`
// 		UserEmail   string    `gorm:"column:email"`
// 		UserProfile string    `gorm:"column:profile"`
// 	}

// 	if err := db.Raw(`SELECT
// 			b.board_id,
// 			b.board_name,
// 			b.create_at,
// 			b.create_by,
// 			u.name,
// 			u.email,
// 			u.profile
// 		FROM board b
// 		INNER JOIN user u ON b.create_by = u.user_id
// 		WHERE
// 			b.create_by = ?
// 			AND NOT EXISTS (
// 				SELECT 1 FROM board_user bu
// 				WHERE bu.board_id = b.board_id AND bu.user_id = ?
// 			)`, userId, userId).Scan(&boardData).Error; err != nil {
// 		return nil, err
// 	}

// 	board := make([]map[string]interface{}, 0, len(boardData))
// 	for _, b := range boardData {
// 		board = append(board, map[string]interface{}{
// 			"BoardID":   b.BoardID,
// 			"BoardName": b.BoardName,
// 			"CreatedAt": b.CreatedAt,
// 			"CreatedBy": b.CreatedBy,
// 			"CreatedByUser": map[string]interface{}{
// 				"UserID":  b.CreatedBy,
// 				"Name":    b.UserName,
// 				"Email":   b.UserEmail,
// 				"Profile": b.UserProfile,
// 			},
// 		})
// 	}

// 	return board, nil
// }

// func extractedBoardIDs(boardData, boardGroupData []map[string]interface{}) []uint {
// 	allBoardIDs := make([]uint, 0, len(boardData)+len(boardGroupData))

// 	for _, b := range boardData {
// 		if boardID, ok := b["BoardID"].(uint); ok {
// 			allBoardIDs = append(allBoardIDs, boardID)
// 		}
// 	}
// 	for _, bg := range boardGroupData {
// 		if boardID, ok := bg["BoardID"].(uint); ok {
// 			allBoardIDs = append(allBoardIDs, boardID)
// 		}
// 	}

// 	return allBoardIDs
// }

// func TasksData(db *gorm.DB, allBoardIDs []uint, userId uint) ([]map[string]interface{}, error) {
// 	var tasksData []struct {
// 		TaskID      int       `gorm:"column:task_id"`
// 		BoardID     *int      `gorm:"column:board_id"`
// 		TaskName    string    `gorm:"column:task_name"`
// 		Description *string   `gorm:"column:description"`
// 		Status      string    `gorm:"column:status"`
// 		Priority    *string   `gorm:"column:priority"`
// 		CreateBy    *int      `gorm:"column:create_by"`
// 		CreateAt    time.Time `gorm:"column:create_at"`
// 	}

// 	query := `SELECT
// 		task_id, board_id, task_name, description,
// 		status, priority, create_by, create_at
// 	FROM tasks
// 	WHERE `

// 	var args []interface{}

// 	if len(allBoardIDs) > 0 {
// 		// กรณี Group Boards: แสดงทั้ง tasks ใน boards และ Today tasks ของ user
// 		query += `(board_id IN (?) OR (board_id IS NULL AND create_by = ?))`
// 		args = append(args, allBoardIDs, userId)
// 	} else {
// 		// กรณี Private/Today: แสดงเฉพาะ Today tasks ของ user เท่านั้น
// 		query += `(board_id IS NULL AND create_by = ?)`
// 		args = append(args, userId)
// 	}

// 	if err := db.Raw(query, args...).Scan(&tasksData).Error; err != nil {
// 		return nil, fmt.Errorf("failed to fetch tasks: %w", err)
// 	}

// 	if len(tasksData) == 0 {
// 		return make([]map[string]interface{}, 0), nil
// 	}

// 	// Extract all task IDs
// 	taskIDs := make([]uint, len(tasksData))
// 	for i, task := range tasksData {
// 		taskIDs[i] = uint(task.TaskID)
// 	}

// 	// Fetch all related data in parallel
// 	var wg sync.WaitGroup
// 	relatedDataChan := make(chan TaskRelatedData, 1)
// 	errorChan := make(chan error, 1)

// 	wg.Add(1)
// 	go func() {
// 		defer wg.Done()
// 		relatedData, err := fetchAllRelatedData(db, taskIDs)
// 		if err != nil {
// 			select {
// 			case errorChan <- err:
// 			default:
// 			}
// 			return
// 		}
// 		relatedDataChan <- relatedData
// 	}()

// 	wg.Wait()

// 	select {
// 	case err := <-errorChan:
// 		return nil, err
// 	default:
// 	}

// 	relatedData := <-relatedDataChan

// 	// Group related data by task ID
// 	checklistsByTask := groupChecklistsByTask(relatedData.Checklists)
// 	attachmentsByTask := groupAttachmentsByTask(relatedData.Attachments)
// 	notificationsByTask := groupNotificationsByTask(relatedData.Notifications)
// 	assignedByTask := groupAssignedByTask(relatedData.Assigned)

// 	// Build result
// 	tasks := make([]map[string]interface{}, 0, len(tasksData))
// 	for _, task := range tasksData {
// 		// Handle null board_id - convert to "Today"
// 		var boardDisplay interface{}
// 		if task.BoardID == nil {
// 			boardDisplay = "Today"
// 		} else {
// 			boardDisplay = *task.BoardID
// 		}

// 		// Handle null Description and Priority - convert to empty string
// 		var description string
// 		if task.Description == nil {
// 			description = ""
// 		} else {
// 			description = *task.Description
// 		}

// 		var priority string
// 		if task.Priority == nil {
// 			priority = ""
// 		} else {
// 			priority = *task.Priority
// 		}

// 		taskMap := map[string]interface{}{
// 			"TaskID":        task.TaskID,
// 			"BoardID":       boardDisplay, // This will be "Today" if board_id is null
// 			"TaskName":      task.TaskName,
// 			"Description":   description, // Empty string if null
// 			"Status":        task.Status,
// 			"Priority":      priority, // Empty string if null
// 			"CreateBy":      task.CreateBy,
// 			"CreatedAt":     task.CreateAt,
// 			"Checklists":    buildChecklistsMap(checklistsByTask[task.TaskID]),
// 			"Attachments":   buildAttachmentsMap(attachmentsByTask[task.TaskID]),
// 			"Notifications": buildNotificationsMap(notificationsByTask[task.TaskID]),
// 			"Assigned":      buildAssignedMap(assignedByTask[task.TaskID]),
// 		}
// 		tasks = append(tasks, taskMap)
// 	}

// 	return tasks, nil
// }
