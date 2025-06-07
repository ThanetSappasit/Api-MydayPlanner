package shareboard

import (
	"crypto/rand"
	"encoding/hex"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ShareboardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/shareboard")
	{
		routes.POST("/create/:boardid", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
			CreateShareboard(c, db, firestoreClient)
		})
		// routes.GET("/all", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		// 	GetAllShareboards(c, db, firestoreClient)
		// })
		// routes.GET("/detail/:shareboardId", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		// 	GetShareboardDetail(c, db, firestoreClient)
		// })
		// routes.DELETE("/delete/:shareboardId", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		// 	DeleteShareboard(c, db, firestoreClient)
		// })
	}
}

func CreateShareboard(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	boardID := c.Param("boardid")

	// แปลง boardID จาก string เป็น int
	boardIDInt, err := strconv.Atoi(boardID)
	if err != nil {
		c.JSON(400, gin.H{
			"error": "Invalid board ID format",
		})
		return
	}

	// ตรวจสอบว่า board มีอยู่จริงและเป็นของ user นี้
	var board model.Board
	if err := db.Where("board_id = ? AND create_by = ?", boardIDInt, userId).First(&board).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(404, gin.H{
				"error": "Board not found or access denied",
			})
			return
		}
		c.JSON(500, gin.H{
			"error": "Database error",
		})
		return
	}

	// สร้าง token แบบสุ่ม
	token, err := generateSecureToken(32) // 32 bytes = 64 hex characters
	if err != nil {
		c.JSON(500, gin.H{
			"error": "Failed to generate token",
		})
		return
	}

	// กำหนดเวลาหมดอายุ (7 วันจากตอนนี้)
	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	// สร้าง BoardToken record
	boardToken := model.BoardToken{
		BoardID:   boardIDInt,
		Token:     token,
		ExpiresAt: expiresAt,
		CreateAt:  time.Now(),
	}

	// บันทึกลงฐานข้อมูล
	if err := db.Create(&boardToken).Error; err != nil {
		c.JSON(500, gin.H{
			"error": "Failed to create share token",
		})
		return
	}

	// สร้าง URL แชร์พร้อม token
	shareURL := "https://api-mydayplanner.onrender.com/board/" + boardID + "?token=" + token

	c.JSON(200, gin.H{
		"message":    "Share link created successfully",
		"share_url":  shareURL,
		"token":      token,
		"expires_at": expiresAt.Format(time.RFC3339),
		"board_id":   boardIDInt,
	})
}

// generateSecureToken สร้าง token แบบสุ่มที่ปลอดภัย
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// ValidateShareToken ฟังก์ชันสำหรับตรวจสอบ token (ใช้ในการเข้าถึงลิงก์)
func ValidateShareToken(c *gin.Context, db *gorm.DB) {
	boardID := c.Param("boardid")
	token := c.Query("token")

	if token == "" {
		c.JSON(401, gin.H{
			"error": "Access token required",
		})
		return
	}

	// แปลง boardID
	boardIDInt, err := strconv.Atoi(boardID)
	if err != nil {
		c.JSON(400, gin.H{
			"error": "Invalid board ID format",
		})
		return
	}

	// ตรวจสอบ token ในฐานข้อมูล
	var boardToken model.BoardToken
	if err := db.Where("board_id = ? AND token = ? AND expires_at > ?",
		boardIDInt, token, time.Now()).First(&boardToken).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(401, gin.H{
				"error": "Invalid or expired token",
			})
			return
		}
		c.JSON(500, gin.H{
			"error": "Database error",
		})
		return
	}

	// ดึงข้อมูล board
	var board model.Board
	if err := db.Where("board_id = ?", boardIDInt).First(&board).Error; err != nil {
		c.JSON(404, gin.H{
			"error": "Board not found",
		})
		return
	}

	c.JSON(200, gin.H{
		"message":          "Access granted",
		"board":            board,
		"token_expires_at": boardToken.ExpiresAt.Format(time.RFC3339),
	})
}

// RevokeShareToken ฟังก์ชันสำหรับยกเลิก token
func RevokeShareToken(c *gin.Context, db *gorm.DB) {
	userId := c.MustGet("userId").(uint)
	boardID := c.Param("boardid")
	token := c.Param("token")

	boardIDInt, err := strconv.Atoi(boardID)
	if err != nil {
		c.JSON(400, gin.H{
			"error": "Invalid board ID format",
		})
		return
	}

	// ตรวจสอบว่า user เป็นเจ้าของ board
	var board model.Board
	if err := db.Where("board_id = ? AND user_id = ?", boardIDInt, userId).First(&board).Error; err != nil {
		c.JSON(404, gin.H{
			"error": "Board not found or access denied",
		})
		return
	}

	// ลบ token
	if err := db.Where("board_id = ? AND token = ?", boardIDInt, token).Delete(&model.BoardToken{}).Error; err != nil {
		c.JSON(500, gin.H{
			"error": "Failed to revoke token",
		})
		return
	}

	c.JSON(200, gin.H{
		"message": "Token revoked successfully",
	})
}
