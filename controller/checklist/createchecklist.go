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

	var user model.User
	if err := db.Where("user_id = ?", userId).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
		}
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

	// ตรวจสอบสิทธิ์การเข้าถึง board และ permission ในการบันทึก Firestore
	var shouldSaveToFirestore = false
	var hasPermission = false

	if task.BoardID == nil {
		// Task ส่วนตัว: ต้องสร้างเองเท่านั้นถึงจะเข้าถึงได้
		if task.CreateBy != nil && uint(*task.CreateBy) == userId {
			hasPermission = true
			shouldSaveToFirestore = false // Task ส่วนตัว ไม่ sync Firestore
		} else {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not the owner of this personal task"})
			return
		}
	} else {
		// ถ้ามี BoardID ⇒ ตรวจสอบว่าเป็นสมาชิกบอร์ดหรือเจ้าของบอร์ด
		var boardUser model.BoardUser
		if err := db.Where("board_id = ? AND user_id = ?", task.BoardID, userId).First(&boardUser).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// ไม่พบใน BoardUser ⇒ ตรวจสอบว่าเป็นเจ้าของบอร์ดไหม
				var board model.Board
				if err := db.Where("board_id = ? AND create_by = ?", task.BoardID, userId).First(&board).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not a board member or board owner"})
					} else {
						c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify board ownership"})
					}
					return
				}
				// เป็นเจ้าของบอร์ด
				hasPermission = true
				shouldSaveToFirestore = false
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch board user"})
				return
			}
		} else {
			// เป็นสมาชิกบอร์ด
			hasPermission = true
			shouldSaveToFirestore = true
		}
	}

	// เริ่ม transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	newChecklist := model.Checklist{
		TaskID:        taskID,
		ChecklistName: req.ChecklistName,
		Status:        "0",
		CreateAt:      time.Now(),
	}

	if err := tx.Create(&newChecklist).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create checklist"})
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	// บันทึกลง Firestore (หลัง database commit สำเร็จ) - เฉพาะเมื่อเป็น board member
	if shouldSaveToFirestore && hasPermission {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		checklistIDInt := int(newChecklist.ChecklistID)
		if err := saveTaskToFirestore(ctx, firestoreClient, &newChecklist, checklistIDInt); err != nil {
			// Log error แต่ไม่ return เพราะ database บันทึกสำเร็จแล้ว
			// ควรใช้ proper logging system
			fmt.Printf("Warning: Failed to save checklist to Firestore: %v\n", err)
		}
	}

	// สร้าง response
	response := gin.H{
		"message":     "Checklist created successfully",
		"checklistID": newChecklist.ChecklistID,
	}

	c.JSON(http.StatusCreated, response)
}

func saveTaskToFirestore(ctx context.Context, client *firestore.Client, checklist *model.Checklist, ChecklistID int) error {
	taskPath := fmt.Sprintf("Checklist/%d", ChecklistID)

	taskData := map[string]interface{}{
		"checklist_id":   checklist.ChecklistID,
		"task_id":        checklist.TaskID,
		"checklist_name": checklist.ChecklistName,
		"status":         checklist.Status,
		"create_at":      checklist.CreateAt,
	}

	_, err := client.Doc(taskPath).Set(ctx, taskData)
	return err
}
