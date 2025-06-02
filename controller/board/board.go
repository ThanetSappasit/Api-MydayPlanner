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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func BoardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/board", middleware.AccessTokenMiddleware())
	{
		routes.POST("/create", func(c *gin.Context) {
			CreateBoardsFirebase(c, db, firestoreClient)
		})
		routes.PUT("/adjust", func(c *gin.Context) {
			AdjustBoards(c, db, firestoreClient)
		})
		routes.DELETE("/delete", func(c *gin.Context) {
			DeleteDataBoard(c, db, firestoreClient)
		})
	}
}

func CreateBoardsFirebase(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
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
		Email  string
	}
	if err := db.Table("user").Select("user_id, name, email").Where("user_id = ?", userId).First(&user).Error; err != nil {
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
			"CreatedAt": newBoard.CreatedAt,
		}

		mainDoc := firestoreClient.Collection("Boards").Doc(user.Email)
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

func DeleteDataBoard(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// Get user ID from token and board ID from path parameter
	userID := c.MustGet("userId").(uint)
	var dataID dto.DeleteBoardRequest
	if err := c.ShouldBindJSON(&dataID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var user model.User
	if err := db.Raw("SELECT user_id, email FROM user WHERE user_id = ?", userID).Scan(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user email"})
		return
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
	boardDocRef := firestoreClient.Collection("Boards").Doc(user.Email).Collection("Boards").Doc(adjustData.BoardID)

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
