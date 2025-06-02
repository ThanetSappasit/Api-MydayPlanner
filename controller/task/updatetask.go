package task

import (
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func UpdateTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.PUT("/taskadjust", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		AdjustTask(c, db, firestoreClient)
	})
}

func AdjustTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)

	var adjustData dto.AdjustTaskRequest
	if err := c.ShouldBindJSON(&adjustData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// ตรวจสอบว่ามี required fields
	if adjustData.BoardID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "board_id is required"})
		return
	}

	if adjustData.TaskID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task_id is required"})
		return
	}

	taskIDInt := adjustData.TaskID
	boardIDInt := adjustData.BoardID

	var user struct {
		UserID int
		Email  string
	}
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userID).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	ctx := c.Request.Context()

	// ดึงข้อมูล task เดิมจาก database
	var currentTask model.Tasks
	if err := db.Where("task_id = ? AND board_id = ?", taskIDInt, boardIDInt).First(&currentTask).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task data"})
		return
	}

	// ตรวจสอบว่า task นี้เป็นของ user นี้หรือไม่
	if currentTask.CreateBy != user.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to modify this task"})
		return
	}

	// เตรียมข้อมูลสำหรับอัปเดต
	updateData := make(map[string]interface{})
	firestoreUpdates := []firestore.Update{}

	// ตรวจสอบและเตรียมข้อมูลที่จะอัปเดต
	// TaskName - ถ้าไม่ใช่ empty string และต่างจากข้อมูลเดิม
	if adjustData.TaskName != "" && adjustData.TaskName != currentTask.TaskName {
		updateData["task_name"] = adjustData.TaskName
		firestoreUpdates = append(firestoreUpdates, firestore.Update{
			Path:  "TaskName",
			Value: adjustData.TaskName,
		})
	}

	// Description - ถ้าไม่ใช่ empty string หรือถ้าต่างจากข้อมูลเดิม (รวมถึงการลบ description)
	if adjustData.Description != currentTask.Description {
		updateData["description"] = adjustData.Description
		firestoreUpdates = append(firestoreUpdates, firestore.Update{
			Path:  "Description",
			Value: adjustData.Description,
		})
	}

	// Status - ถ้าไม่ใช่ empty string และต่างจากข้อมูลเดิม
	if adjustData.Status != "" {
		// ตรวจสอบว่า status เป็นค่าที่ valid (0, 1, 2)
		if adjustData.Status == "0" || adjustData.Status == "1" || adjustData.Status == "2" {
			if adjustData.Status != currentTask.Status {
				updateData["status"] = adjustData.Status
				firestoreUpdates = append(firestoreUpdates, firestore.Update{
					Path:  "Status",
					Value: adjustData.Status,
				})
			}
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status value. Must be 0, 1, or 2"})
			return
		}
	}

	// Priority - ถ้าไม่ใช่ empty string หรือถ้าต่างจากข้อมูลเดิม (รวมถึงการลบ priority)
	if adjustData.Priority != currentTask.Priority {
		// ตรวจสอบว่า priority เป็นค่าที่ valid (1, 2, 3) หรือ empty
		if adjustData.Priority == "" || adjustData.Priority == "1" || adjustData.Priority == "2" || adjustData.Priority == "3" {
			updateData["priority"] = adjustData.Priority
			firestoreUpdates = append(firestoreUpdates, firestore.Update{
				Path:  "Priority",
				Value: adjustData.Priority,
			})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid priority value. Must be 1, 2, 3, or empty"})
			return
		}
	}

	// ถ้าไม่มีข้อมูลที่ต้องอัปเดต
	if len(updateData) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message": "No changes detected",
			"task_id": adjustData.TaskID,
		})
		return
	}

	// เริ่ม transaction เพื่ออัปเดตทั้ง SQL และ Firebase
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// อัปเดตใน SQL Database
	result := tx.Model(&model.Tasks{}).Where("task_id = ? AND board_id = ?", taskIDInt, boardIDInt).Updates(updateData)
	if result.Error != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to update task in database: %v", result.Error),
		})
		return
	}

	// ตรวจสอบว่ามีการอัปเดตจริงหรือไม่
	if result.RowsAffected == 0 {
		tx.Rollback()
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Task not found or no changes made",
		})
		return
	}

	// อัปเดตใน Firebase ถ้ามี updates
	if len(firestoreUpdates) > 0 {
		taskDocRef := firestoreClient.Collection("Boards").Doc(user.Email).Collection("Boards").Doc(strconv.Itoa(adjustData.BoardID)).Collection("Tasks").Doc(strconv.Itoa(adjustData.TaskID))

		_, err := taskDocRef.Update(ctx, firestoreUpdates)
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to update task in Firebase: %v", err),
			})
			return
		}
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to commit transaction: %v", err),
		})
		return
	}

	// เตรียม response data
	responseData := gin.H{
		"message": "Task updated successfully",
		"task_id": adjustData.TaskID,
	}

	// ส่ง response กลับ
	c.JSON(http.StatusOK, responseData)
}
