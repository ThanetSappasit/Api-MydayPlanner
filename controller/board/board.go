package board

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func BoardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/board", middleware.AccessTokenMiddleware())
	{
		routes.GET("/allboards", func(c *gin.Context) {
			GetBoards(c, db, firestoreClient)
		})
		routes.POST("/create", func(c *gin.Context) {
			CreateBoardsFirebase(c, db, firestoreClient)
		})
		routes.DELETE("/delete/:boardID", func(c *gin.Context) {
			DeleteBoard(c, db, firestoreClient)
		})
	}
}

func GetBoards(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// Get all boards for a user
	userId := c.MustGet("userId").(uint)

	var user model.User
	if err := db.Where("user_id = ?", userId).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// 1. ค้นหาบอร์ดที่ผู้ใช้เป็นผู้สร้าง
	var createdBoards []model.Board
	if err := db.Where("create_by = ?", user.UserID).Find(&createdBoards).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch created boards"})
		return
	}
	// 2. ค้นหาบอร์ดที่ผู้ใช้เป็นสมาชิก (จาก board_user)
	var memberBoards []model.Board
	if err := db.Joins("JOIN board_user ON board.board_id = board_user.board_id").
		Where("board_user.user_id = ?", user.UserID).
		Find(&memberBoards).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch member boards"})
		return
	}

	var memberBoardsList []gin.H
	for _, memberBoards := range memberBoards {
		memberBoardsList = append(memberBoardsList, gin.H{
			"board_id":   memberBoards.BoardID,
			"board_name": memberBoards.BoardName,
			"created_by": memberBoards.CreatedBy,
			"created_at": memberBoards.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"created_boards": createdBoards,
		"member_boards":  memberBoards,
	})
}

func CreateBoardsFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var board dto.CreateBoardRequest
	if err := c.ShouldBindJSON(&board); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	// สร้าง context ที่มี timeout เพื่อป้องกันการรอนานเกินไป
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// 1. ลดการ query ข้อมูล user โดยเลือกเฉพาะข้อมูลที่จำเป็น
	var user struct {
		UserID int
		Name   string
	}
	if err := db.Table("user").Select("user_id, name").Where("user_id = ?", board.CreatedBy).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// เตรียมข้อมูลก่อนเริ่ม transaction
	newBoard := model.Board{
		BoardName: board.BoardName,
		CreatedBy: user.UserID,
		CreatedAt: time.Now(),
	}

	// แปลงค่า is_group เป็นชื่อ collection
	groupType := "Private"
	if board.Is_group == "1" {
		groupType = "Group"
	}

	// 2. สร้าง board ใน PostgreSQL ก่อน
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create board: " + err.Error()})
		return
	}

	if board.Is_group == "1" {
		boardUser := model.BoardUser{
			BoardID: newBoard.BoardID,
			UserID:  user.UserID,
			AddedAt: time.Now(),
		}

		if err := tx.Create(&boardUser).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create board user: " + err.Error()})
			return
		}
	}

	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction: " + err.Error()})
		return
	}

	// 3. หลังจาก commit แล้ว ตอนนี้ newBoard.BoardID จะมีค่าแล้ว
	// ส่งข้อมูลไป Firebase ผ่าน goroutine
	errChan := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					errChan <- err
				} else {
					errChan <- fmt.Errorf("panic in Firebase operation: %v", r)
				}
			}
		}()

		boardDataFirebase := gin.H{
			"BoardID":   newBoard.BoardID,
			"BoardName": newBoard.BoardName,
			"CreatedBy": newBoard.CreatedBy,
			"CreatedAt": user.Name,
		}

		mainDoc := firestoreClient.Collection("Boards").Doc(strconv.Itoa(board.CreatedBy))
		subCollection := mainDoc.Collection(fmt.Sprintf("%s_Boards", groupType))
		_, err := subCollection.Doc(strconv.Itoa(newBoard.BoardID)).Set(ctx, boardDataFirebase)
		errChan <- err
	}()

	// รอผลลัพธ์จาก Firebase
	firestoreErr := <-errChan

	// ตรวจสอบ error จาก Firestore
	if firestoreErr != nil {
		// Log error แต่ไม่ fail ทั้งหมด เนื่องจาก PostgreSQL สำเร็จแล้ว
		fmt.Printf("Firestore error (board created in DB): %v\n", firestoreErr)
		c.JSON(http.StatusCreated, gin.H{
			"message": "Board created successfully (with Firestore sync issue)",
			"boardID": newBoard.BoardID,
			"warning": "Firestore sync failed but board was created",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Board created successfully",
		"boardID": newBoard.BoardID,
	})
}

func DeleteBoard(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// Get user ID from token and board ID from path parameter
	userID := c.MustGet("userId").(uint)
	boardIDStr := c.Param("boardID")

	// Convert boardID from string to int
	boardID, err := strconv.Atoi(boardIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid board ID"})
		return
	}

	// Begin transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}

	// Check if board exists
	var board model.Board
	if err := tx.Where("board_id = ?", boardID).First(&board).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusNotFound, gin.H{"error": "Board not found"})
		return
	}

	// Check if user is authorized to delete (only the creator can delete)
	if board.CreatedBy != int(userID) {
		tx.Rollback()
		c.JSON(http.StatusForbidden, gin.H{"error": "Not authorized to delete this board"})
		return
	}

	// Check if it's a group board by looking for entries in board_user
	var boardUsers []model.BoardUser
	if err := tx.Where("board_id = ?", boardID).Find(&boardUsers).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check board type"})
		return
	}

	// Determine board type for Firestore deletion
	boardType := "Private"
	if len(boardUsers) > 0 {
		boardType = "Group"

		// Delete all board_user entries for this board
		if err := tx.Where("board_id = ?", boardID).Delete(&model.BoardUser{}).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete board users"})
			return
		}
	}

	// Delete the board from the database
	if err := tx.Delete(&board).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete board"})
		return
	}

	// Delete from Firestore
	mainDoc := firestoreClient.Collection("Boards").Doc(strconv.Itoa(int(userID)))
	subCollection := mainDoc.Collection(fmt.Sprintf("%s_Boards", boardType))
	_, err = subCollection.Doc(boardIDStr).Delete(c)
	if err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete board from Firestore"})
		return
	}

	// Commit the transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Board deleted successfully",
	})
}
