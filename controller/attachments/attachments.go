package attachments

import (
	"context"
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
			Attachment(c, db, firestoreClient)
		})
		routes.DELETE("/delete/:attachmentid", func(c *gin.Context) {
			DeleteAttachment(c, db, firestoreClient)
		})
	}
}

func Attachment(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	taskID := c.Param("taskid")
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

	// Convert taskID from string to int
	taskIDInt, err := strconv.Atoi(taskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	// Create attachment record
	attachment := model.Attachment{
		TasksID:  taskIDInt,
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

	// Save to Firebase using Set() with specific document ID
	ctx := context.Background()
	docRef := firestoreClient.Collection("Attachments").Doc(strconv.Itoa(attachmentID))
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
	attachmentID := c.Param("attachmentid")

	var hasAnyData int
	if err := db.Raw(`
		SELECT 
			CASE 
				WHEN EXISTS(SELECT 1 FROM board WHERE create_by = ?) OR
					 EXISTS(SELECT 1 FROM board_user WHERE user_id = ?) OR
					 EXISTS(SELECT 1 FROM tasks WHERE create_by = ?)
				THEN 1 
				ELSE 0 
			END as has_any_data
	`, userID, userID, userID).Scan(&hasAnyData).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check user data"})
		return
	}
	if hasAnyData == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "User has no permission to delete this attachment"})
		return
	}

	// ลบ tasks จาก GORM ก่อน
	if err := db.Where("attachment_id = ? ", attachmentID).Delete(&model.Attachment{}).Error; err != nil {
		c.JSON(500, gin.H{"error": "Failed to delete attachment from database"})
		return
	}

	// ลบ tasks จาก Firestore หลังจากลบจาก SQL เรียบร้อยแล้ว
	ctx := c.Request.Context()

	if _, err := firestoreClient.Collection("Attachments").Doc(attachmentID).Delete(ctx); err != nil {
		// ถ้าลบจาก Firestore ล้มเหลว อาจต้องพิจารณา rollback การลบจาก SQL
		c.JSON(500, gin.H{"error": "Failed to delete attachments from Firestore"})
		return
	}

	c.JSON(200, gin.H{"message": "attachments deleted successfully"})
}
