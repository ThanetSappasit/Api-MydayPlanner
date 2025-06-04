package user

import (
	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetAllUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var users []map[string]interface{}
	if err := db.Table("user").Select("user_id, email, name, role, profile, create_at").Scan(&users).Error; err != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	c.JSON(200, users)
}
