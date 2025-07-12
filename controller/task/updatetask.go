package task

import (
	"errors"
	"fmt"
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

	// ตรวจสอบสิทธิ์และเก็บข้อมูลว่าเป็น board_user หรือไม่
	canUpdate := false
	isBoardUser := false

	if task.BoardID == nil {
		// Task ที่ไม่มี board_id - ตรวจสอบ create_by
		if task.CreateBy != nil && *task.CreateBy == int(userId) {
			canUpdate = true
		}
	} else {
		// Task ที่มี board_id - ตรวจสอบตามลำดับ
		// 1. ตรวจสอบว่า user เป็นสมาชิกของ board หรือไม่
		var boardUser model.BoardUser
		if err := db.Where("board_id = ? AND user_id = ?", task.BoardID, userId).First(&boardUser).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// 2. ถ้าไม่เป็นสมาชิก ให้ตรวจสอบว่าเป็นผู้สร้าง board หรือไม่
				var board model.Board
				if err := db.Where("board_id = ? AND create_by = ?", task.BoardID, userId).First(&board).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: You are not the board owner or member"})
					} else {
						c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board ownership"})
					}
					return
				}
				// เป็นเจ้าของ board
				canUpdate = true
				isBoardUser = false
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board membership"})
				return
			}
		} else {
			// เป็นสมาชิกของ board
			canUpdate = true
			isBoardUser = true
		}
	}

	if !canUpdate {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: You don't have permission to update this task"})
		return
	}

	// เตรียมข้อมูลสำหรับอัปเดท (อัปเดทเฉพาะฟิลด์ที่ส่งมา)
	updates := make(map[string]interface{})

	// ตรวจสอบและเพิ่มฟิลด์ที่จะอัปเดท
	if strings.TrimSpace(taskreq.TaskName) != "" {
		taskName := strings.TrimSpace(taskreq.TaskName)
		if len(taskName) > 255 { // เพิ่ม validation
			c.JSON(http.StatusBadRequest, gin.H{"error": "Task name is too long (max 255 characters)"})
			return
		}
		updates["task_name"] = taskName
	}

	if strings.TrimSpace(taskreq.Description) != "" {
		description := strings.TrimSpace(taskreq.Description)
		if len(description) > 2000 { // เพิ่ม validation
			c.JSON(http.StatusBadRequest, gin.H{"error": "Description is too long (max 2000 characters)"})
			return
		}
		updates["description"] = description
	}

	if strings.TrimSpace(taskreq.Priority) != "" {
		priorityStr := strings.TrimSpace(taskreq.Priority)
		// ตรวจสอบว่า priority ถูกต้องหรือไม่ (1, 2, 3)
		if priorityStr == "1" || priorityStr == "2" || priorityStr == "3" {
			// แปลงเป็น int เพื่อเก็บใน database
			priority, _ := strconv.Atoi(priorityStr)
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

	// สำหรับ Firestore rollback
	var firestoreDocRef *firestore.DocumentRef
	var firestoreOriginalData map[string]interface{}
	var firestoreUpdated bool = false

	// อัปเดท Firestore ก่อน (เฉพาะเมื่อเป็น board_user และมี board_id)
	if task.BoardID != nil && isBoardUser {
		// เตรียมข้อมูลสำหรับ Firestore
		firestoreUpdates := []firestore.Update{}
		for key, value := range updates {
			// ไม่ส่ง updated_at ไป Firestore เพราะ Firestore มี timestamp เป็นของตัวเอง
			if key != "updated_at" {
				firestoreUpdates = append(firestoreUpdates, firestore.Update{
					Path:  key,
					Value: value,
				})
			}
		}

		if len(firestoreUpdates) > 0 {
			firestoreDocRef = firestoreClient.Collection("Boards").Doc(fmt.Sprintf("%d", *task.BoardID)).
				Collection("Tasks").Doc(fmt.Sprintf("%d", taskID))

			// ดึงข้อมูลเดิมจาก Firestore เพื่อใช้ในการ rollback
			docSnap, err := firestoreDocRef.Get(c)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get original Firestore data"})
				return
			}
			if docSnap.Exists() {
				firestoreOriginalData = docSnap.Data()
			}

			// อัปเดท Firestore
			_, err = firestoreDocRef.Update(c, firestoreUpdates)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task in Firestore"})
				return
			}
			firestoreUpdated = true
		}
	}

	// อัปเดทข้อมูลใน Database ด้วย Transaction
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

	// หาก Database update ล้มเหลว และได้อัปเดท Firestore แล้ว ให้ rollback Firestore
	if err != nil {
		if firestoreUpdated && firestoreDocRef != nil {
			// Rollback Firestore
			if firestoreOriginalData != nil {
				// สร้าง updates สำหรับ rollback
				rollbackUpdates := []firestore.Update{}
				for key := range updates {
					if key != "updated_at" {
						if originalValue, exists := firestoreOriginalData[key]; exists {
							rollbackUpdates = append(rollbackUpdates, firestore.Update{
								Path:  key,
								Value: originalValue,
							})
						}
					}
				}

				if len(rollbackUpdates) > 0 {
					_, rollbackErr := firestoreDocRef.Update(c, rollbackUpdates)
					if rollbackErr != nil {
						// Log rollback error
						fmt.Printf("Failed to rollback Firestore for task %d: %v", taskID, rollbackErr)
					}
				}
			}
		}

		// ส่ง error response
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
			"message":          "Task updated successfully",
			"taskId":           taskID,
			"updatedFields":    getUpdatedFieldNames(updates),
			"firestoreUpdated": firestoreUpdated,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":          "Task updated successfully",
		"task":             updatedTask,
		"updatedFields":    getUpdatedFieldNames(updates),
		"firestoreUpdated": firestoreUpdated,
	})
}

// ฟังก์ชันช่วยเพื่อแปลงชื่อฟิลด์ที่อัปเดท
func getUpdatedFieldNames(updates map[string]interface{}) []string {
	var fields []string
	for key := range updates {
		switch key {
		case "task_name":
			fields = append(fields, "taskName")
		case "description":
			fields = append(fields, "description")
		case "priority":
			fields = append(fields, "priority")
		case "updated_at":
			// ไม่ต้องแสดง updated_at
			continue
		}
	}
	return fields
}
