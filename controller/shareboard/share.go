package shareboard

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/url"
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
	}
}

// ShareToken struct สำหรับเก็บข้อมูล share token
type ShareToken struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	BoardID   uint      `json:"board_id"`
	Token     string    `json:"token" gorm:"uniqueIndex"`
	ExpireAt  time.Time `json:"expire_at"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy uint      `json:"created_by"`
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

	// กำหนด expire time (เช่น 7 วันจากตอนนี้)
	expireAt := time.Now().Add(7 * 24 * time.Hour)

	// สร้าง URL parameters
	params := url.Values{}
	params.Add("boardId", boardID)
	params.Add("expire", strconv.FormatInt(expireAt.Unix(), 10))

	// Encode parameters เป็น string แล้ว encode อีกทีเป็น base64 หรือ hex
	paramsString := params.Encode()

	// Method 1: ใช้ base64 encoding
	encodedParams := base64.URLEncoding.EncodeToString([]byte(paramsString))

	// สร้าง deep link URL ตามรูปแบบที่แสดงในภาพ
	// deepLinkURL := fmt.Sprintf("myapp://mydayplanner-app/source?%s", encodedParams)

	// บันทึก share token ลง database
	shareToken := model.BoardToken{
		BoardID:   boardIDInt,
		Token:     encodedParams,
		ExpiresAt: expireAt,
		CreateAt:  time.Now(),
	}

	if err := db.Create(&shareToken).Error; err != nil {
		c.JSON(500, gin.H{
			"error": "Failed to create share token",
		})
		return
	}

	// Response
	c.JSON(200, gin.H{
		"success":     true,
		"message":     "Share URL created successfully",
		"deep_link":   encodedParams,
		"expire_at":   expireAt.Format(time.RFC3339),
		"expire_unix": expireAt.Unix(),
		"board_id":    boardIDInt,
	})
}

// ฟังก์ชันสำหรับตรวจสอบและใช้ share token
func JoinSharedBoard(c *gin.Context, db *gorm.DB) {
	token := c.Query("token")
	boardIDStr := c.Query("boardId")

	if token == "" || boardIDStr == "" {
		c.JSON(400, gin.H{
			"error": "Missing required parameters",
		})
		return
	}

	// ตรวจสอบ share token
	var shareToken ShareToken
	if err := db.Where("token = ?", token).First(&shareToken).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(404, gin.H{
				"error": "Invalid share link",
			})
			return
		}
		c.JSON(500, gin.H{
			"error": "Database error",
		})
		return
	}

	// ตรวจสอบว่า token หมดอายุหรือยัง
	if time.Now().After(shareToken.ExpireAt) {
		c.JSON(410, gin.H{
			"error": "Share link has expired",
		})
		return
	}

	// ตรวจสอบว่า boardID ตรงกับ token หรือไม่
	boardID, _ := strconv.Atoi(boardIDStr)
	if shareToken.BoardID != uint(boardID) {
		c.JSON(400, gin.H{
			"error": "Invalid board ID for this token",
		})
		return
	}

	// ดึงข้อมูล board
	var board model.Board
	if err := db.Where("board_id = ?", boardID).First(&board).Error; err != nil {
		c.JSON(404, gin.H{
			"error": "Board not found",
		})
		return
	}

	c.JSON(200, gin.H{
		"success": true,
		"board":   board,
		"message": "Successfully joined shared board",
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
