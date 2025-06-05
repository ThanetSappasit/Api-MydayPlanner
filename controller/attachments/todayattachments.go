package attachments

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func CreateTodayAttachmentsFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	taskId := c.Param("taskid")

	var req dto.CreateAttachmentsTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var user model.User
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	// ตรวจสอบว่า task มีอยู่จริงหรือไม่
	taskDoc, err := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Doc(taskId).Get(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}
	if !taskDoc.Exists() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task does not exist"})
		return
	}

	// ดึงข้อมูล attachmentsID ทั้งหมดที่มีอยู่
	attachmentsIter := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Doc(taskId).Collection("Attachments").Documents(c)
	defer attachmentsIter.Stop()

	existingAttachmentsIDs := make(map[int]bool)
	maxAttachmentsID := 0

	for {
		doc, err := attachmentsIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch existing checklists"})
			return
		}

		// แปลง checklistID จาก string เป็น int
		if attachmentsIDStr, ok := doc.Data()["AttachmentsID"].(string); ok {
			if attachmentsIDInt, parseErr := strconv.Atoi(attachmentsIDStr); parseErr == nil {
				existingAttachmentsIDs[attachmentsIDInt] = true
				if attachmentsIDInt > maxAttachmentsID {
					maxAttachmentsID = attachmentsIDInt
				}
			}
		}
	}

	// หา checklistID ที่ว่างที่เล็กที่สุด
	var newAttachmentsID int
	found := false

	// ตรวจสอบจาก 1 ไปจนถึง maxChecklistID เพื่อหาช่องว่าง
	for i := 1; i <= maxAttachmentsID; i++ {
		if !existingAttachmentsIDs[i] {
			newAttachmentsID = i
			found = true
			break
		}
	}

	// หากไม่มีช่องว่าง ให้ใช้เลขถัดไป
	if !found {
		newAttachmentsID = maxAttachmentsID + 1
	}

	attachmentsID := fmt.Sprintf("%d", newAttachmentsID)

	attachmentsData := map[string]interface{}{
		"AttachmentsID": attachmentsID,
		"FileName":      req.Filename,
		"FilePath":      req.Filepath,
		"FileType":      req.Filetype,
		"UploadAt":      time.Now(),
	}

	// สร้าง Attachments ใหม่
	_, err = firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Doc(taskId).Collection("Attachments").Doc(attachmentsID).Set(c, attachmentsData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create checklist in Firestore: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Checklist created successfully",
		"AttachmentsID": attachmentsID,
	})
}

func DeleteTodayTaskAttachment(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.DeleteAttachmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Query the email from the database using userId
	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userId).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	ctx := context.Background()

	// Reference to the attachment document
	// Path: /TodayTasks/{email}/tasks/{taskId}/Attachments/{attachmentId}
	DocRef := firestoreClient.Collection("TodayTasks").Doc(email).Collection("tasks").Doc(req.TaskID).Collection("Attachments").Doc(req.AttachmentID)

	// Get the current document data to verify it exists
	docSnapshot, err := DocRef.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Attachment not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve attachment"})
		return
	}

	// Check if document exists
	if !docSnapshot.Exists() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Attachment not found"})
		return
	}

	// Delete the document
	_, err = DocRef.Delete(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete attachment"})
		return
	}

	// Return success response
	c.JSON(http.StatusOK, gin.H{
		"message":       "Attachment deleted successfully",
		"task_id":       req.TaskID,
		"attachment_id": req.AttachmentID,
	})
}
