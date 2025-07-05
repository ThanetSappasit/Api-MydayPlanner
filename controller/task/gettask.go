package task

import (
	"mydayplanner/middleware"
	"net/http"
	"strconv"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TaskController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/task", middleware.AccessTokenMiddleware())
	{
		routes.GET("/user/:boardid", func(c *gin.Context) {
			Getboardusertask(c, db, firestoreClient)
		})
	}
}

// UserResponse represents the response structure for user data
type UserResponse struct {
	UserID  int    `json:"user_id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Profile string `json:"profile"`
	Role    string `json:"role"`
}

// BoardUserResponse represents the complete response
type BoardUserResponse struct {
	BoardID int            `json:"board_id"`
	Users   []UserResponse `json:"users"`
}

func Getboardusertask(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// Get userId from middleware (for potential authorization checks)
	userId := c.MustGet("userId").(uint)

	// Get boardId from URL parameters
	boardIdStr := c.Param("boardid")
	boardId, err := strconv.Atoi(boardIdStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid board ID",
		})
		return
	}

	// Single optimized query using JOIN to get users and check access in one go
	var userResponses []UserResponse
	err = db.Table("board_user bu").
		Select("u.user_id, u.name, u.email, u.profile, u.role").
		Joins("INNER JOIN user u ON bu.user_id = u.user_id").
		Where("bu.board_id = ?", boardId).
		// Check if requesting user has access to this board in the same query
		Where("EXISTS (SELECT 1 FROM board_user bu2 WHERE bu2.board_id = ? AND bu2.user_id = ?)", boardId, userId).
		Find(&userResponses).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to fetch board users",
		})
		return
	}

	// If query returns empty, it means either no users in board or no access
	// We need to differentiate between these cases
	if len(userResponses) == 0 {
		// Check if user has access to board
		var accessCheck int64
		if err := db.Table("board_user").
			Where("board_id = ? AND user_id = ?", boardId, userId).
			Count(&accessCheck).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Database error",
			})
			return
		}

		if accessCheck == 0 {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Access denied to this board",
			})
			return
		}
	}

	// Return response
	response := BoardUserResponse{
		BoardID: boardId,
		Users:   userResponses,
	}

	c.JSON(http.StatusOK, response)
}
