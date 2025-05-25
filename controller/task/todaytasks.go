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
			DataTodayTask(c, firestoreClient)
		})
		routes.POST("/allarchivetoday", func(c *gin.Context) {
			DataArchiveTodayTask(c, firestoreClient)
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
	}
}

func DataTodayTask(c *gin.Context, firestoreClient *firestore.Client) {
	var req dto.DataTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	iter := firestoreClient.Collection("TodayTasks").Doc(req.Email).Collection("tasks").Where("archive", "==", false).Documents(c)

	defer iter.Stop()
	var tasks []map[string]interface{}
	// ดึงข้อมูลจาก Firestore
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

	c.JSON(http.StatusOK, tasks)
}
func DataArchiveTodayTask(c *gin.Context, firestoreClient *firestore.Client) {
	var req dto.DataTodayTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	iter := firestoreClient.Collection("TodayTasks").Doc(req.Email).Collection("tasks").Where("archive", "==", true).Documents(c)

	defer iter.Stop()
	var tasks []map[string]interface{}
	// ดึงข้อมูลจาก Firestore
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

	c.JSON(http.StatusOK, tasks)
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
		"taskname":    req.TaskName,
		"description": req.Desciption,
		"status":      req.Status,
		"priority":    req.Priority,
		"created_at":  time.Now(),
		"archive":     false,
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
