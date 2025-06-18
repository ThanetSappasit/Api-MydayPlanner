package checklist

import (
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
	userId := c.MustGet("userId").(uint)

	taskIDStr := c.Param("taskid")
	if taskIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Task ID is required"})
		return
	}

	// แปลง string ID เป็น int
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

	// ===================
	// ดึงข้อมูล task
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

	// ตรวจสอบสิทธิ์
	canDelete := false

	if task.BoardID == nil {
		// Task ไม่มี board_id - ตรวจสอบ create_by
		if task.CreateBy != nil && *task.CreateBy == int(userId) {
			canDelete = true
		}
	} else {
		// Task มี board_id - ตรวจสอบสิทธิ์แบบ 2 เงื่อนไข
		// 1. ถ้าเป็นคนสร้าง task (create_by = user_id)
		if task.CreateBy != nil && *task.CreateBy == int(userId) {
			canDelete = true
		} else {
			// 2. ถ้าเป็นสมาชิกของ board (ผ่าน board_user)
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

	// Create checklist record
	var checklist model.Checklist

	// Start transaction
	err = db.Transaction(func(tx *gorm.DB) error {
		checklist = model.Checklist{
			TaskID:        taskID,
			ChecklistName: req.ChecklistName,
			Status:        "0",
			CreateAt:      time.Now(),
		}

		// Save to SQL database
		if err := tx.Create(&checklist).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create checklist"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Checklist created successfully",
		"checklist_id": checklist.ChecklistID, // แก้ไขตัวแปรให้ถูกต้อง
	})
}
