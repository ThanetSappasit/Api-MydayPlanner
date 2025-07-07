package attachments

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

func AttachmentsController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/attachment", middleware.AccessTokenMiddleware())
	{
		routes.POST("/create/:taskid", func(c *gin.Context) {
			CreateAttachment(c, db, firestoreClient)
		})
		routes.DELETE("/delete/:taskid/:attachmentid", func(c *gin.Context) {
			DeleteAttachment(c, db, firestoreClient)
		})
	}
}

func CreateAttachment(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	taskIDStr := c.Param("taskid")

	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	var req dto.CreateAttachmentsTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
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
	hasPermission := false
	shouldSaveToFirestore := false

	if task.BoardID == nil {
		// ถ้า task ไม่มี board_id แสดงว่าเป็น task ส่วนตัวของ user นี้
		if task.CreateBy != nil && uint(*task.CreateBy) == userID {
			hasPermission = true
			shouldSaveToFirestore = false // ไม่ต้องบันทึกลง Firestore เพราะไม่ใช่ board member
		} else {
			var boardUser model.BoardUser
			if err := db.Where("board_id = ? AND user_id = ?", task.BoardID, userID).First(&boardUser).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					// ไม่ใช่ board member ตรวจว่าเป็น board owner หรือเปล่า
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
	}

	if !hasPermission {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// สร้าง record ใน database
	var attachment model.Attachment
	err = db.Transaction(func(tx *gorm.DB) error {
		attachment = model.Attachment{
			TasksID:  taskID,
			FileName: req.Filename,
			FilePath: req.Filepath,
			FileType: req.Filetype,
			UploadAt: time.Now(),
		}

		if err := tx.Create(&attachment).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create attachment"})
		return
	}

	// บันทึกลง Firestore ถ้าเป็น board member
	if shouldSaveToFirestore {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		docID := strconv.Itoa(int(attachment.AttachmentID))
		_, err := firestoreClient.Collection("Tasks").Doc(taskIDStr).Collection("Attachments").Doc(docID).Set(ctx, map[string]interface{}{
			"attachment_id": attachment.AttachmentID,
			"tasks_id":      attachment.TasksID,
			"file_name":     attachment.FileName,
			"file_path":     attachment.FilePath,
			"file_type":     attachment.FileType,
			"upload_at":     attachment.UploadAt,
		})

		if err != nil {
			fmt.Printf("⚠️ Failed to save attachment %s to Firestore: %v\n", docID, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Attachment created successfully",
		"attachment_id": attachment.AttachmentID,
		"attachment": gin.H{
			"attachment_id": attachment.AttachmentID,
			"task_id":       attachment.TasksID,
			"file_name":     attachment.FileName,
			"file_path":     attachment.FilePath,
			"file_type":     attachment.FileType,
			"upload_at":     attachment.UploadAt,
		},
	})
}

func DeleteAttachment(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	taskIDStr := c.Param("taskid")
	attachmentIDStr := c.Param("attachmentid")

	// แปลง taskID เป็น int
	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	// แปลง attachmentID เป็น int
	attachmentIDInt, err := strconv.Atoi(attachmentIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid attachment ID"})
		return
	}

	// ตรวจสอบว่า attachment นี้เป็นของ task ที่ระบุหรือไม่
	var existingAttachment struct {
		AttachmentID int `db:"attachment_id"`
		TasksID      int `db:"tasks_id"`
	}

	if err := db.Table("attachments").
		Select("attachment_id, tasks_id").
		Where("attachment_id = ? AND tasks_id = ?", attachmentIDInt, taskID).
		First(&existingAttachment).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Attachment not found or not belong to specified task"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch attachment"})
		}
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

	// ตรวจสอบสิทธิ์แบบเดียวกับฟังก์ชันอื่น
	hasPermission := false
	shouldDeleteFromFirestore := false

	if task.BoardID == nil {
		// ถ้า task ไม่มี board_id แสดงว่าเป็น task ส่วนตัวของ user นี้
		if task.CreateBy != nil && uint(*task.CreateBy) == userID {
			hasPermission = true
			shouldDeleteFromFirestore = false // ไม่ต้องบันทึกลง Firestore เพราะไม่ใช่ board member
		} else {
			var boardUser model.BoardUser
			if err := db.Where("board_id = ? AND user_id = ?", task.BoardID, userID).First(&boardUser).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					// ไม่ใช่ board member → ตรวจว่าเป็นเจ้าของ board หรือไม่
					var board model.Board
					if err := db.Where("board_id = ? AND create_by = ?", task.BoardID, userID).First(&board).Error; err != nil {
						if errors.Is(err, gorm.ErrRecordNotFound) {
							c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not a board member or board owner"})
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
	}

	if !hasPermission {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// ลบ attachment ด้วย Transaction
	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("attachment_id = ?", attachmentIDInt).Delete(&model.Attachment{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("attachment not found or already deleted")
		}
		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete attachment"})
		return
	}

	// ✅ ลบจาก Firestore ถ้าเป็นสมาชิกบอร์ด
	if shouldDeleteFromFirestore {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := firestoreClient.Collection("Tasks").Doc(taskIDStr).Collection("Attachments").
			Doc(strconv.Itoa(attachmentIDInt)).
			Delete(ctx)

		if err != nil {
			fmt.Printf("⚠️ Failed to delete attachment from Firestore: %v\n", err)
		}
	}

	// ตอบกลับ
	c.JSON(http.StatusOK, gin.H{
		"message":       "Attachment deleted successfully",
		"attachment_id": attachmentIDInt,
	})
}
