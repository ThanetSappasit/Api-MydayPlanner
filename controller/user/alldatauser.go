package user

import (
	"mydayplanner/model"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func AllDataUser(c *gin.Context, db *gorm.DB) {
	userId := c.MustGet("userId").(uint)

	var user model.User
	if err := db.Raw("SELECT user_id, email, name, role, profile, is_verify, is_active, create_at FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}
	userData := map[string]interface{}{
		"UserID":    user.UserID,
		"Email":     user.Email,
		"Name":      user.Name,
		"Profile":   user.Profile,
		"Role":      user.Role,
		"IsVerify":  user.IsVerify,
		"IsActive":  user.IsActive,
		"CreatedAt": user.CreatedAt,
	}

	var boardData []model.Board
	if err := db.Raw(`SELECT 
			b.board_id,
			b.board_name,
			b.create_at,
			b.create_by
		FROM board b
		LEFT JOIN board_user bu ON b.board_id = bu.board_id AND bu.user_id = ?
		WHERE bu.board_id IS NULL;`, userId).Scan(&boardData).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get board data"})
		return
	}

	// Initialize as empty slice instead of nil
	board := make([]map[string]interface{}, 0)
	for _, b := range boardData {
		board = append(board, map[string]interface{}{
			"BoardID":   b.BoardID,
			"BoardName": b.BoardName,
			"CreatedAt": b.CreatedAt,
			"CreatedBy": b.CreatedBy,
		})
	}

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get boardgroup data"})
		return
	}

	// Initialize as empty slice instead of nil
	boardgroup := make([]map[string]interface{}, 0)
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

	// Get tasks data for all boards that user has access to
	var allBoardIDs []int
	for _, b := range boardData {
		allBoardIDs = append(allBoardIDs, b.BoardID)
	}
	for _, bg := range boardGroupData {
		allBoardIDs = append(allBoardIDs, bg.BoardID)
	}

	tasks := make([]map[string]interface{}, 0)

	if len(allBoardIDs) > 0 {
		var tasksData []model.Tasks
		if err := db.Raw(`SELECT 
				task_id, board_id, task_name, description, 
				status, priority, create_by, create_at
			FROM tasks 
			WHERE board_id IN (?)`, allBoardIDs).Scan(&tasksData).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get tasks data"})
			return
		}

		for _, task := range tasksData {
			// Get checklists for this task
			var checklistsData []model.Checklist
			checklists := make([]map[string]interface{}, 0)
			if err := db.Raw(`SELECT checklist_id, task_id, checklist_name, create_at 
				FROM checklists WHERE task_id = ?`, task.TaskID).Scan(&checklistsData).Error; err == nil {
				for _, checklist := range checklistsData {
					checklists = append(checklists, map[string]interface{}{
						"ChecklistID":   checklist.ChecklistID,
						"TaskID":        checklist.TaskID,
						"ChecklistName": checklist.ChecklistName,
						"CreatedAt":     checklist.CreateAt,
					})
				}
			}

			// Get attachments for this task
			var attachmentsData []model.Attachment
			attachments := make([]map[string]interface{}, 0)
			if err := db.Raw(`SELECT attachment_id, tasks_id, file_name, file_path, file_type, upload_at 
				FROM attachments WHERE tasks_id = ?`, task.TaskID).Scan(&attachmentsData).Error; err == nil {
				for _, attachment := range attachmentsData {
					attachments = append(attachments, map[string]interface{}{
						"AttachmentID": attachment.AttachmentID,
						"TasksID":      attachment.TasksID,
						"FileName":     attachment.FileName,
						"FilePath":     attachment.FilePath,
						"FileType":     attachment.FileType,
						"UploadAt":     attachment.UploadAt,
					})
				}
			}

			// Get assigned users for this task
			var assignedData []struct {
				model.Assigned
				UserName string `gorm:"column:user_name"`
				Email    string `gorm:"column:email"`
			}
			assigned := make([]map[string]interface{}, 0)
			if err := db.Raw(`SELECT a.ass_id, a.task_id, a.user_id, a.assign_at, u.name as user_name, u.email
				FROM assigned a
				LEFT JOIN user u ON a.user_id = u.user_id
				WHERE a.task_id = ?`, task.TaskID).Scan(&assignedData).Error; err == nil {
				for _, assign := range assignedData {
					assigned = append(assigned, map[string]interface{}{
						"AssID":    assign.AssID,
						"TaskID":   assign.TaskID,
						"UserID":   assign.UserID,
						"AssignAt": assign.AssignAt,
						"UserName": assign.UserName,
						"Email":    assign.Email,
					})
				}
			}

			// Add task with related data
			tasks = append(tasks, map[string]interface{}{
				"TaskID":      task.TaskID,
				"BoardID":     task.BoardID,
				"TaskName":    task.TaskName,
				"Description": task.Description,
				"Status":      task.Status,
				"Priority":    task.Priority,
				"CreateBy":    task.CreateBy,
				"CreatedAt":   task.CreateAt,
				"Checklists":  checklists,
				"Attachments": attachments,
				"Assigned":    assigned,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"user":       userData,
		"board":      board,
		"boardgroup": boardgroup,
		"tasks":      tasks,
	})
}
