package user

import (
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/api/iterator"
	"gorm.io/gorm"
)

func UserController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/user", middleware.AccessTokenMiddleware())
	{
		routes.GET("/alldata", func(c *gin.Context) {
			GetAllDataFirebase(c, db, firestoreClient)
		})
		routes.GET("/ReadAllUser", func(c *gin.Context) {
			ReadAllUser(c, db)
		})
		routes.GET("/Profile", func(c *gin.Context) {
			Profile(c, db)
		})
		routes.GET("/ProfileAdmin", middleware.AdminMiddleware(), func(c *gin.Context) {
			Profile(c, db)
		})
		routes.GET("/AlldataUser", func(c *gin.Context) {
			GetUserAllData(c, db, firestoreClient)
		})
		routes.PUT("/updateprofile", func(c *gin.Context) {
			UpdateProfileUser(c, db, firestoreClient)
		})
		routes.DELETE("/account", func(c *gin.Context) {
			DeleteUser(c, db, firestoreClient)
		})
	}
	router.POST("/email", func(c *gin.Context) {
		EmailData(c, db)
	})
}

func ReadAllUser(c *gin.Context, db *gorm.DB) {

	var users []model.User
	result := db.Find(&users)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	c.JSON(http.StatusOK, users)
}

func Profile(c *gin.Context, db *gorm.DB) {
	userId := c.MustGet("userId").(uint)
	var user model.User
	result := db.First(&user, userId)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	c.JSON(200, gin.H{"user": user})
}

func EmailData(c *gin.Context, db *gorm.DB) {
	var email dto.EmailRequest
	if err := c.ShouldBindJSON(&email); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	var user model.User
	result := db.First(&user, email)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	response := gin.H{
		"UserID": user.UserID,
		"Email":  user.Email,
	}
	c.JSON(http.StatusOK, response)
}

func UpdateProfileUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var updateProfile dto.UpdateProfileRequest
	if err := c.ShouldBindJSON(&updateProfile); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	var user model.User
	result := db.First(&user, userId)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	if updateProfile.HashedPassword != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(updateProfile.HashedPassword), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}
		updateProfile.HashedPassword = string(hashedPassword)
	}

	updates := map[string]interface{}{
		"name":            updateProfile.Name,
		"hashed_password": updateProfile.HashedPassword,
		"profile":         updateProfile.Profile,
	}

	updateMap := make(map[string]interface{})
	for key, value := range updates {
		if value != "" {
			updateMap[key] = value
		}
	}

	if err := db.Model(&user).Updates(updateMap).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Profile updated successfully"})
}

func DeleteUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	const checkSql = `
        SELECT DISTINCT *
        FROM user
        LEFT JOIN board ON user.user_id = board.create_by
        LEFT JOIN board_user ON user.user_id = board_user.user_id
        WHERE user.user_id = ?
            AND (board.board_id IS NOT NULL OR board_user.board_id IS NOT NULL)
    `
	var results []map[string]interface{}
	if err := db.Raw(checkSql, userId).Scan(&results).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check user associations"})
		return
	}

	//เช็คอีเมลก่อนว่ามีบอร์ดงานไหมถ้ามีไม่ให้ลบ ถ้าไม่มีลบเลย
	if len(results) > 0 {
		const updateSql = `
                UPDATE user
                SET is_active = "2"
                WHERE user_id = ?;`
		if err := db.Exec(updateSql, userId).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to deactivate user"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User deactivated successfully"})
	} else {
		const deleteSql = `
                DELETE FROM user
                WHERE user_id = ?;`
		if err := db.Exec(deleteSql, userId).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User deleted successfully"})
	}
}

func GetUserAllData(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	// Get user profile with only required fields
	var user struct {
		UserID    uint      `json:"UserID"`
		Name      string    `json:"Name"`
		Email     string    `json:"Email"`
		Profile   string    `json:"Profile"`
		Role      string    `json:"Role"`
		CreatedAt time.Time `json:"CreatedAt"`
	}
	if err := db.Table("user").
		Select("user_id, name, email, profile, role, create_at").
		Where("user_id = ?", userId).
		Scan(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	// Define Checklist struct without AssignedUser info
	type ChecklistWithUser struct {
		ChecklistID   int       `json:"ChecklistID"`
		ChecklistName string    `json:"ChecklistName"`
		IsArchive     bool      `json:"IsArchive"`
		CreateAt      time.Time `json:"CreateAt"`
	}

	// Define Attachment struct
	type AttachmentData struct {
		AttachmentID int       `json:"AttachmentID"`
		FileName     string    `json:"FileName"`
		FilePath     string    `json:"FilePath"`
		FileType     string    `json:"FileType"`
		UploadAt     time.Time `json:"UploadAt"`
	}

	// Define BoardToken struct
	type BoardTokenData struct {
		TokenID   int       `json:"TokenID"`
		Token     string    `json:"Token"`
		ExpiresAt time.Time `json:"ExpiresAt"`
		CreateAt  time.Time `json:"CreateAt"`
	}

	// Define Assigned struct
	type AssignedData struct {
		AssID    int       `json:"AssID"`
		UserID   int       `json:"UserID"`
		AssignAt time.Time `json:"AssignAt"`
		User     struct {
			UserID  int    `json:"UserID"`
			Name    string `json:"Name"`
			Profile string `json:"Profile"`
		} `json:"User"`
	}

	// Define Task struct with Creator info, Checklist, Attachments and Assigned
	type TaskWithDetails struct {
		TaskID      int       `json:"TaskID"`
		TaskName    string    `json:"TaskName"`
		Description string    `json:"Description"`
		Status      string    `json:"Status"`
		Priority    string    `json:"Priority"`
		CreateAt    time.Time `json:"CreateAt"`
		CreateBy    []struct {
			UserID  int    `json:"UserID"`
			Name    string `json:"Name"`
			Profile string `json:"Profile"`
		} `json:"CreateBy"`
		Checklist   []ChecklistWithUser `json:"checklist"`
		Attachments []AttachmentData    `json:"attachments"`
		Assigned    []AssignedData      `json:"assigned"`
	}

	// Define Board struct with Tasks and BoardTokens
	type BoardWithTasks struct {
		BoardID     int               `json:"BoardID"`
		BoardName   string            `json:"BoardName"`
		CreatedAt   time.Time         `json:"CreatedAt"`
		CreatedBy   uint              `json:"CreatedBy"`
		Tasks       []TaskWithDetails `json:"tasks"`
		BoardTokens []BoardTokenData  `json:"boardtokens"`
	}

	// Helper function to get tasks with details for a board
	getTasksForBoard := func(boardID int, includingAssigned bool) ([]TaskWithDetails, error) {
		// Get tasks for this board
		var tasks []struct {
			TaskID         int       `json:"task_id"`
			TaskName       string    `json:"task_name"`
			Description    string    `json:"description"`
			Status         string    `json:"status"`
			Priority       string    `json:"priority"`
			CreateAt       time.Time `json:"create_at"`
			CreateBy       int       `json:"create_by"`
			CreatorUserID  int       `json:"creator_user_id"`
			CreatorName    string    `json:"creator_name"`
			CreatorProfile string    `json:"creator_profile"`
		}

		if err := db.Table("tasks").
			Select("tasks.task_id, tasks.task_name, tasks.description, tasks.status, tasks.priority, tasks.create_at, tasks.create_by, user.user_id as creator_user_id, user.name as creator_name, user.profile as creator_profile").
			Joins("LEFT JOIN user ON tasks.create_by = user.user_id").
			Where("tasks.board_id = ?", boardID).
			Scan(&tasks).Error; err != nil {
			return []TaskWithDetails{}, err // Return empty array instead of nil
		}

		// Initialize as empty array to prevent null
		taskDetails := make([]TaskWithDetails, 0)

		for _, task := range tasks {
			taskWithDetails := TaskWithDetails{
				TaskID:      task.TaskID,
				TaskName:    task.TaskName,
				Description: task.Description,
				Status:      task.Status,
				Priority:    task.Priority,
				CreateAt:    task.CreateAt,
			}

			// Initialize CreateBy as empty array and add creator info if available
			taskWithDetails.CreateBy = make([]struct {
				UserID  int    `json:"UserID"`
				Name    string `json:"Name"`
				Profile string `json:"Profile"`
			}, 0)

			// Add creator info if available
			if task.CreatorUserID != 0 {
				taskWithDetails.CreateBy = append(taskWithDetails.CreateBy, struct {
					UserID  int    `json:"UserID"`
					Name    string `json:"Name"`
					Profile string `json:"Profile"`
				}{
					UserID:  task.CreatorUserID,
					Name:    task.CreatorName,
					Profile: task.CreatorProfile,
				})
			}

			// Get checklists for this task (without assigned user info)
			var checklists []struct {
				ChecklistID   int       `json:"checklist_id"`
				ChecklistName string    `json:"checklist_name"`
				IsArchive     bool      `json:"is_archive"`
				CreateAt      time.Time `json:"create_at"`
			}

			// Initialize checklist as empty array to prevent null
			taskWithDetails.Checklist = make([]ChecklistWithUser, 0)

			if err := db.Table("checklists").
				Select("checklists.checklist_id, checklists.checklist_name, checklists.is_archive, checklists.create_at").
				Where("checklists.task_id = ?", task.TaskID).
				Scan(&checklists).Error; err != nil {
				// Even if error, keep empty array
				taskWithDetails.Checklist = make([]ChecklistWithUser, 0)
			} else {
				// Convert checklists to ChecklistWithUser format (without AssignedTo)
				for _, checklist := range checklists {
					checklistWithUser := ChecklistWithUser{
						ChecklistID:   checklist.ChecklistID,
						ChecklistName: checklist.ChecklistName,
						IsArchive:     checklist.IsArchive,
						CreateAt:      checklist.CreateAt,
					}
					taskWithDetails.Checklist = append(taskWithDetails.Checklist, checklistWithUser)
				}
			}

			// Get attachments for this task
			var attachments []AttachmentData

			// Initialize attachments as empty array to prevent null
			taskWithDetails.Attachments = make([]AttachmentData, 0)

			if err := db.Table("attachments").
				Select("attachment_id, file_name, file_path, file_type, upload_at").
				Where("tasks_id = ?", task.TaskID).
				Scan(&attachments).Error; err != nil {
				// Even if error, keep empty array
				taskWithDetails.Attachments = make([]AttachmentData, 0)
			} else if len(attachments) > 0 {
				taskWithDetails.Attachments = attachments
			}
			// If no attachments found, it will remain as empty array

			// Get assigned users for this task (only if includingAssigned is true)
			taskWithDetails.Assigned = make([]AssignedData, 0) // Always initialize as empty array

			if includingAssigned {
				var assignedUsers []struct {
					AssID    int       `json:"ass_id"`
					UserID   int       `json:"user_id"`
					AssignAt time.Time `json:"assign_at"`
					UserName string    `json:"user_name"`
					Profile  string    `json:"profile"`
				}

				if err := db.Table("assigned").
					Select("assigned.ass_id, assigned.user_id, assigned.assign_at, user.name as user_name, user.profile").
					Joins("LEFT JOIN user ON assigned.user_id = user.user_id").
					Where("assigned.task_id = ?", task.TaskID).
					Scan(&assignedUsers).Error; err != nil {
					// Even if error, keep empty array
					taskWithDetails.Assigned = make([]AssignedData, 0)
				} else {
					// Convert to AssignedData format
					for _, assigned := range assignedUsers {
						assignedData := AssignedData{
							AssID:    assigned.AssID,
							UserID:   assigned.UserID,
							AssignAt: assigned.AssignAt,
							User: struct {
								UserID  int    `json:"UserID"`
								Name    string `json:"Name"`
								Profile string `json:"Profile"`
							}{
								UserID:  assigned.UserID,
								Name:    assigned.UserName,
								Profile: assigned.Profile,
							},
						}
						taskWithDetails.Assigned = append(taskWithDetails.Assigned, assignedData)
					}
				}
			}

			taskDetails = append(taskDetails, taskWithDetails)
		}

		return taskDetails, nil
	}

	// Helper function to get board tokens for a board
	getBoardTokensForBoard := func(boardID int) ([]BoardTokenData, error) {
		var boardTokens []BoardTokenData

		if err := db.Table("board_token").
			Select("token_id, token, expires_at, create_at").
			Where("board_id = ?", boardID).
			Scan(&boardTokens).Error; err != nil {
			return make([]BoardTokenData, 0), err // Return empty array instead of nil
		}

		// Initialize as empty array to prevent null
		if boardTokens == nil {
			boardTokens = make([]BoardTokenData, 0)
		}

		return boardTokens, nil
	}

	// Initialize arrays as empty to prevent null
	boardGroup := make([]BoardWithTasks, 0)
	boards := make([]BoardWithTasks, 0)

	// Get boards where user is a member (boardgroup)
	var boardGroupData []struct {
		BoardID   int       `json:"BoardID"`
		BoardName string    `json:"BoardName"`
		CreatedAt time.Time `json:"CreatedAt"`
		CreatedBy uint      `json:"CreatedBy"`
	}
	if err := db.Table("board_user").
		Select("board.board_id, board.board_name, board.create_at, board.create_by as created_by").
		Joins("JOIN board ON board_user.board_id = board.board_id").
		Where("board_user.user_id = ?", userId).
		Scan(&boardGroupData).Error; err != nil {
		// Don't return error, just keep empty array
		boardGroupData = make([]struct {
			BoardID   int       `json:"BoardID"`
			BoardName string    `json:"BoardName"`
			CreatedAt time.Time `json:"CreatedAt"`
			CreatedBy uint      `json:"CreatedBy"`
		}, 0)
	}

	// Process boardgroup
	for _, board := range boardGroupData {
		tasks, err := getTasksForBoard(board.BoardID, true) // Include assigned for board groups
		if err != nil {
			// If error getting tasks, use empty array
			tasks = make([]TaskWithDetails, 0)
		}

		// Get board tokens for this board
		boardTokens, err := getBoardTokensForBoard(board.BoardID)
		if err != nil {
			// If error getting board tokens, use empty array
			boardTokens = make([]BoardTokenData, 0)
		}

		boardWithTasks := BoardWithTasks{
			BoardID:     board.BoardID,
			BoardName:   board.BoardName,
			CreatedAt:   board.CreatedAt,
			CreatedBy:   board.CreatedBy,
			Tasks:       tasks,
			BoardTokens: boardTokens,
		}
		boardGroup = append(boardGroup, boardWithTasks)
	}

	// Extract board IDs from boardGroup to exclude them
	boardGroupIds := make([]int, 0)
	for _, board := range boardGroup {
		boardGroupIds = append(boardGroupIds, board.BoardID)
	}

	// Get boards created by the user, excluding those in boardgroup
	var boardsData []struct {
		BoardID   int       `json:"BoardID"`
		BoardName string    `json:"BoardName"`
		CreatedAt time.Time `json:"CreatedAt"`
		CreatedBy uint      `json:"CreatedBy"`
	}

	query := db.Table("board").
		Select("board_id, board_name, create_at, create_by as created_by").
		Where("create_by = ?", userId)

	if len(boardGroupIds) > 0 {
		query = query.Where("board_id NOT IN ?", boardGroupIds)
	}

	if err := query.Scan(&boardsData).Error; err != nil {
		// Don't return error, just keep empty array
		boardsData = make([]struct {
			BoardID   int       `json:"BoardID"`
			BoardName string    `json:"BoardName"`
			CreatedAt time.Time `json:"CreatedAt"`
			CreatedBy uint      `json:"CreatedBy"`
		}, 0)
	}

	// Process boards
	for _, board := range boardsData {
		tasks, err := getTasksForBoard(board.BoardID, false) // Don't include assigned for regular boards
		if err != nil {
			// If error getting tasks, use empty array
			tasks = make([]TaskWithDetails, 0)
		}

		// Note: Regular boards (not board groups) don't need board tokens
		// Only board groups need board tokens as per requirement
		boardWithTasks := BoardWithTasks{
			BoardID:     board.BoardID,
			BoardName:   board.BoardName,
			CreatedAt:   board.CreatedAt,
			CreatedBy:   board.CreatedBy,
			Tasks:       tasks,
			BoardTokens: make([]BoardTokenData, 0), // Empty array for regular boards
		}
		boards = append(boards, boardWithTasks)
	}

	// Get today tasks from Firestore
	todaytasks := make([]map[string]interface{}, 0)

	if firestoreClient != nil && user.Email != "" {
		iter := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Where("archive", "==", false).Documents(c)
		if iter != nil {
			defer iter.Stop()
			for {
				doc, err := iter.Next()
				if err == iterator.Done {
					break
				}
				if err != nil {
					// If error, just keep empty array and continue
					break
				}
				if doc != nil && doc.Data() != nil {
					todaytasks = append(todaytasks, doc.Data())
				}
			}
		}
	}

	// Return the data - all arrays are guaranteed to be empty arrays, never null
	c.JSON(http.StatusOK, gin.H{
		"board":      boards,
		"boardgroup": boardGroup,
		"user":       user,
		"todaytasks": todaytasks,
	})
}

func GetAllDataFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	var user model.User
	if err := db.Raw("SELECT user_id, email, name, profile, create_at FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	userData := map[string]interface{}{
		"UserID":    user.UserID,
		"Email":     user.Email,
		"Name":      user.Name,
		"Profile":   user.Profile,
		"CreatedAt": user.CreatedAt,
	}

	// ===== Helper สำหรับ subcollections =====
	getSubcollection := func(collectionPath string) []map[string]interface{} {
		items := []map[string]interface{}{}
		docs, err := firestoreClient.Collection(collectionPath).Documents(c).GetAll()
		if err != nil {
			// Log error but continue processing
			return items
		}
		for _, d := range docs {
			items = append(items, d.Data())
		}
		return items
	}

	// ===== Tasks =====
	taskDocs, err := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Documents(c).GetAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get tasks from Firestore"})
		return
	}

	tasks := []map[string]interface{}{}
	for _, doc := range taskDocs {
		taskData := doc.Data()

		taskID := doc.Ref.ID
		taskData["Attachments"] = getSubcollection(fmt.Sprintf("TodayTasks/%s/tasks/%s/Attachments", user.Email, taskID))
		taskData["Checklists"] = getSubcollection(fmt.Sprintf("TodayTasks/%s/tasks/%s/Checklists", user.Email, taskID))

		tasks = append(tasks, taskData)
	}

	// ===== Group Boards =====
	groupBoardDocs, err := firestoreClient.Collection("Boards").Doc(user.Email).Collection("Group_Boards").Documents(c).GetAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get Group Boards from Firestore"})
		return
	}

	groupBoards := []map[string]interface{}{}
	for _, boardDoc := range groupBoardDocs {
		boardData := boardDoc.Data()
		boardID := boardDoc.Ref.ID

		// Get tasks for this board
		taskCollectionPath := fmt.Sprintf("Boards/%s/Group_Boards/%s/tasks", user.Email, boardID)
		taskDocs, err := firestoreClient.Collection(taskCollectionPath).Documents(c).GetAll()
		if err == nil {
			boardTasks := []map[string]interface{}{}
			for _, taskDoc := range taskDocs {
				taskData := taskDoc.Data()
				taskID := taskDoc.Ref.ID

				// Add subcollections to each task with correct paths and field names
				taskData["Assigned"] = getSubcollection(fmt.Sprintf("Boards/%s/Group_Boards/%s/tasks/%s/Assigned", user.Email, boardID, taskID))
				taskData["Attachments"] = getSubcollection(fmt.Sprintf("Boards/%s/Group_Boards/%s/tasks/%s/Attachments", user.Email, boardID, taskID))
				taskData["Checklists"] = getSubcollection(fmt.Sprintf("Boards/%s/Group_Boards/%s/tasks/%s/Checklists", user.Email, boardID, taskID))

				boardTasks = append(boardTasks, taskData)
			}
			boardData["tasks"] = boardTasks
		}

		groupBoards = append(groupBoards, boardData)
	}

	// ===== Private Boards =====
	privateBoardDocs, err := firestoreClient.Collection("Boards").Doc(user.Email).Collection("Private_Boards").Documents(c).GetAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get Private Boards from Firestore"})
		return
	}

	privateBoards := []map[string]interface{}{}
	for _, boardDoc := range privateBoardDocs {
		boardData := boardDoc.Data()
		boardID := boardDoc.Ref.ID

		// Get tasks for this board
		taskCollectionPath := fmt.Sprintf("Boards/%s/Private_Boards/%s/tasks", user.Email, boardID)
		taskDocs, err := firestoreClient.Collection(taskCollectionPath).Documents(c).GetAll()
		if err == nil {
			boardTasks := []map[string]interface{}{}
			for _, taskDoc := range taskDocs {
				taskData := taskDoc.Data()
				taskID := taskDoc.Ref.ID

				// Add subcollections to each task with correct paths and field names
				taskData["Attachments"] = getSubcollection(fmt.Sprintf("Boards/%s/Private_Boards/%s/tasks/%s/Attachments", user.Email, boardID, taskID))
				taskData["Checklists"] = getSubcollection(fmt.Sprintf("Boards/%s/Private_Boards/%s/tasks/%s/Checklists", user.Email, boardID, taskID))

				boardTasks = append(boardTasks, taskData)
			}
			boardData["tasks"] = boardTasks
		}

		privateBoards = append(privateBoards, boardData)
	}

	// ===== Response =====
	c.JSON(http.StatusOK, gin.H{
		"user":       userData,
		"todaytasks": tasks,
		"boardgroup": groupBoards,
		"board":      privateBoards,
	})
}
