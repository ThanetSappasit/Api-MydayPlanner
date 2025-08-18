package user

import (
	"context"
	"errors"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func UserController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/user", middleware.AccessTokenMiddleware())
	{
		routes.GET("/data", func(c *gin.Context) {
			AllDataUser(c, db, firestoreClient)
		})
		routes.GET("/alluser", func(c *gin.Context) {
			GetAllUser(c, db, firestoreClient)
		})
		routes.POST("/search", func(c *gin.Context) {
			SearchUser(c, db)
		})
		routes.PUT("/profile", func(c *gin.Context) {
			UpdateProfileUser(c, db, firestoreClient)
		})
		routes.PUT("/removepassword", func(c *gin.Context) {
			RemovePassword(c, db, firestoreClient)
		})
		routes.DELETE("/account", func(c *gin.Context) {
			DeleteUser(c, db, firestoreClient)
		})
	}
}

func GetAllUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var users []map[string]interface{}
	if err := db.Table("user").Select("user_id, email, name, role, profile, is_active, is_verify, create_at").Scan(&users).Error; err != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	c.JSON(200, users)
}

func SearchUser(c *gin.Context, db *gorm.DB) {
	var emailReq dto.EmailText
	if err := c.ShouldBindJSON(&emailReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	searchPattern := "%" + emailReq.Email + "%"

	var users []model.User
	if err := db.Where("email LIKE ? AND is_verify != ? AND is_active = ?", searchPattern, "0", "1").Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	if len(users) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"message": "No matching users found"})
		return
	}

	// map ข้อมูลแต่ละ user -> response (ไม่รวม password)
	var userResponses []interface{}
	for _, user := range users {
		userResp := struct {
			UserID    int    `json:"user_id"`
			Name      string `json:"name"`
			Email     string `json:"email"`
			Profile   string `json:"profile"`
			Role      string `json:"role"`
			IsVerify  string `json:"is_verify"`
			IsActive  string `json:"is_active"`
			CreatedAt string `json:"created_at"`
		}{
			UserID:    user.UserID,
			Name:      user.Name,
			Email:     user.Email,
			Profile:   user.Profile,
			Role:      user.Role,
			IsVerify:  user.IsVerify,
			IsActive:  user.IsActive,
			CreatedAt: user.CreatedAt.Format(time.RFC3339),
		}
		userResponses = append(userResponses, userResp)
	}

	c.JSON(http.StatusOK, userResponses)
}

func UpdateProfileUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	var updateProfile dto.UpdateProfileRequest
	if err := c.ShouldBindJSON(&updateProfile); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	// Validate if there's anything to update
	if updateProfile.Name == "" && updateProfile.HashedPassword == "" && updateProfile.Profile == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No data to update"})
		return
	}

	// Validate input data
	if updateProfile.Name != "" {
		updateProfile.Name = strings.TrimSpace(updateProfile.Name)
		if len(updateProfile.Name) < 2 || len(updateProfile.Name) > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Name must be between 2 and 100 characters"})
			return
		}
	}

	if updateProfile.Profile != "" {
		updateProfile.Profile = strings.TrimSpace(updateProfile.Profile)
		if len(updateProfile.Profile) > 500 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Profile description must not exceed 500 characters"})
			return
		}
	}

	// Start database transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}

	// Ensure rollback on any error
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		}
	}()

	// Check if user exists before updating
	var existingUser model.User
	if err := tx.Table("user").Select("user_id, name").Where("user_id = ?", userId).First(&existingUser).Error; err != nil {
		tx.Rollback()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		}
		return
	}

	// Build update map efficiently
	updateMap := make(map[string]interface{})

	// Only add non-empty fields
	if updateProfile.Name != "" {
		updateMap["name"] = updateProfile.Name
	}
	if updateProfile.Profile != "" {
		updateMap["profile"] = updateProfile.Profile
	}

	// Handle password hashing if password is provided
	if updateProfile.HashedPassword != "" {
		// Hash password synchronously (no need for goroutine)
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(updateProfile.HashedPassword), bcrypt.DefaultCost)
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process password"})
			return
		}
		updateMap["hashed_password"] = string(hashedPassword)

	}
	// Update user profile in transaction
	result := tx.Model(&model.User{}).Where("user_id = ?", userId).Updates(updateMap)
	if result.Error != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user profile"})
		return
	}

	// Check if any rows were affected
	if result.RowsAffected == 0 {
		tx.Rollback()
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found or no changes made"})
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save changes"})
		return
	}

	// Prepare response data (without sensitive information)
	responseData := gin.H{
		"message": "Profile updated successfully",
	}

	c.JSON(http.StatusOK, responseData)
}

func DeleteUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	// Use channels for concurrent checking
	type checkResult struct {
		hasBoards bool
		err       error
	}

	checkChan := make(chan checkResult, 1)

	// Check board associations concurrently
	go func() {
		// More efficient query using COUNT with LIMIT 1
		const checkSql = `
			SELECT 
				CASE 
					WHEN creator_count > 0 AND member_count > 0 THEN TRUE
					ELSE FALSE
				END AS is_valid
			FROM (
				SELECT 
					(SELECT COUNT(*) FROM board WHERE create_by = ?) AS creator_count,
					(SELECT COUNT(*) FROM board_user WHERE user_id = ?) AS member_count
			) AS counts;`

		var hasBoards bool
		err := db.Raw(checkSql, userId, userId).Scan(&hasBoards).Error
		checkChan <- checkResult{hasBoards: hasBoards, err: err}
	}()

	// Get result from goroutine
	result := <-checkChan
	if result.err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check user associations"})
		return
	}

	// Optimized transaction handling
	if result.hasBoards {
		// Deactivate user
		updateResult := db.Model(&model.User{}).Where("user_id = ?", userId).Update("is_active", "2")
		if updateResult.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to deactivate user"})
			return
		}

		if updateResult.RowsAffected == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "User deactivated successfully"})
	} else {
		// Delete user
		deleteResult := db.Where("user_id = ?", userId).Delete(&model.User{})
		if deleteResult.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user"})
			return
		}

		if deleteResult.RowsAffected == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "User deleted successfully"})
	}
}

func RemovePassword(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	var passwordReq dto.PasswordRequest
	if err := c.ShouldBindJSON(&passwordReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	// Validate input
	if passwordReq.OldPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Old password is required"})
		return
	}

	// Start database transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}

	// Ensure rollback on any error
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		}
	}()

	// Get user with transaction
	var user model.User
	if err := tx.Table("user").Where("user_id = ?", userId).First(&user).Error; err != nil {
		tx.Rollback()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		}
		return
	}

	// Validate old password
	if err := bcrypt.CompareHashAndPassword([]byte(user.HashedPassword), []byte(passwordReq.OldPassword)); err != nil {
		tx.Rollback()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Current password is incorrect"})
		return
	}

	var hashedPassword []byte
	var err error

	// Handle password removal (set to "-") or change
	if passwordReq.NewPassword == "-" {
		// Remove password - set to special value
		hashedPassword = []byte("-")
	} else {
		// Validate new password is provided
		if passwordReq.NewPassword == "" {
			tx.Rollback()
			c.JSON(http.StatusBadRequest, gin.H{"error": "New password is required"})
			return
		}

		// Check if new password is same as old password
		if err := bcrypt.CompareHashAndPassword([]byte(user.HashedPassword), []byte(passwordReq.NewPassword)); err == nil {
			tx.Rollback()
			c.JSON(http.StatusBadRequest, gin.H{"error": "New password must be different from current password"})
			return
		}

		// Hash new password
		hashedPassword, err = bcrypt.GenerateFromPassword([]byte(passwordReq.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process new password"})
			return
		}
	}

	// Update password in transaction
	result := tx.Model(&model.User{}).Where("user_id = ?", userId).Update("hashed_password", string(hashedPassword))
	if result.Error != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	if result.RowsAffected == 0 {
		tx.Rollback()
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found or password unchanged"})
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save changes"})
		return
	}

	// Update Firestore if client is available
	if firestoreClient != nil {
		ctx := context.Background()
		_, err := firestoreClient.Collection("usersLogin").Doc(fmt.Sprintf("%s", user.Email)).Update(ctx, []firestore.Update{
			{Path: "passwordChangedAt", Value: time.Now()},
		})
		if err != nil {
			// Log error but don't fail the request since DB is already updated
			fmt.Printf("Failed to update Firestore for user %d: %v\n", userId, err)
		}
	}

	var message string
	if passwordReq.NewPassword == "-" {
		message = "Password removed successfully"
	} else {
		message = "Password changed successfully"
	}

	c.JSON(http.StatusOK, gin.H{
		"message": message,
	})
}

func GetEmail(c *gin.Context, db *gorm.DB) {
	var emailReq dto.EmailRequest
	if err := c.ShouldBindJSON(&emailReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	// Query ข้อมูลจากฐานข้อมูล
	var user model.User
	result := db.Where("email = ?", emailReq.Email).First(&user)

	// ตรวจสอบว่าพบข้อมูลหรือไม่
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		// Error อื่นๆ จากฐานข้อมูล
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	// สร้าง response
	response := gin.H{
		"Email":  user.Email,
		"UserID": user.UserID,
	}

	// ส่ง response กลับ
	c.JSON(http.StatusOK, response)
}
