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

func CreateBoardController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/board", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
		CreateBoard(c, db, firestoreClient)
	})
}

func CreateBoard(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
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
			"Type":      groupType,
		}

		mainDoc := firestoreClient.Collection("Boards").Doc(user.Email)
		boardDoc := mainDoc.Collection("Boards").Doc(strconv.Itoa(newBoard.BoardID))
		_, err := boardDoc.Set(ctx, boardDataFirebase)
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
