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
	"strings"
	"time"

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
	userId := c.MustGet("userId").(uint)
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

	// ตรวจสอบความยาวของชื่อ checklist
	if strings.TrimSpace(req.ChecklistName) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Checklist name is required"})
		return
	}

	checklistName := strings.TrimSpace(req.ChecklistName)
	if len(checklistName) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Checklist name is too long (max 255 characters)"})
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

	// ตรวจสอบสิทธิ์และกำหนดว่าต้องอัพเดท Firestore หรือไม่
	var canUpdate = false
	var shouldUpdateFirestore = false

	if task.BoardID == nil {
		// Task ที่ไม่มี board_id - ตรวจสอบ create_by
		if task.CreateBy != nil && *task.CreateBy == int(userId) {
			canUpdate = true
			shouldUpdateFirestore = false
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
				// เป็นเจ้าของ board แต่ไม่ต้องอัพเดท Firestore
				canUpdate = true
				shouldUpdateFirestore = false
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board membership"})
				return
			}
		} else {
			// เป็นสมาชิกของ board ต้องอัพเดท Firestore ด้วย
			canUpdate = true
			shouldUpdateFirestore = true
		}
	}

	if !canUpdate {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// Variables สำหรับ rollback
	var firestoreDocRef *firestore.DocumentRef
	var firestoreOriginalData map[string]interface{}
	var firestoreUpdated = false

	// Step 1: อัพเดท Firestore ก่อน (เฉพาะเมื่อเป็น board member)
	if shouldUpdateFirestore {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		firestoreDocRef = firestoreClient.Collection("Checklist").Doc(fmt.Sprintf("%d", checklistID))

		// ดึงข้อมูลเดิมจาก Firestore เพื่อใช้ในการ rollback
		docSnap, err := firestoreDocRef.Get(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get original Firestore data"})
			return
		}

		if docSnap.Exists() {
			firestoreOriginalData = docSnap.Data()
		}

		// อัพเดท Firestore
		firestoreUpdates := []firestore.Update{
			{
				Path:  "checklist_name",
				Value: checklistName,
			}, {
				Path:  "updatedAt",
				Value: firestore.ServerTimestamp,
			},
		}

		_, err = firestoreDocRef.Update(ctx, firestoreUpdates)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update checklist in Firestore"})
			return
		}
		firestoreUpdated = true
	}

	// Step 2: อัพเดท Database ด้วย Transaction
	var updatedChecklist model.Checklist
	err = db.Transaction(func(tx *gorm.DB) error {
		// อัปเดทเฉพาะชื่อ
		result := tx.Model(&model.Checklist{}).
			Where("checklist_id = ?", checklistID).
			Update("checklist_name", checklistName)

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

	// Step 3: หาก Database update ล้มเหลว และได้อัพเดท Firestore แล้ว ให้ rollback Firestore
	if err != nil {
		if firestoreUpdated && firestoreDocRef != nil {
			rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer rollbackCancel()

			// Rollback Firestore
			if firestoreOriginalData != nil {
				if originalChecklistName, exists := firestoreOriginalData["checklist_name"]; exists {
					rollbackUpdates := []firestore.Update{
						{
							Path:  "checklist_name",
							Value: originalChecklistName,
						},
					}

					_, rollbackErr := firestoreDocRef.Update(rollbackCtx, rollbackUpdates)
					if rollbackErr != nil {
						// Log rollback error but continue with the main error response
						fmt.Printf("CRITICAL: Failed to rollback Firestore for checklist %d: %v\n", checklistID, rollbackErr)
					} else {
						fmt.Printf("INFO: Successfully rolled back Firestore for checklist %d\n", checklistID)
					}
				}
			} else {
				// ถ้าไม่มีข้อมูลเดิม ให้ลบ document ออก
				_, rollbackErr := firestoreDocRef.Delete(rollbackCtx)
				if rollbackErr != nil {
					fmt.Printf("CRITICAL: Failed to delete Firestore document for checklist %d during rollback: %v\n", checklistID, rollbackErr)
				} else {
					fmt.Printf("INFO: Successfully deleted Firestore document for checklist %d during rollback\n", checklistID)
				}
			}
		}

		// ส่ง error response
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Checklist not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update checklist"})
		}
		return
	}

	// Success response
	c.JSON(http.StatusOK, gin.H{
		"message": "Checklist updated successfully",
		"checklist": gin.H{
			"checklist_id":   updatedChecklist.ChecklistID,
			"task_id":        updatedChecklist.TaskID,
			"checklist_name": updatedChecklist.ChecklistName,
			"status":         updatedChecklist.Status,
			"create_at":      updatedChecklist.CreateAt,
		},
		"firestoreUpdated": firestoreUpdated,
	})
}
