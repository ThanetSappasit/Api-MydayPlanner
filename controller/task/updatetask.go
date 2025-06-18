package task

import (
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func UpdateTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.PUT("/updatetask/:taskid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		AdjustTask(c, db, firestoreClient)
	})
}

func AdjustTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
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

	var taskreq dto.AdjustTaskRequest
	if err := c.ShouldBindJSON(&taskreq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input", "details": err.Error()})
		return
	}

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
	canUpdate := false

	if task.BoardID == nil {
		// Task ไม่มี board_id - ตรวจสอบ create_by
		if task.CreateBy != nil && *task.CreateBy == int(userId) {
			canUpdate = true
		}
	} else {
		// Task มี board_id - ตรวจสอบสิทธิ์แบบ 2 เงื่อนไข
		// 1. ถ้าเป็นคนสร้าง task (create_by = user_id)
		if task.CreateBy != nil && *task.CreateBy == int(userId) {
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

			if err := db.Raw(query, taskID, userId).Count(&count).Error; err != nil {
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

	// เตรียมข้อมูลสำหรับอัปเดท (อัปเดทเฉพาะฟิลด์ที่ส่งมา)
	updates := make(map[string]interface{})

	// ตรวจสอบและเพิ่มฟิลด์ที่จะอัปเดท
	if strings.TrimSpace(taskreq.TaskName) != "" {
		updates["task_name"] = strings.TrimSpace(taskreq.TaskName)
	}

	if strings.TrimSpace(taskreq.Description) != "" {
		updates["description"] = strings.TrimSpace(taskreq.Description)
	}

	if strings.TrimSpace(taskreq.Priority) != "" {
		priority := strings.TrimSpace(taskreq.Priority)
		// ตรวจสอบว่า priority ถูกต้องหรือไม่ (1, 2, 3)
		if priority == "1" || priority == "2" || priority == "3" {
			updates["priority"] = priority
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Priority must be 1, 2, or 3"})
			return
		}
	}

	// ตรวจสอบว่ามีฟิลด์ที่จะอัปเดทหรือไม่
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No valid fields to update"})
		return
	}

	// อัปเดทข้อมูลด้วย Transaction
	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Tasks{}).
			Where("task_id = ?", taskID).
			Updates(updates)

		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		return nil
	})

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found or no changes made"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task"})
		}
		return
	}

	// ดึงข้อมูล task ที่อัปเดทแล้วเพื่อส่งกลับ
	var updatedTask model.Tasks
	if err := db.Where("task_id = ?", taskID).First(&updatedTask).Error; err != nil {
		// ถ้าดึงข้อมูลไม่ได้ ก็ส่ง response สำเร็จแต่ไม่มีข้อมูล task
		c.JSON(http.StatusOK, gin.H{
			"message":       "Task updated successfully",
			"taskId":        taskID,
			"updatedFields": getUpdatedFieldNames(updates),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Task updated successfully",
		"task":          updatedTask,
		"updatedFields": getUpdatedFieldNames(updates),
	})
}

// ฟังก์ชันช่วยเพื่อแปลงชื่อฟิลด์ที่อัปเดท
func getUpdatedFieldNames(updates map[string]interface{}) []string {
	var fields []string
	for key := range updates {
		switch key {
		case "task_name":
			fields = append(fields, "Task Name")
		case "description":
			fields = append(fields, "Description")
		case "priority":
			fields = append(fields, "Priority")
		}
	}
	return fields
}
