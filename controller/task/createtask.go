package task

import (
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

func CreateTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/task", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		CreateTask(c, db, firestoreClient)
	})
}

func CreateTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var task dto.CreateTaskRequest
	if err := c.ShouldBindJSON(&task); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var user model.User
	if err := db.Where("user_id = ?", userId).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Check if board exists
	var board model.Board
	if err := db.Where("board_id = ?", task.BoardID).First(&board).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Board not found"})
		return
	}

	// Determine if it's a group board by checking if any entries exist in board_user
	var boardUser model.BoardUser
	err := db.Where("board_id = ?", task.BoardID).First(&boardUser).Error

	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}

	newTask := model.Tasks{
		BoardID:     task.BoardID,
		TaskName:    task.TaskName,
		Description: task.Description, // แก้ไข typo จาก Desciption
		Status:      task.Status,
		Priority:    task.Priority,
		CreateBy:    user.UserID,
		CreateAt:    time.Now(),
	}

	if err := tx.Create(&newTask).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create task"})
		return
	}

	taskDataFirebase := gin.H{
		"TaskID":      newTask.TaskID,
		"TaskName":    newTask.TaskName,
		"CreatedBy":   newTask.CreateBy,
		"CreatedAt":   newTask.CreateAt,
		"Status":      newTask.Status,
		"Priority":    newTask.Priority,
		"Description": newTask.Description,
		"Archived":    false,
	}

	// Set data according to the Firestore structure
	// แก้ไข path ให้เป็น /Boards/{userEmail}/Boards/{boardID}/Tasks/{taskID}
	userIDStr := user.Email
	boardIDStr := strconv.Itoa(task.BoardID)
	taskIDStr := strconv.Itoa(newTask.TaskID)

	_, err = firestoreClient.Collection("Boards").
		Doc(userIDStr).
		Collection("Boards").
		Doc(boardIDStr).
		Collection("Tasks"). // แก้ไขจาก "tasks" เป็น "Tasks" (uppercase T)
		Doc(taskIDStr).
		Set(c, taskDataFirebase)

	if err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add task to Firestore"})
		return
	}

	// Commit the transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Task created successfully",
		"taskID":  newTask.TaskID,
	})
}
