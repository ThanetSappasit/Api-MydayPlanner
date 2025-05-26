package user

import (
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

	// Define Task struct with Creator info, Checklist and Attachments
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
	}

	// Define Board struct with Tasks
	type BoardWithTasks struct {
		BoardID   int               `json:"BoardID"`
		BoardName string            `json:"BoardName"`
		CreatedAt time.Time         `json:"CreatedAt"`
		CreatedBy uint              `json:"CreatedBy"`
		Tasks     []TaskWithDetails `json:"tasks"`
	}

	// Helper function to get tasks with details for a board
	getTasksForBoard := func(boardID int) ([]TaskWithDetails, error) {
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
			return nil, err
		}

		var taskDetails []TaskWithDetails
		for _, task := range tasks {
			taskWithDetails := TaskWithDetails{
				TaskID:      task.TaskID,
				TaskName:    task.TaskName,
				Description: task.Description,
				Status:      task.Status,
				Priority:    task.Priority,
				CreateAt:    task.CreateAt,
				CreateBy: []struct {
					UserID  int    `json:"UserID"`
					Name    string `json:"Name"`
					Profile string `json:"Profile"`
				}{
					{
						UserID:  task.CreatorUserID,
						Name:    task.CreatorName,
						Profile: task.CreatorProfile,
					},
				},
			}

			// Get checklists for this task (without assigned user info)
			var checklists []struct {
				ChecklistID   int       `json:"checklist_id"`
				ChecklistName string    `json:"checklist_name"`
				IsArchive     bool      `json:"is_archive"`
				CreateAt      time.Time `json:"create_at"`
			}

			if err := db.Table("checklists").
				Select("checklists.checklist_id, checklists.checklist_name, checklists.is_archive, checklists.create_at").
				Where("checklists.task_id = ?", task.TaskID).
				Scan(&checklists).Error; err != nil {
				return nil, err
			}

			// Initialize as empty array to prevent null
			taskWithDetails.Checklist = make([]ChecklistWithUser, 0)

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

			// Get attachments for this task
			var attachments []AttachmentData
			if err := db.Table("attachments").
				Select("attachment_id, file_name, file_path, file_type, upload_at").
				Where("tasks_id = ?", task.TaskID).
				Scan(&attachments).Error; err != nil {
				return nil, err
			}

			// Initialize as empty array to prevent null
			taskWithDetails.Attachments = make([]AttachmentData, 0)
			if len(attachments) > 0 {
				taskWithDetails.Attachments = attachments
			}

			taskDetails = append(taskDetails, taskWithDetails)
		}

		return taskDetails, nil
	}

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
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve board groups",
			"error":   err.Error(),
		})
		return
	}

	// Process boardgroup
	var boardGroup []BoardWithTasks
	for _, board := range boardGroupData {
		tasks, err := getTasksForBoard(board.BoardID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to retrieve tasks for board group",
				"error":   err.Error(),
			})
			return
		}

		boardWithTasks := BoardWithTasks{
			BoardID:   board.BoardID,
			BoardName: board.BoardName,
			CreatedAt: board.CreatedAt,
			CreatedBy: board.CreatedBy,
			Tasks:     tasks,
		}
		boardGroup = append(boardGroup, boardWithTasks)
	}

	// Extract board IDs from boardGroup to exclude them
	var boardGroupIds []int
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
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve boards",
			"error":   err.Error(),
		})
		return
	}

	// Process boards
	var boards []BoardWithTasks
	for _, board := range boardsData {
		tasks, err := getTasksForBoard(board.BoardID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to retrieve tasks for board",
				"error":   err.Error(),
			})
			return
		}

		boardWithTasks := BoardWithTasks{
			BoardID:   board.BoardID,
			BoardName: board.BoardName,
			CreatedAt: board.CreatedAt,
			CreatedBy: board.CreatedBy,
			Tasks:     tasks,
		}
		boards = append(boards, boardWithTasks)
	}

	// Ensure arrays are never null
	if boards == nil {
		boards = make([]BoardWithTasks, 0)
	}
	if boardGroup == nil {
		boardGroup = make([]BoardWithTasks, 0)
	}

	iter := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Where("archive", "==", false).Documents(c)
	defer iter.Stop()
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

	// Return the data
	c.JSON(http.StatusOK, gin.H{
		"board":      boards,
		"boardgroup": boardGroup,
		"user":       user,
		"todaytasks": todaytasks,
	})
}
