package user

import (
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"time"

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
	router.POST("/email", func(c *gin.Context) {
		EmailData(c, db)
	})
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

func EmailData(c *gin.Context, db *gorm.DB) {
	var email dto.EmailRequest
	if err := c.ShouldBindJSON(&email); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	var user model.User
	result := db.First(&user, email)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	response := gin.H{
		"UserID": user.UserID,
		"Email":  user.Email,
	}
	c.JSON(http.StatusOK, response)
}

func GetUserAllData(c *gin.Context, db *gorm.DB) {
	userId := c.MustGet("userId").(uint)

	// Get user profile with only required fields
	var user struct {
		UserID    uint      `json:"UserID"`
		Name      string    `json:"Name"`
		Email     string    `json:"Email"`
		Profile   string    `json:"Profile"`
		Role      string    `json:"Role"`
		CreatedAt time.Time `json:"CreatedAt"`
	}
	if err := db.Table("user").
		Select("user_id, name, email, profile, role, create_at").
		Where("user_id = ?", userId).
		Scan(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	// Get boards where user is a member (boardgroup) with only required fields
	var boardGroup []struct {
		BoardID   int       `json:"BoardID"`
		BoardName string    `json:"BoardName"`
		CreatedAt time.Time `json:"CreatedAt"`
		CreatedBy uint      `json:"CreatedBy"`
	}
	if err := db.Table("board_user").
		Select("board.board_id, board.board_name, board.create_at, board.create_by as created_by").
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

	// Extract board IDs from boardGroup to exclude them from the board query
	var boardGroupIds []int
	for _, board := range boardGroup {
		boardGroupIds = append(boardGroupIds, board.BoardID)
	}

	// Get boards created by the user, excluding those in boardgroup
	var boards []struct {
		BoardID   int       `json:"BoardID"`
		BoardName string    `json:"BoardName"`
		CreatedAt time.Time `json:"CreatedAt"`
		CreatedBy uint      `json:"CreatedBy"`
	}

	query := db.Table("board").
		Select("board_id, board_name, create_at, create_by as created_by").
		Where("create_by = ?", userId)

	if len(boardGroupIds) > 0 {
		query = query.Where("board_id NOT IN ?", boardGroupIds)
	}

	if err := query.Scan(&boards).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve boards",
			"error":   err.Error(),
		})
		return
	}

	// Ensure board and boardgroup are always arrays, never null
	if boards == nil {
		boards = []struct {
			BoardID   int       `json:"BoardID"`
			BoardName string    `json:"BoardName"`
			CreatedAt time.Time `json:"CreatedAt"`
			CreatedBy uint      `json:"CreatedBy"`
		}{}
	}

	if boardGroup == nil {
		boardGroup = []struct {
			BoardID   int       `json:"BoardID"`
			BoardName string    `json:"BoardName"`
			CreatedAt time.Time `json:"CreatedAt"`
			CreatedBy uint      `json:"CreatedBy"`
		}{}
	}

	// Return the data directly without the success wrapper
	c.JSON(http.StatusOK, gin.H{
		"board":      boards,
		"boardgroup": boardGroup,
		"user":       user,
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
