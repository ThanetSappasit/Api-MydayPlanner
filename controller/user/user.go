package user

import (
	"mydayplanner/dto"
	"mydayplanner/model"
	"net/http"

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

func GetAllUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var users []map[string]interface{}
	if err := db.Table("user").Select("user_id, email, name, role, profile, is_active, is_verify, create_at").Scan(&users).Error; err != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	c.JSON(200, users)
}
