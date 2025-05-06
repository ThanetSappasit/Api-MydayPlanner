package board

import (
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func BoardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/board", middleware.AccessTokenMiddleware())
	{
		routes.GET("/allboards", func(c *gin.Context) {
			GetBoards(c, db, firestoreClient)
		})
	}
}

func GetBoards(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// Get all boards for a user
	userId := c.MustGet("userId").(uint)

	var user model.User
	if err := db.Where("user_id = ?", userId).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// 1. ค้นหาบอร์ดที่ผู้ใช้เป็นผู้สร้าง
	var createdBoards []model.Board
	if err := db.Where("create_by = ?", user.UserID).Find(&createdBoards).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch created boards"})
		return
	}
	// 2. ค้นหาบอร์ดที่ผู้ใช้เป็นสมาชิก (จาก board_user)
	var memberBoards []model.Board
	if err := db.Joins("JOIN board_user ON board.board_id = board_user.board_id").
		Where("board_user.user_id = ?", user.UserID).
		Find(&memberBoards).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch member boards"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"created_boards": createdBoards,
		"member_boards":  memberBoards,
	})
}
