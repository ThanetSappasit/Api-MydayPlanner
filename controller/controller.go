package controller

import (
	"mydayplanner/controller/user"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetemailCTL(router *gin.Engine, db *gorm.DB) {
	router.POST("/email", func(c *gin.Context) {
		user.GetEmail(c, db)
	})
}
