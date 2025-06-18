package checklist

import (
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func DeleteChecklistController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.DELETE("/checklist/:taskid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		DeleteChecklist(c, db, firestoreClient)
	})
	router.DELETE("/checklist/:taskid/:checklistid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		DeleteSingleChecklist(c, db, firestoreClient)
	})
}

func DeleteChecklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	taskIDStr := c.Param("taskid")

	// แปลง taskID เป็น int
	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	var req dto.DeleteChecklistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	checklistIDs := req.ChecklistIDs

	// ตรวจสอบว่ามี checklist IDs หรือไม่
	if len(checklistIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No checklist IDs provided"})
		return
	}

	// ดึงข้อมูล checklists พร้อมตรวจสอบว่าเป็นของ task ที่ระบุหรือไม่
	var existingChecklists []struct {
		ChecklistID int `db:"checklist_id"`
		TaskID      int `db:"task_id"`
	}

	if err := db.Table("checklists").
		Select("checklist_id, task_id").
		Where("checklist_id IN ? AND task_id = ?", checklistIDs, taskID).
		Find(&existingChecklists).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch checklists"})
		return
	}

	// ตรวจสอบว่าพบ checklists ทั้งหมดที่ร้องขอหรือไม่
	if len(existingChecklists) != len(checklistIDs) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Some checklists not found or not belong to specified task"})
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
	canDelete := false

	if task.BoardID == nil {
		// Task ไม่มี board_id - ตรวจสอบ create_by
		if task.CreateBy != nil && *task.CreateBy == int(userID) {
			canDelete = true
		}
	} else {
		// Task มี board_id - ตรวจสอบสิทธิ์แบบ 2 เงื่อนไข
		// 1. ถ้าเป็นคนสร้าง task (create_by = user_id)
		if task.CreateBy != nil && *task.CreateBy == int(userID) {
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

			if err := db.Raw(query, taskID, userID).Count(&count).Error; err != nil {
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

	// เก็บ checklist IDs ที่สามารถลบได้
	var deletableChecklistIDs []int
	for _, checklist := range existingChecklists {
		deletableChecklistIDs = append(deletableChecklistIDs, checklist.ChecklistID)
	}

	// ลบ checklists ด้วย Transaction
	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("checklist_id IN ?", deletableChecklistIDs).Delete(&model.Checklist{})
		if result.Error != nil {
			return result.Error
		}

		// ตรวจสอบว่าลบได้จริงหรือไม่
		if result.RowsAffected != int64(len(deletableChecklistIDs)) {
			return fmt.Errorf("expected to delete %d checklists, but deleted %d", len(deletableChecklistIDs), result.RowsAffected)
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete checklists"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":           fmt.Sprintf("%d checklists deleted successfully", len(deletableChecklistIDs)),
		"deletedCount":      len(deletableChecklistIDs),
		"deletedChecklists": deletableChecklistIDs,
	})
}

func DeleteSingleChecklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
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

	// ตรวจสอบว่า checklist นี้เป็นของ task ที่ระบุหรือไม่
	var existingChecklist struct {
		ChecklistID int `db:"checklist_id"`
		TaskID      int `db:"task_id"`
	}

	if err := db.Table("checklists").
		Select("checklist_id, task_id").
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
	canDelete := false

	if task.BoardID == nil {
		// Task ไม่มี board_id - ตรวจสอบ create_by
		if task.CreateBy != nil && *task.CreateBy == int(userID) {
			canDelete = true
		}
	} else {
		// Task มี board_id - ตรวจสอบสิทธิ์แบบ 2 เงื่อนไข
		// 1. ถ้าเป็นคนสร้าง task (create_by = user_id)
		if task.CreateBy != nil && *task.CreateBy == int(userID) {
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

			if err := db.Raw(query, taskID, userID).Count(&count).Error; err != nil {
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

	// ลบ checklist ด้วย Transaction
	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("checklist_id = ?", checklistID).Delete(&model.Checklist{})
		if result.Error != nil {
			return result.Error
		}

		// ตรวจสอบว่าลบได้จริงหรือไม่
		if result.RowsAffected == 0 {
			return fmt.Errorf("checklist not found or already deleted")
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete checklist"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Checklist deleted successfully",
		"checklist_id": checklistID,
	})
}
