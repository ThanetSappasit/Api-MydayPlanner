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
		routes.POST("/create", func(c *gin.Context) {
			Attachment(c, db, firestoreClient)
		})
		routes.DELETE("/delete/:boardid", func(c *gin.Context) {
			DeleteAttachment(c, db, firestoreClient)
		})
	}
}

func Attachment(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.CreateAttachmentsTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Get user email for Firebase path
	var user model.User
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	// Convert string IDs to integers
	taskID, err := strconv.Atoi(req.TaskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	// Create attachment record
	attachment := model.Attachment{
		TasksID:  taskID,
		FileName: req.Filename,
		FilePath: req.Filepath,
		FileType: req.Filetype,
		UploadAt: time.Now(),
	}

	// Save to SQL database
	if err := db.Create(&attachment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create attachment"})
		return
	}

	// Get the generated AttachmentID
	attachmentID := attachment.AttachmentID

	// Prepare Firebase document data
	firestoreData := map[string]interface{}{
		"AttachmentID": attachmentID,
		"TaskID":       taskID,
		"Filename":     attachment.FileName,
		"Filepath":     attachment.FilePath,
		"Filetype":     attachment.FileType,
		"UploadAt":     attachment.UploadAt,
	}

	// Create Firebase collection path (not document path)
	collectionPath := fmt.Sprintf("Boards/%s/Boards/%s/Tasks/%s/Attachments",
		user.Email, req.BoardID, req.TaskID)

	// Save to Firebase using Set() with specific document ID
	ctx := context.Background()
	docRef := firestoreClient.Collection(collectionPath).Doc(strconv.Itoa(attachmentID))
	_, err = docRef.Set(ctx, firestoreData)
	if err != nil {
		// Log the error but don't fail the request since SQL save was successful
		c.JSON(http.StatusPartialContent, gin.H{
			"message":       "Attachment created in database but failed to sync with Firebase",
			"attachment_id": attachmentID,
			"error":         err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Attachment created successfully",
		"attachment_id": attachmentID,
	})
}

func DeleteAttachment(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)

	var req dto.DeleteAttachmentRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid input"})
		return
	}

	attachmentID := req.AttachmentID
	taskID := req.TaskID

	// ลบ tasks จาก GORM ก่อน
	if err := db.Where("attachment_id = ? AND tasks_id = ?", attachmentID, taskID).Delete(&model.Attachment{}).Error; err != nil {
		c.JSON(500, gin.H{"error": "Failed to delete attachment from database"})
		return
	}

	// ลบ tasks จาก Firestore หลังจากลบจาก SQL เรียบร้อยแล้ว
	ctx := c.Request.Context()

	// ใช้ path ใหม่: /Boards/email/Boards/boardID/Tasks/taskID
	userEmail, err := getUserEmail(userID, db) // คุณต้องมี function นี้เพื่อดึง email จาก userID
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to get user email"})
		return
	}
	boardID := c.Param("boardid") // รับ boardID จาก URL

	docPath := fmt.Sprintf("Boards/%s/Boards/%s/Tasks/%s/Attachments/%s", userEmail, boardID, taskID, attachmentID)
	if _, err := firestoreClient.Doc(docPath).Delete(ctx); err != nil {
		// ถ้าลบจาก Firestore ล้มเหลว อาจต้องพิจารณา rollback การลบจาก SQL
		c.JSON(500, gin.H{"error": "Failed to delete attachments from Firestore"})
		return
	}

	c.JSON(200, gin.H{"message": "attachments deleted successfully"})
}

func getUserEmail(userID uint, db *gorm.DB) (string, error) {
	var user struct {
		UserID uint
		Email  string
	}

	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userID).Scan(&user).Error; err != nil {
		return "", err
	}

	return user.Email, nil
}
