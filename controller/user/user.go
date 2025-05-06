package user

import (
	"mydayplanner/middleware"
	"mydayplanner/model"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func UserController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/user", middleware.AccessTokenMiddleware())
	{
		routes.GET("/ReadAllUser", func(c *gin.Context) {
			ReadAllUser(c, db)
		})
		routes.GET("/Profile", func(c *gin.Context) {
			Profile(c, db)
		})
		routes.GET("/ProfileAdmin", middleware.AdminMiddleware(), func(c *gin.Context) {
			Profile(c, db)
		})
	}
}

func ReadAllUser(c *gin.Context, db *gorm.DB) {

	var users []model.User
	result := db.Find(&users)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	c.JSON(200, gin.H{"users": users})
}

func Profile(c *gin.Context, db *gorm.DB) {
	userId := c.MustGet("userId").(uint)
	var user model.User
	result := db.First(&user, userId)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	c.JSON(200, gin.H{"user": user})
}
