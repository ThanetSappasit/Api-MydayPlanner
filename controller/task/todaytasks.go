package task

import (
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"gorm.io/gorm"
)

func TodayTaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/todaytasks", middleware.AccessTokenMiddleware())
	{
		routes.POST("/alltoday", func(c *gin.Context) {
			DataTodayTask(c, db, firestoreClient)
		})
		routes.POST("/allarchivetoday", func(c *gin.Context) {
			DataArchiveTodayTask(c, db, firestoreClient)
		})
		routes.POST("/data", func(c *gin.Context) {
			DataTodayTaskByName(c, firestoreClient)
		})
		routes.POST("/create", func(c *gin.Context) {
			CreateTodayTaskFirebase(c, firestoreClient)
		})
		routes.PUT("/finish", func(c *gin.Context) {
			FinishTodayTaskFirebase(c, firestoreClient)
		})
		routes.PUT("/adjusttask", func(c *gin.Context) {
			UpdateTodayTaskFirebase(c, db, firestoreClient)
		})
	}
}

func DataTodayTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	// Query the email from the database using userId
	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userId).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	iter := firestoreClient.
		Collection("TodayTasks").
		Doc(email).
		Collection("tasks").
		Where("archive", "==", false).
		Documents(c)

	defer iter.Stop()

	// Ensure tasks is a non-nil empty slice
	tasks := make([]map[string]interface{}, 0)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch tasks"})
			return
		}

		tasks = append(tasks, doc.Data())
	}

	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

func DataArchiveTodayTask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	// Query the email from the database using userId
	var email string
	if err := db.Raw("SELECT email FROM user WHERE user_id = ?", userId).Scan(&email).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
	}

	iter := firestoreClient.
		Collection("TodayTasks").
		Doc(email).
		Collection("tasks").
		Where("archive", "==", true).
		Documents(c)

	defer iter.Stop()

	// Ensure tasks is a non-nil empty slice
	tasks := make([]map[string]interface{}, 0)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch tasks"})
			return
		}

		tasks = append(tasks, doc.Data())
	}

	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

func DataTodayTaskByName(c *gin.Context, firestoreClient *firestore.Client) {
	var req dto.DataTodayTaskByNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	docRef := firestoreClient.
		Collection("TodayTasks").
		Doc(req.Email).
		Collection("tasks").
		Doc(req.TaskName)

	// ดึงข้อมูลจาก Firestore
	docSnap, err := docRef.Get(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get task"})
		return
	}

	// แปลงข้อมูลให้อยู่ในรูป map[string]interface{}
	data := docSnap.Data()

	c.JSON(http.StatusOK, data)
}

func CreateTodayTaskFirebase(c *gin.Context, firestoreClient *firestore.Client) {
	var req dto.CreateTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// นับจำนวนเอกสารที่มีอยู่แล้วใน collection
	tasksIter := firestoreClient.Collection("TodayTasks").Doc(req.Email).Collection("tasks").Documents(c)
	defer tasksIter.Stop()

	count := 0
	for {
		_, err := tasksIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count tasks"})
			return
		}
		count++
	}

	taskID := fmt.Sprintf("%d_%s", count+1, req.TaskName)

	taskData := map[string]interface{}{
		"taskname":   req.TaskName,
		"status":     req.Status,
		"created_at": time.Now(),
		"archive":    false,
	}

	if req.Desciption != nil {
		taskData["description"] = *req.Desciption
	}
	if req.Priority != nil {
		taskData["priority"] = *req.Priority
	}

	_, err := firestoreClient.Collection("TodayTasks").Doc(req.Email).Collection("tasks").Doc(taskID).Set(c, taskData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create task in Firestore"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task created successfully"})
}

func FinishTodayTaskFirebase(c *gin.Context, firestoreClient *firestore.Client) {
	var req dto.FinishTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	docRef := firestoreClient.
		Collection("TodayTasks").
		Doc(req.Email).
		Collection("tasks").
		Doc(req.TaskName)

	_, err := docRef.Update(c, []firestore.Update{
		{Path: "archive", Value: true},
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task archive status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task archived successfully"})
}

func UpdateTodayTaskFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var req dto.AdjustTodayTaskRequest
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

}
