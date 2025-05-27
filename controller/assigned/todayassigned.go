package assigned

import (
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"gorm.io/gorm"
)

func TodayAssignedController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/todayassigned", middleware.AccessTokenMiddleware())
	{
		routes.POST("/create", func(c *gin.Context) {
			CreateTodayAssignedFirebase(c, db, firestoreClient)
		})
	}
}

func CreateTodayAssignedFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.CreateTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var user model.User
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userId).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	// ดึงข้อมูล taskID ทั้งหมดที่มีอยู่
	tasksIter := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Documents(c)
	defer tasksIter.Stop()

	existingTaskIDs := make(map[int]bool)
	maxTaskID := 0

	for {
		doc, err := tasksIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch existing tasks"})
			return
		}

		// แปลง taskID จาก string เป็น int
		if taskIDStr, ok := doc.Data()["TaskID"].(string); ok {
			if taskIDInt, parseErr := strconv.Atoi(taskIDStr); parseErr == nil {
				existingTaskIDs[taskIDInt] = true
				if taskIDInt > maxTaskID {
					maxTaskID = taskIDInt
				}
			}
		}
	}

	// หา taskID ที่ว่างที่เล็กที่สุด
	var newTaskID int
	found := false

	// ตรวจสอบจาก 1 ไปจนถึง maxTaskID เพื่อหาช่องว่าง
	for i := 1; i <= maxTaskID; i++ {
		if !existingTaskIDs[i] {
			newTaskID = i
			found = true
			break
		}
	}

	// หากไม่มีช่องว่าง ให้ใช้เลขถัดไป
	if !found {
		newTaskID = maxTaskID + 1
	}

	taskID := fmt.Sprintf("%d", newTaskID)

	taskData := map[string]interface{}{
		"TaskID":    taskID,
		"TaskName":  req.TaskName,
		"CreatedBy": user.UserID,
		"CreatedAt": time.Now(),
		"Status":    req.Status,
		"Archived":  false,
	}

	// ตรวจสอบและเพิ่ม description (รองรับทั้งกรณี nil และ empty string)
	if req.Description != nil {
		taskData["Description"] = *req.Description
	} else {
		taskData["Description"] = ""
	}

	// ตรวจสอบและเพิ่ม priority (รองรับทั้งกรณี nil และ empty string)
	if req.Priority != nil {
		taskData["Priority"] = *req.Priority
	} else {
		taskData["Priority"] = ""
	}

	_, err := firestoreClient.Collection("TodayTasks").Doc(user.Email).Collection("tasks").Doc(taskID).Set(c, taskData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create task in Firestore"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Task created successfully",
		"taskID":  taskID,
	})
}
