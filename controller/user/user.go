package user

import (
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"

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
		routes.GET("/AlldataUser", func(c *gin.Context) {
			GetUserAllData(c, db)
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

func GetUserAllData(c *gin.Context, db *gorm.DB) {
	userId := c.MustGet("userId").(uint)

	// Get user profile
	var user model.User
	if err := db.First(&user, userId).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	// Get boards where user is a member (boardgroup)
	var boardGroup []model.Board
	if err := db.Table("board_user").
		Select("board.*").
		Joins("JOIN board ON board_user.board_id = board.board_id").
		Where("board_user.user_id = ?", userId).
		Scan(&boardGroup).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve board groups",
			"error":   err.Error(),
		})
		return
	}

	// Fetch boardIds to get complete board information with preloaded Creator
	var boardIds []int
	for _, board := range boardGroup {
		boardIds = append(boardIds, board.BoardID)
	}

	// Now fetch complete board information with preloaded Creator with selected fields
	if len(boardIds) > 0 {
		var completeBoardGroup []model.Board
		if err := db.Preload("Creator", func(db *gorm.DB) *gorm.DB {
			return db.Select("user_id, name, profile")
		}).Where("board_id IN ?", boardIds).Find(&completeBoardGroup).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to retrieve complete board information",
				"error":   err.Error(),
			})
			return
		}
		boardGroup = completeBoardGroup
	}

	// Extract board IDs from boardGroup to exclude them from the board query
	var boardGroupIds []int
	for _, board := range boardGroup {
		boardGroupIds = append(boardGroupIds, board.BoardID)
	}

	// Get boards created by the user, excluding those in boardgroup
	var boards []model.Board
	query := db.Preload("Creator", func(db *gorm.DB) *gorm.DB {
		return db.Select("user_id, name, profile")
	}).Where("create_by = ?", userId)
	if len(boardGroupIds) > 0 {
		query = query.Where("board_id NOT IN ?", boardGroupIds)
	}
	if err := query.Find(&boards).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve boards",
			"error":   err.Error(),
		})
		return
	}

	// Prepare response
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user":       user,
			"board":      boards,     // Boards created by user (excluding those in boardgroup)
			"boardgroup": boardGroup, // Boards where user is a member
		},
	})
}
