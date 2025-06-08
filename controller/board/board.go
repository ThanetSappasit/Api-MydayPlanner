package board

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"net/url"
	"strconv"
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
		routes.PUT("/newtoken/:boardId", func(c *gin.Context) {
			NewBoardToken(c, db, firestoreClient)
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

func InviteBoardFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	var req dto.InviteBoardRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// Validate input
	if req.BoardID == "" && req.UserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "BoardID is required"})
		return
	}

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

	// หา ID ที่ต่อเนื่องที่ว่างอยู่สำหรับ BoardInvite
	ctx := context.Background()
	inviteID := ""

	// ดึงข้อมูลทั้งหมดจาก BoardInvite collection
	docs, err := firestoreClient.Collection("BoardInvite").Documents(ctx).GetAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get existing invites: " + err.Error()})
		return
	}

	// สร้าง map เพื่อเก็บ ID ที่ใช้แล้ว
	usedIDs := make(map[int]bool)
	for _, doc := range docs {
		if id, err := strconv.Atoi(doc.Ref.ID); err == nil {
			usedIDs[id] = true
		}
	}

	// หา ID ที่ว่างตัวแรก
	nextID := 1
	for usedIDs[nextID] {
		nextID++
	}
	inviteID = strconv.Itoa(nextID)

	// สร้างข้อมูลสำหรับบันทึกลง Firebase
	inviteData := map[string]interface{}{
		"id":         nextID,
		"user_id":    user.UserID,
		"board_id":   boardIDInt,
		"accept":     false, // ค่าเริ่มต้นเป็น false
		"email":      user.Email,
		"board_name": board.BoardName, // เพิ่มชื่อ board เพื่อใช้งานง่าย
		"created_at": time.Now(),
		"updated_at": time.Now(),
	}

	// บันทึกลง Firebase Firestore
	_, err = firestoreClient.Collection("BoardInvite").Doc(inviteID).Set(ctx, inviteData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save invite to Firebase: " + err.Error()})
		return
	}

	// ส่งผลลัพธ์กลับ
	c.JSON(http.StatusOK, gin.H{
		"message": "Board invitation sent successfully",
		"data": gin.H{
			"invite_id":  nextID,
			"user_id":    user.UserID,
			"board_id":   boardIDInt,
			"email":      user.Email,
			"accept":     false,
			"created_at": time.Now(),
		},
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

func NewBoardToken(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	BoardID := c.Param("boardId")

	// แปลง BoardID จาก string เป็น int
	boardIDInt, err := strconv.Atoi(BoardID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid BoardID"})
		return
	}

	// เริ่ม transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction: " + tx.Error.Error()})
		return
	}

	// ใช้ defer เพื่อทำ rollback หากเกิดข้อผิดพลาด
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// ตรวจสอบว่ามี token อยู่แล้วหรือไม่
	var existingToken model.BoardToken
	result := tx.Where("board_id = ?", boardIDInt).First(&existingToken)

	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing token: " + result.Error.Error()})
		return
	}

	// หากไม่มี token อยู่แล้ว ให้ส่งข้อผิดพลาด
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		tx.Rollback()
		c.JSON(http.StatusNotFound, gin.H{"error": "No token found for this board"})
		return
	}

	// ตรวจสอบ token ที่มีอยู่ว่าหมดอายุหรือไม่โดยการ decode
	decodedBytes, err := base64.URLEncoding.DecodeString(existingToken.Token)
	if err != nil {
		// หาก decode ไม่ได้ ให้สร้าง token ใหม่เลย
		fmt.Printf("Warning: Failed to decode existing token: %v\n", err)
	} else {
		// parse URL parameters
		decodedParams, err := url.ParseQuery(string(decodedBytes))
		if err != nil {
			fmt.Printf("Warning: Failed to parse decoded token parameters: %v\n", err)
		} else {
			// ตรวจสอบเวลาหมดอายุ
			if expireStr := decodedParams.Get("expire"); expireStr != "" {
				if expireUnix, err := strconv.ParseInt(expireStr, 10, 64); err == nil {
					expireTime := time.Unix(expireUnix, 0)
					if time.Now().Before(expireTime) {
						// token ยังไม่หมดอายุ
						tx.Rollback()
						c.JSON(http.StatusOK, gin.H{
							"message": "Token is still valid",
							"data": gin.H{
								"token_id":   existingToken.TokenID,
								"board_id":   existingToken.BoardID,
								"token":      existingToken.Token,
								"is_expired": false,
							},
						})
						return
					}
				}
			}
		}
	}

	// สร้างเวลาหมดอายุ (เช่น 7 วันจากตอนนี้)
	expireAt := time.Now().Add(7 * 24 * time.Hour)

	// สร้าง URL parameters
	params := url.Values{}
	params.Add("boardId", BoardID)
	params.Add("expire", strconv.FormatInt(expireAt.Unix(), 10))

	paramsString := params.Encode()

	// Method 1: ใช้ base64 encoding
	encodedParams := base64.URLEncoding.EncodeToString([]byte(paramsString))

	// อัปเดท token ที่มีอยู่แล้ว
	existingToken.Token = encodedParams
	existingToken.ExpiresAt = expireAt
	existingToken.CreateAt = time.Now()

	// บันทึกการอัปเดทลงฐานข้อมูล
	if err := tx.Save(&existingToken).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update board token: " + err.Error()})
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction: " + err.Error()})
		return
	}

	// ส่งข้อมูล token กลับไป
	c.JSON(http.StatusOK, gin.H{
		"message": "Board token updated successfully",
		"data": gin.H{
			"token_id":   existingToken.TokenID,
			"board_id":   existingToken.BoardID,
			"token":      existingToken.Token,
			"is_expired": true,
		},
	})
}
