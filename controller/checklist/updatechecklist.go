package checklist

import (
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func UpdateChecklistController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.PUT("/checklist/:taskid/:checklistid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		UpdateChecklist(c, db, firestoreClient)
	})
}

func UpdateChecklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	taskIDStr := c.Param("taskid")
	checklistIDStr := c.Param("checklistid")

	// แปลง taskID เป็น int
	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	// แปลง checklistID เป็น int
	checklistID, err := strconv.Atoi(checklistIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid checklist ID"})
		return
	}

	var req dto.UpdateChecklistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// ตรวจสอบว่า checklist นี้เป็นของ task ที่ระบุหรือไม่
	var existingChecklist struct {
		ChecklistID   int    `db:"checklist_id"`
		TaskID        int    `db:"task_id"`
		ChecklistName string `db:"checklist_name"`
	}

	if err := db.Table("checklists").
		Select("checklist_id, task_id, checklist_name").
		Where("checklist_id = ? AND task_id = ?", checklistID, taskID).
		First(&existingChecklist).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Checklist not found or not belong to specified task"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch checklist"})
		}
		return
	}

	// ตรวจสอบสิทธิ์ในการเข้าถึง task
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
	canUpdate := false

	if task.BoardID == nil {
		// Task ไม่มี board_id - ตรวจสอบ create_by
		if task.CreateBy != nil && *task.CreateBy == int(userID) {
			canUpdate = true
		}
	} else {
		// Task มี board_id - ตรวจสอบสิทธิ์แบบ 2 เงื่อนไข
		// 1. ถ้าเป็นคนสร้าง task (create_by = user_id)
		if task.CreateBy != nil && *task.CreateBy == int(userID) {
			canUpdate = true
		} else {
			// 2. ถ้าเป็นสมาชิกของ board (ผ่าน board_user)
			var count int64
			query := `
				SELECT COUNT(1)
				FROM tasks t
				JOIN board_user bu ON t.board_id = bu.board_id
				WHERE t.task_id = ? AND bu.user_id = ?
			`

			if err := db.Raw(query, taskID, userID).Count(&count).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board access"})
				return
			}

			if count > 0 {
				canUpdate = true
			}
		}
	}

	if !canUpdate {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// อัปเดท checklist name ด้วย Transaction
	var updatedChecklist model.Checklist
	err = db.Transaction(func(tx *gorm.DB) error {
		// อัปเดทเฉพาะชื่อ
		result := tx.Model(&model.Checklist{}).
			Where("checklist_id = ?", checklistID).
			Update("checklist_name", req.ChecklistName)

		if result.Error != nil {
			return result.Error
		}

		// ตรวจสอบว่าอัปเดทได้จริงหรือไม่
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		// ดึงข้อมูลที่อัปเดทแล้วมาส่งกลับ
		if err := tx.Where("checklist_id = ?", checklistID).First(&updatedChecklist).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Checklist not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update checklist"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Checklist updated successfully",
		"checklist": gin.H{
			"checklist_id":   updatedChecklist.ChecklistID,
			"task_id":        updatedChecklist.TaskID,
			"checklist_name": updatedChecklist.ChecklistName,
			"status":         updatedChecklist.Status,
			"create_at":      updatedChecklist.CreateAt,
		},
	})
}
