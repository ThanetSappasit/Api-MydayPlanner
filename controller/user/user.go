package user

import (
	"context"
	"errors"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/model"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func UpdateProfileUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	var updateProfile dto.UpdateProfileRequest
	if err := c.ShouldBindJSON(&updateProfile); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Validate if there's anything to update
	if updateProfile.Name == "" && updateProfile.HashedPassword == "" && updateProfile.Profile == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No data to update"})
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

	// Handle password hashing concurrently if password is provided
	if updateProfile.HashedPassword != "" {
		// Use channel for concurrent password hashing
		hashChan := make(chan struct {
			hashedPassword string
			err            error
		}, 1)

		go func() {
			hashedPassword, err := bcrypt.GenerateFromPassword([]byte(updateProfile.HashedPassword), bcrypt.DefaultCost)
			hashChan <- struct {
				hashedPassword string
				err            error
			}{string(hashedPassword), err}
		}()

		// Get result from goroutine
		hashResult := <-hashChan
		if hashResult.err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}
		updateMap["hashed_password"] = hashResult.hashedPassword
	}

	// Single database operation - update directly without SELECT
	result := db.Model(&model.User{}).Where("user_id = ?", userId).Updates(updateMap)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user profile"})
		return
	}

	// Check if any rows were affected (user exists)
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Profile updated successfully"})
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

func GetAllUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var users []map[string]interface{}
	if err := db.Table("user").Select("user_id, email, name, role, profile, is_active, is_verify, create_at").Scan(&users).Error; err != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	c.JSON(200, users)
}

func ChangedPassword(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)

	var passwordReq dto.PasswordRequest
	if err := c.ShouldBindJSON(&passwordReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	// Validate input
	if passwordReq.OldPassword == "" || passwordReq.NewPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Old password and new password are required"})
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

	// Check if new password is same as old password
	if err := bcrypt.CompareHashAndPassword([]byte(user.HashedPassword), []byte(passwordReq.NewPassword)); err == nil {
		tx.Rollback()
		c.JSON(http.StatusBadRequest, gin.H{"error": "New password must be different from current password"})
		return
	}

	// Hash new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(passwordReq.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process new password"})
		return
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

	if firestoreClient != nil {
		ctx := context.Background()
		_, err := firestoreClient.Collection("usersLogin").Doc(fmt.Sprintf("%s", user.Email)).Update(ctx, []firestore.Update{
			{Path: "passwordChangedAt", Value: time.Now()},
		})
		if err != nil {
			// Log error but don't fail the request since DB is already updated
			fmt.Printf("Failed to update Firestore for user %d: %v", userId, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Password changed successfully",
	})
}
