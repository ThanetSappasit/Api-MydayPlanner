package checklist

import (
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

	var boardCount int64
	if err := db.Table("board").Where("create_by = ?", userID).Count(&boardCount).Error; err != nil {
		c.JSON(500, gin.H{"error": "Failed to check board"})
		return
	}

	var boardUserCount int64
	if err := db.Table("board_user").Where("user_id = ?", userID).Count(&boardUserCount).Error; err != nil {
		c.JSON(500, gin.H{"error": "Failed to check board_user"})
		return
	}

	if boardCount == 0 && boardUserCount == 0 {
		c.JSON(403, gin.H{"error": "You do not have permission to delete checklist"})
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

	for _, checklistID := range checklistID {
		if _, err := firestoreClient.Collection("Checklists").Doc(checklistID).Delete(ctx); err != nil {
			// ถ้าลบจาก Firestore ล้มเหลว อาจต้องพิจารณา rollback การลบจาก SQL
			c.JSON(500, gin.H{"error": "Failed to delete task from Firestore"})
			return
		}
	}

	c.JSON(200, gin.H{"message": "Tasks deleted successfully"})
}
