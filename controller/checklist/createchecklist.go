package checklist

import (
	"context"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func CreateChecklistController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/checklist/:taskid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		Checklist(c, db, firestoreClient)
	})
}

func Checklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	ctx := context.Background()
	userId := c.MustGet("userId").(uint)

	taskIDStr := c.Param("taskid")
	if taskIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Task ID is required"})
		return
	}

	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	var req dto.CreateChecklistTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var task struct {
		TaskID   int  `db:"task_id"`
		BoardID  *int `db:"board_id"`
		CreateBy *int `db:"create_by"`
	}

	if err := db.Table("tasks").
		Select("task_id, board_id, create_by").
		Where("task_id = ?", taskID).
		First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch task"})
		}
		return
	}

	canDelete := false

	if task.BoardID == nil {
		if task.CreateBy != nil && *task.CreateBy == int(userId) {
			canDelete = true
		}
	} else {
		if task.CreateBy != nil && *task.CreateBy == int(userId) {
			canDelete = true
		} else {
			var count int64
			query := `
				SELECT COUNT(1)
				FROM tasks t
				JOIN board_user bu ON t.board_id = bu.board_id
				WHERE t.task_id = ? AND bu.user_id = ?
			`

			if err := db.Raw(query, taskID, userId).Count(&count).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board access"})
				return
			}

			if count > 0 {
				canDelete = true
			}
		}
	}

	if !canDelete {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	var checklist model.Checklist

	err = db.Transaction(func(tx *gorm.DB) error {
		checklist = model.Checklist{
			TaskID:        taskID,
			ChecklistName: req.ChecklistName,
			Status:        "0",
			CreateAt:      time.Now(),
		}

		if err := tx.Create(&checklist).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create checklist"})
		return
	}

	// ✅ ตรวจสอบสมาชิก board และส่งข้อมูลไป Firestore ถ้ามีสมาชิก
	if task.BoardID != nil {
		var count int64
		err := db.Model(&model.BoardUser{}).
			Where("board_id = ?", *task.BoardID).
			Count(&count).Error

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error while verifying board users"})
			return
		}

		if count == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No board users found for this board"})
			return
		}

		// ✅ ถ้ามีสมาชิกในบอร์ด ให้บันทึกลง Firestore
		_, err = firestoreClient.Collection("Checklist").Doc(strconv.Itoa(int(checklist.ChecklistID))).Set(ctx, map[string]interface{}{
			"checklist_id":   checklist.ChecklistID,
			"task_id":        checklist.TaskID,
			"checklist_name": checklist.ChecklistName,
			"status":         checklist.Status,
			"create_at":      checklist.CreateAt,
		})

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save checklist to Firestore"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Checklist created successfully",
		"checklist_id": checklist.ChecklistID,
	})
}
