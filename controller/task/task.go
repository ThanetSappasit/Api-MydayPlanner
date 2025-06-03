package task

import (
	"mydayplanner/middleware"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.PUT("/taskfinish/:taskid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		FinishTask(c, db, firestoreClient)
	})
}
