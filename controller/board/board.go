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
	"strings"
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
			InviteBoardFirebase(c, db, firestoreClient)
		})
		routes.POST("/accept", func(c *gin.Context) {
			AcceptInvite(c, db, firestoreClient)
		})
		routes.POST("/addboard/:boardId", func(c *gin.Context) {
			Addboard(c, db, firestoreClient)
		})
		routes.PUT("/adjust", func(c *gin.Context) {
			AdjustBoards(c, db, firestoreClient)
		})
		routes.PUT("/newtoken/:boardId", func(c *gin.Context) {
			NewBoardToken(c, db, firestoreClient)
		})
	}
}

type BoardInvite struct {
	Accept    bool      `firestore:"accept"`
	BoardID   int       `firestore:"board_id"`
	CreatedAt time.Time `firestore:"created_at"`
	InviteID  int       `firestore:"invite_id"`
	InviterID int       `firestore:"inviter_id"`
	UpdatedAt time.Time `firestore:"updated_at"`
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
	if req.BoardID == "" || req.UserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "BoardID and UserID are required"})
		return
	}

	// ตรวจสอบว่า userID จาก MustGet มีอยู่ในระบบจริงหรือไม่
	var inviterUser model.User
	if err := db.Where("user_id = ?", userID).First(&inviterUser).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Inviter user not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify inviter user"})
		}
		return
	}

	// แปลง BoardID และ UserID จาก string เป็น int
	boardIDInt, err := strconv.Atoi(req.BoardID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid BoardID format"})
		return
	}

	inviteeUserIDInt, err := strconv.Atoi(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid UserID format"})
		return
	}

	// ตรวจสอบว่า Board มีอยู่ในระบบหรือไม่
	var board model.BoardUser
	if err := db.Where("board_id = ?", boardIDInt).First(&board).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Board not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get board data"})
		}
		return
	}

	// ตรวจสอบว่า User ที่จะถูกเชิญมีอยู่ในระบบหรือไม่
	var inviteeUser model.User
	if err := db.Where("user_id = ?", inviteeUserIDInt).First(&inviteeUser).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Invitee user not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get invitee user data"})
		}
		return
	}

	// ตรวจสอบว่า User ยังไม่ได้เป็นสมาชิกของ Board นี้
	var existingBoardUser model.BoardUser
	if err := db.Where("board_id = ? AND user_id = ?", boardIDInt, inviteeUserIDInt).First(&existingBoardUser).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User is already a member of this board"})
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing board membership"})
		return
	}

	// หา ID ที่ต่อเนื่องที่ว่างอยู่สำหรับ BoardInvite
	ctx := context.Background()

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

	// หา ID ที่ว่างตัวแรก (เริ่มจาก 1)
	nextID := 1
	for usedIDs[nextID] {
		nextID++
	}
	inviteID := strconv.Itoa(nextID)

	// สร้างข้อมูลสำหรับบันทึกลง Firebase
	inviteData := map[string]interface{}{
		"inviter_id": int(userID),      // ผู้เชิญ
		"invite_id":  inviteeUserIDInt, // ผู้ถูกเชิญ
		"board_id":   boardIDInt,
		"accept":     false, // ค่าเริ่มต้นเป็น false
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
		"message":   "Board invitation sent successfully",
		"invite_id": nextID,
	})
}

func AcceptInvite(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	var req dto.AcceptBoardRequest

	// Bind JSON request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	// Get user data และตรวจสอบว่า user มีอยู่ในระบบหรือไม่
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

	// ดึงข้อมูลจาก Firebase Firestore
	ctx := context.Background()
	docRef := firestoreClient.Collection("BoardInvite").Doc(req.InviteID)
	docSnapshot, err := docRef.Get(ctx)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invite not found"})
		return
	}

	var invite BoardInvite
	if err := docSnapshot.DataTo(&invite); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse invite data"})
		return
	}

	// ตรวจสอบว่า invitation นี้เป็นของ user นี้หรือไม่ (optional security check)
	if invite.InviterID == int(userID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot accept your own invitation"})
		return
	}

	// ตรวจสอบการตอบรับ
	if !req.Accept {
		// หาก Accept เป็น false ให้ลบ document ออกจาก Firestore
		if _, err := docRef.Delete(ctx); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete invitation"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Invitation declined and removed"})
		return
	}

	// หาก Accept เป็น true
	// 1. อัปเดต accept เป็น true ใน Firestore
	if _, err := docRef.Update(ctx, []firestore.Update{
		{Path: "accept", Value: true},
		{Path: "updated_at", Value: time.Now()},
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update invitation"})
		return
	}

	// 2. ตรวจสอบว่า user นี้เป็นสมาชิกของ board นี้แล้วหรือยัง
	var existingBoardUser int64
	db.Model(&model.BoardUser{}).Where("board_id = ? AND user_id = ?", invite.BoardID, userID).Count(&existingBoardUser)

	if existingBoardUser > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "User is already a member of this board"})
		return
	}

	// 3. บันทึกข้อมูลลงใน SQL database
	boardUser := model.BoardUser{
		BoardID: invite.BoardID,
		UserID:  int(userID),
		AddedAt: time.Now(),
	}

	if err := db.Create(&boardUser).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add user to board"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Invitation accepted successfully",
		"board_user_id": boardUser.BoardUserID,
		"board_id":      boardUser.BoardID,
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

func Addboard(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userID := c.MustGet("userId").(uint)
	boardID := c.Param("boardId")

	var user struct {
		UserID int
		Email  string
	}
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userID).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user data"})
		return
	}

	// Convert boardID from string to int
	boardIDInt, err := strconv.Atoi(boardID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid board ID"})
		return
	}

	// Create BoardUser record
	boardUser := model.BoardUser{
		BoardID: boardIDInt,
		UserID:  user.UserID,
	}

	// Insert into database
	if err := db.Create(&boardUser).Error; err != nil {
		// Check if it's a duplicate entry error
		if strings.Contains(err.Error(), "Duplicate entry") || strings.Contains(err.Error(), "UNIQUE constraint failed") {
			c.JSON(http.StatusConflict, gin.H{"error": "User is already added to this board"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add user to board"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "User added to board successfully",
		"board_user_id": boardUser.BoardUserID,
		"board_id":      boardUser.BoardID,
	})
}
