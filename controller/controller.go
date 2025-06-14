package controller

import (
	"mydayplanner/controller/assigned"
	"mydayplanner/controller/attachments"
	"mydayplanner/controller/checklist"
	"mydayplanner/controller/user"
	"mydayplanner/middleware"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func UserController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/user", middleware.AccessTokenMiddleware())
	{
		// routes.GET("/data", func(c *gin.Context) {
		// 	user.GetAllDataFirebase(c, db, firestoreClient)
		// })
		routes.GET("/data", func(c *gin.Context) {
			user.AllDataUser(c, db, firestoreClient)
		})
		routes.GET("/alluser", func(c *gin.Context) {
			user.GetAllUser(c, db, firestoreClient)
		})
		routes.PUT("/profile", func(c *gin.Context) {
			user.UpdateProfileUser(c, db, firestoreClient)
		})
		routes.PUT("/password", func(c *gin.Context) {
			user.ChangedPassword(c, db, firestoreClient)
		})
		routes.DELETE("/account", func(c *gin.Context) {
			user.DeleteUser(c, db, firestoreClient)
		})
	}
}

func BoardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {

}

func ChecklistController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/checklist/:taskid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		checklist.Checklist(c, db, firestoreClient)
	})
	router.PUT("/checklist/:checklistid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		checklist.UpdateChecklist(c, db, firestoreClient)
	})
	router.PUT("/checklistfinish/:checklistid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		checklist.FinishChecklist(c, db, firestoreClient)
	})
	router.DELETE("/checklist/:boardid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		checklist.DeleteChecklist(c, db, firestoreClient)
	})
}

func TodayChecklistController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/todaychecklist", middleware.AccessTokenMiddleware())
	{
		routes.POST("/create/:taskid", func(c *gin.Context) {
			checklist.CreateTodayChecklistFirebase(c, db, firestoreClient)
		})
		routes.PUT("/adjustchecklist", func(c *gin.Context) {
			checklist.UpdateTodayChecklistFirebase(c, db, firestoreClient)
		})
		routes.PUT("/finish", func(c *gin.Context) {
			checklist.FinishTodayChecklistFirebase(c, db, firestoreClient)
		})
		routes.DELETE("/checklist", func(c *gin.Context) {
			checklist.DeleteTodayChecklistFirebase(c, db, firestoreClient)
		})
	}
}

func TodayAttachmentsController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/todayattechments", middleware.AccessTokenMiddleware())
	{
		routes.POST("/create/:taskid", func(c *gin.Context) {
			attachments.CreateTodayAttachmentsFirebase(c, db, firestoreClient)
		})
		routes.DELETE("/attachment", func(c *gin.Context) {
			attachments.DeleteTodayTaskAttachment(c, db, firestoreClient)
		})
	}
}

func AssignedController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/assigned", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		assigned.AssignedTask(c, db, firestoreClient)
	})
}
