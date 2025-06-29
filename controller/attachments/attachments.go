package attachments

import (
	"context"
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
	ctx := context.Background()

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

	var req dto.CreateAttachmentsTaskRequest
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
	canCreate := false

	if task.BoardID == nil {
		// Task ไม่มี board_id - ตรวจสอบ create_by
		if task.CreateBy != nil && *task.CreateBy == int(userID) {
			canCreate = true
		}
	} else {
		// Task มี board_id - ตรวจสอบสิทธิ์แบบ 2 เงื่อนไข
		// 1. ถ้าเป็นคนสร้าง task (create_by = user_id)
		if task.CreateBy != nil && *task.CreateBy == int(userID) {
			canCreate = true
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
				canCreate = true
			}
		}
	}

	if !canCreate {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// Create attachment record
	var attachment model.Attachment

	// Start transaction
	err = db.Transaction(func(tx *gorm.DB) error {
		attachment = model.Attachment{
			TasksID:  taskID,
			FileName: req.Filename,
			FilePath: req.Filepath,
			FileType: req.Filetype,
			UploadAt: time.Now(),
		}

		// Save to SQL database
		if err := tx.Create(&attachment).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create attachment"})
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
		_, err = firestoreClient.Collection("Attachments").Doc(strconv.Itoa(int(attachment.AttachmentID))).Set(ctx, map[string]interface{}{
			"attachment_id": attachment.AttachmentID,
			"tasks_id":      attachment.TasksID,
			"file_name":     attachment.FileName,
			"file_path":     attachment.FilePath,
			"file_type":     attachment.FileType,
			"upload_at":     attachment.UploadAt,
		})

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save checklist to Firestore"})
			return
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
		TasksID      int `db:"tasks_id"` // แก้ไขให้ตรงกับ model
	}

	if err := db.Table("attachments").
		Select("attachment_id, tasks_id").
		Where("attachment_id = ? AND tasks_id = ?", attachmentIDInt, taskID). // แก้ไขให้ตรงกับ model
		First(&existingAttachment).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Attachment not found or not belong to specified task"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch attachment"})
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

	// ลบ attachment ด้วย Transaction
	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("attachment_id = ?", attachmentIDInt).Delete(&model.Attachment{})
		if result.Error != nil {
			return result.Error
		}

		// ตรวจสอบว่าลบได้จริงหรือไม่
		if result.RowsAffected == 0 {
			return fmt.Errorf("attachment not found or already deleted")
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete attachment"})
		return
	}

	// ✅ ลบ checklist จาก Firestore ถ้ามีสมาชิกในบอร์ด
	if task.BoardID != nil {
		var boardUserCount int64
		if err := db.Model(&model.BoardUser{}).
			Where("board_id = ?", *task.BoardID).
			Count(&boardUserCount).Error; err != nil {
			fmt.Printf("⚠️ Failed to check board users for Firestore deletion: %v", err)
		} else if boardUserCount > 0 {
			ctx := context.Background() // หรือใช้ ctx จาก `c.Request.Context()` ก็ได้

			// ลบ attachment จาก Firestore collection "Attachments"
			_, err := firestoreClient.Collection("Attachments").Doc(strconv.Itoa(attachmentIDInt)).Delete(ctx)
			if err != nil {
				fmt.Printf("⚠️ Failed to delete attachment from Firestore: %v\n", err)
			}

		}
	}

	// แก้ไข response message
	c.JSON(http.StatusOK, gin.H{
		"message":       "Attachment deleted successfully", // แก้ไขจาก "Checklist deleted successfully"
		"attachment_id": attachmentIDInt,
	})
}
