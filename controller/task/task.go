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

func TaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/task", middleware.AccessTokenMiddleware())
	{
		routes.POST("/create", func(c *gin.Context) {
			CreateTaskFirebase(c, db, firestoreClient)
		})
	}
}

func CreateTaskFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var task dto.CreateTaskRequest
	if err := c.ShouldBindJSON(&task); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var user model.User
	if err := db.Where("user_id = ?", task.CreatedBy).First(&user).Error; err != nil {
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
	isGroupBoard := err == nil

	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}

	newTask := model.Tasks{
		BoardID:     task.BoardID,
		TaskName:    task.TaskName,
		Description: task.Desciption,
		Status:      task.Status,
		Priority:    task.Priority,
		CreateBy:    task.CreatedBy,
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
	}

	// Determine the collection path based on whether it's a group or private board
	var collectionPath string
	if isGroupBoard {
		collectionPath = "Group_Boards"
	} else {
		collectionPath = "Private_Boards"
	}

	// Set data according to the Firestore structure
	userIDStr := strconv.Itoa(user.UserID)
	boardIDStr := strconv.Itoa(task.BoardID)
	taskIDStr := strconv.Itoa(newTask.TaskID)

	_, err = firestoreClient.Collection("Boards").
		Doc(userIDStr).
		Collection(collectionPath).
		Doc(boardIDStr).
		Collection("tasks").
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
