package board

import (
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"time"

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
		routes.POST("/board", func(c *gin.Context) {
			CreateBoards(c, db, firestoreClient)
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

func CreateBoards(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// Create a new board
	var board dto.CreateBoardRequest
	if err := c.ShouldBindJSON(&board); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var user model.User
	if err := db.Where("email = ?", board.CreatedBy).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Start a transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}

	// Using GORM models instead of raw SQL
	newBoard := model.Board{
		BoardName: board.BoardName,
		CreatedBy: user.UserID,
		CreatedAt: time.Now(),
	}

	// Create the board
	if err := tx.Create(&newBoard).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create board"})
		return
	}

	// If it's a group board (or for all boards based on your logic), add the creator as a member
	// Assuming Is_group is a string representation of a boolean or number
	if board.Is_group == "1" || board.Is_group == "true" {
		boardUser := model.BoardUser{
			BoardID: newBoard.BoardID, // Assuming ID is the primary key field in your Board model
			UserID:  user.UserID,
			AddedAt: time.Now(),
		}

		if err := tx.Create(&boardUser).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add user to board"})
			return
		}
	}

	// Commit the transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Board created successfully",
		"boardID": newBoard.BoardID,
	})
}
