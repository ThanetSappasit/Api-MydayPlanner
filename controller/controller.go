package controller

import (
	"mydayplanner/controller/assigned"
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
		routes.PUT("/removepassword", func(c *gin.Context) {
			user.RemovePassword(c, db, firestoreClient)
		})
		routes.DELETE("/account", func(c *gin.Context) {
			user.DeleteUser(c, db, firestoreClient)
		})
	}
}

func BoardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {

}

func AssignedController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/assigned", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		assigned.AssignedTask(c, db, firestoreClient)
	})
}
