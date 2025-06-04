package checklist

import (
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/model"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func DeleteChecklist(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)

	var req dto.DeleteChecklistRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid input"})
		return
	}

	checklistID := req.ChecklistID
	taskID := req.TaskID

	// ลบ tasks จาก GORM ก่อน
	for _, checklistID := range checklistID {
		if err := db.Where("checklist_id = ? AND task_id = ?", checklistID, taskID).Delete(&model.Checklist{}).Error; err != nil {
			c.JSON(500, gin.H{"error": "Failed to delete checklist from database"})
			return
		}
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

	for _, checklistID := range checklistID {
		docPath := fmt.Sprintf("Boards/%s/Boards/%s/Tasks/%s/Checklists/%s", userEmail, boardID, taskID, checklistID)
		if _, err := firestoreClient.Doc(docPath).Delete(ctx); err != nil {
			// ถ้าลบจาก Firestore ล้มเหลว อาจต้องพิจารณา rollback การลบจาก SQL
			c.JSON(500, gin.H{"error": "Failed to delete task from Firestore"})
			return
		}
	}

	c.JSON(200, gin.H{"message": "Tasks deleted successfully"})
}

// Helper function - คุณต้องสร้าง function นี้เพื่อดึง email จาก userID
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
