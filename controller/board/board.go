package board

import (
	"errors"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func BoardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/board", middleware.AccessTokenMiddleware())
	{
		routes.POST("/invite", func(c *gin.Context) {
			InviteBoards(c, db, firestoreClient)
		})
		routes.PUT("/adjust", func(c *gin.Context) {
			AdjustBoards(c, db, firestoreClient)
		})
	}
}

func AdjustBoards(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	var adjustData dto.AdjustBoardRequest
	if err := c.ShouldBindJSON(&adjustData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Validate input
	if adjustData.BoardID == "" || adjustData.BoardName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "BoardID and BoardName are required"})
		return
	}

	var user struct {
		UserID int
		Email  string
	}
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userID).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	ctx := c.Request.Context()

	// สร้าง reference ไปยัง board document
	boardDocRef := firestoreClient.Collection("Boards").Doc(adjustData.BoardID)

	// ตรวจสอบว่า board มีอยู่จริงหรือไม่
	boardDoc, err := boardDocRef.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Board not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get board data"})
		return
	}

	// ตรวจสอบว่า board เป็นของ user นี้หรือไม่ (optional: ตรวจสอบ CreatedBy field)
	boardData := boardDoc.Data()
	if createdBy, ok := boardData["CreatedBy"]; ok {
		if createdByInt, ok := createdBy.(int64); ok && int(createdByInt) != user.UserID {
			c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to modify this board"})
			return
		}
	}

	// เริ่ม transaction เพื่ออัปเดตทั้ง Firebase และ SQL
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// อัปเดตใน SQL Database
	result := tx.Exec("UPDATE board SET board_name = ? WHERE board_id = ?",
		adjustData.BoardName, adjustData.BoardID)

	if result.Error != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to update board in database: %v", result.Error),
		})
		return
	}

	// ตรวจสอบว่ามีการอัปเดตจริงหรือไม่
	if result.RowsAffected == 0 {
		tx.Rollback()
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Board not found or you don't have permission to modify this board",
		})
		return
	}

	// อัปเดตใน Firebase
	_, err = boardDocRef.Update(ctx, []firestore.Update{
		{
			Path:  "BoardName",
			Value: adjustData.BoardName,
		},
	})

	if err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to update board in Firebase: %v", err),
		})
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to commit transaction: %v", err),
		})
		return
	}

	// ส่ง response กลับ
	c.JSON(http.StatusOK, gin.H{
		"message":    "Board updated successfully",
		"board_id":   adjustData.BoardID,
		"board_name": adjustData.BoardName,
	})
}

func InviteBoards(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	var req dto.InviteBoardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Validate input
	if req.BoardID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "BoardID is required"})
		return
	}

	// Convert BoardID from string to int early for validation
	var boardIDInt int
	_, err := fmt.Sscanf(req.BoardID, "%d", &boardIDInt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid BoardID format"})
		return
	}

	// Get user data
	var user struct {
		UserID int
		Email  string
	}
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userID).Scan(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		}
		return
	}

	// Get board data
	var board model.Board
	if err := db.Raw("SELECT * FROM board WHERE board_id = ?", boardIDInt).Scan(&board).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Board not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get board data"})
		}
		return
	}

	// Check if user is already invited to this board
	var existingBoardUser model.BoardUser
	if err := db.Where("board_id = ? AND user_id = ?", boardIDInt, user.UserID).First(&existingBoardUser).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User is already a member of this board"})
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing board membership"})
		return
	}

	// Create new board user relationship
	newBoard := model.BoardUser{
		BoardID: boardIDInt,
		UserID:  user.UserID,
		AddedAt: time.Now(),
	}

	// Start database transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database transaction error"})
		return
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Create(&newBoard).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to invite user to board: " + err.Error()})
		return
	}

	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "User invited to board successfully",
		"boardID": newBoard.BoardID,
	})
}
