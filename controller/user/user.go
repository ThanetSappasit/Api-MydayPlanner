package user

import (
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
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
		routes.PUT("/updateprofile", func(c *gin.Context) {
			UpdateProfileUser(c, db, firestoreClient)
		})
		routes.DELETE("/account", func(c *gin.Context) {
			DeleteUser(c, db, firestoreClient)
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
	c.JSON(http.StatusOK, users)
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

func UpdateProfileUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var updateProfile dto.UpdateProfileRequest
	if err := c.ShouldBindJSON(&updateProfile); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	var user model.User
	result := db.First(&user, userId)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	if updateProfile.HashedPassword != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(updateProfile.HashedPassword), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}
		updateProfile.HashedPassword = string(hashedPassword)
	}

	updates := map[string]interface{}{
		"name":            updateProfile.Name,
		"hashed_password": updateProfile.HashedPassword,
		"profile":         updateProfile.Profile,
	}

	updateMap := make(map[string]interface{})
	for key, value := range updates {
		if value != "" {
			updateMap[key] = value
		}
	}

	if err := db.Model(&user).Updates(updateMap).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Profile updated successfully"})
}

func DeleteUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	const checkSql = `
        SELECT DISTINCT *
        FROM user
        LEFT JOIN board ON user.user_id = board.create_by
        LEFT JOIN board_user ON user.user_id = board_user.user_id
        WHERE user.user_id = ?
            AND (board.board_id IS NOT NULL OR board_user.board_id IS NOT NULL)
    `
	var results []map[string]interface{}
	if err := db.Raw(checkSql, userId).Scan(&results).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check user associations"})
		return
	}

	//เช็คอีเมลก่อนว่ามีบอร์ดงานไหมถ้ามีไม่ให้ลบ ถ้าไม่มีลบเลย
	if len(results) > 0 {
		const updateSql = `
                UPDATE user
                SET is_active = "2"
                WHERE user_id = ?;`
		if err := db.Exec(updateSql, userId).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to deactivate user"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User deactivated successfully"})
	} else {
		const deleteSql = `
                DELETE FROM user
                WHERE user_id = ?;`
		if err := db.Exec(deleteSql, userId).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User deleted successfully"})
	}
}
