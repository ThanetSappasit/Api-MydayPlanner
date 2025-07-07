package checklist

import (
	"context"
	"errors"
	"fmt"
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
	if len(checklistIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No checklist IDs provided"})
		return
	}

	// ตรวจสอบว่า checklist เหล่านี้อยู่ใน task ที่ระบุ
	var existingChecklists []model.Checklist
	if err := db.
		Where("checklist_id IN ? AND task_id = ?", checklistIDs, taskID).
		Find(&existingChecklists).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch checklists"})
		return
	}
	if len(existingChecklists) != len(checklistIDs) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Some checklists not found or not belong to specified task"})
		return
	}

	// ตรวจสอบ task
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

	// ตรวจสอบสิทธิ์แบบเดียวกับฝั่ง create
	hasPermission := false
	shouldDeleteFromFirestore := false

	if task.BoardID == nil {
		// ถ้า task ไม่มี board_id แสดงว่าเป็น task ส่วนตัวของ user นี้
		if task.CreateBy != nil && uint(*task.CreateBy) == userID {
			hasPermission = true
			shouldDeleteFromFirestore = false // ไม่ต้องบันทึกลง Firestore เพราะไม่ใช่ board member
		}
	} else {
		var boardUser model.BoardUser
		if err := db.Where("board_id = ? AND user_id = ?", task.BoardID, userID).First(&boardUser).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				var board model.Board
				if err := db.Where("board_id = ? AND create_by = ?", task.BoardID, userID).First(&board).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not a board member or owner"})
					} else {
						c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board ownership"})
					}
					return
				}
				hasPermission = true
				shouldDeleteFromFirestore = false
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch board user"})
				return
			}
		} else {
			hasPermission = true
			shouldDeleteFromFirestore = true
		}
	}

	if !hasPermission {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// ลบ checklists
	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("checklist_id IN ?", checklistIDs).Delete(&model.Checklist{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(checklistIDs)) {
			return fmt.Errorf("expected to delete %d, but deleted %d", len(checklistIDs), result.RowsAffected)
		}
		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete checklists"})
		return
	}

	// ลบจาก Firestore (ถ้าเป็นสมาชิกบอร์ด)
	if shouldDeleteFromFirestore {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		for _, checklist := range existingChecklists {
			docID := strconv.Itoa(int(checklist.ChecklistID))
			_, err := firestoreClient.Collection("Tasks").Doc(taskIDStr).Collection("Checklist").Doc(docID).Delete(ctx)
			if err != nil {
				fmt.Printf("⚠️ Failed to delete checklist %s from Firestore: %v\n", docID, err)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":           fmt.Sprintf("%d checklists deleted successfully", len(checklistIDs)),
		"deletedCount":      len(checklistIDs),
		"deletedChecklists": checklistIDs,
	})
}

func DeleteSingleChecklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	taskIDStr := c.Param("taskid")
	checklistIDStr := c.Param("checklistid")

	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	checklistID, err := strconv.Atoi(checklistIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid checklist ID"})
		return
	}

	// ตรวจสอบ checklist ที่ระบุ
	var checklist model.Checklist
	if err := db.Where("checklist_id = ? AND task_id = ?", checklistID, taskID).
		First(&checklist).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Checklist not found or not belong to specified task"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch checklist"})
		}
		return
	}

	// ตรวจสอบ Task เพื่อดูสิทธิ์การเข้าถึง
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

	hasPermission := false
	shouldDeleteFromFirestore := false

	if task.BoardID == nil {
		// ถ้า task ไม่มี board_id แสดงว่าเป็น task ส่วนตัวของ user นี้
		if task.CreateBy != nil && uint(*task.CreateBy) == userID {
			hasPermission = true
			shouldDeleteFromFirestore = false // ไม่ต้องบันทึกลง Firestore เพราะไม่ใช่ board member
		}
	} else {
		var boardUser model.BoardUser
		if err := db.Where("board_id = ? AND user_id = ?", task.BoardID, userID).First(&boardUser).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				var board model.Board
				if err := db.Where("board_id = ? AND create_by = ?", task.BoardID, userID).First(&board).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not a board member or owner"})
					} else {
						c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board ownership"})
					}
					return
				}
				hasPermission = true
				shouldDeleteFromFirestore = false
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch board user"})
				return
			}
		} else {
			hasPermission = true
			shouldDeleteFromFirestore = true
		}
	}

	if !hasPermission {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// ลบ checklist
	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("checklist_id = ?", checklistID).Delete(&model.Checklist{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("checklist not found or already deleted")
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete checklist"})
		return
	}

	// ลบจาก Firestore ถ้าจำเป็น
	if shouldDeleteFromFirestore {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		docID := strconv.Itoa(checklistID)
		_, err := firestoreClient.Collection("Checklist").Doc(docID).Delete(ctx)
		if err != nil {
			fmt.Printf("⚠️ Failed to delete checklist %s from Firestore: %v\n", docID, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Checklist deleted successfully",
		"checklist_id": checklistID,
	})
}
