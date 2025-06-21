package board

import (
	"context"
	"encoding/base64"
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
	isGroupBoard := false
	if board.Is_group == "1" {
		groupType = "Group"
		isGroupBoard = true
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

	var deepLink string
	if isGroupBoard {
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

		// สร้างเวลาหมดอายุ (เช่น 7 วันจากตอนนี้)
		expireAt := time.Now().Add(7 * 24 * time.Hour)

		// สร้าง URL parameters
		params := url.Values{}
		params.Add("boardId", strconv.Itoa(newBoard.BoardID))
		params.Add("expire", strconv.FormatInt(expireAt.Unix(), 10))

		paramsString := params.Encode()

		// Method 1: ใช้ base64 encoding
		encodedParams := base64.URLEncoding.EncodeToString([]byte(paramsString))

		// บันทึก share token ลง database
		shareToken := model.BoardToken{
			BoardID:   newBoard.BoardID,
			Token:     encodedParams,
			ExpiresAt: expireAt,
			CreateAt:  time.Now(),
		}

		if err := tx.Create(&shareToken).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create share token: " + err.Error()})
			return
		}

		deepLink = encodedParams
	}

	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction: " + err.Error()})
		return
	}

	// สร้าง response object
	response := gin.H{
		"message": "Board created successfully",
		"boardID": newBoard.BoardID,
	}

	// เพิ่ม deep_link ถ้าเป็น group board
	if isGroupBoard && deepLink != "" {
		response["deep_link"] = deepLink
	}

	// 3. บันทึกข้อมูลลง Firestore เฉพาะเมื่อเป็น Group Board เท่านั้น
	if isGroupBoard {
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
				"Members":   []int{user.UserID}, // เพิ่ม members array
			}

			boardDoc := firestoreClient.Collection("Boards").Doc(strconv.Itoa(newBoard.BoardID))
			_, err := boardDoc.Set(ctx, boardDataFirebase)
			errChan <- err
		}()

		// รอผลลัพธ์จาก Firebase
		firestoreErr := <-errChan

		// ตรวจสอบ error จาก Firestore
		if firestoreErr != nil {
			// Log error แต่ไม่ fail ทั้งหมด เนื่องจาก PostgreSQL สำเร็จแล้ว
			fmt.Printf("Firestore error (board created in DB): %v\n", firestoreErr)
			response["message"] = "Board created successfully (with Firestore sync issue)"
			response["warning"] = "Firestore sync failed but board was created"
			c.JSON(http.StatusCreated, response)
			return
		}

		// สำหรับ Group Board ที่ sync กับ Firestore สำเร็จ
		response["message"] = "Group board created successfully and synced to Firestore"
	}

	c.JSON(http.StatusCreated, response)
}
