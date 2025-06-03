package checklist

import (
	"mydayplanner/middleware"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ChecklistController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/checklist", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		Checklist(c, db, firestoreClient)
	})
	router.PUT("/checklist", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		UpdateChecklist(c, db, firestoreClient)
	})
	router.PUT("/checklistfinish/:checklistid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		FinishChecklist(c, db, firestoreClient)
	})
}
