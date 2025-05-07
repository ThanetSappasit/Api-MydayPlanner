package admin

import (
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func AdminController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/admin", middleware.AccessTokenMiddleware())
	{
		routes.PUT("/disableactive/:id", middleware.AdminMiddleware(), func(c *gin.Context) {
			DisableUser(c, db, firestoreClient)
		})
		routes.POST("/createadmin", middleware.AdminMiddleware(), func(c *gin.Context) {
			CreateAdmin(c, db, firestoreClient)
		})
	}
}

func DisableUser(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.Param("id")
	if userId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User ID is required"})
		return
	}
	// ค้นหาผู้ใช้ในฐานข้อมูลโดยใช้ email
	var user model.User
	result := db.First(&user, userId)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}

	// สลับสถานะการใช้งาน (toggle)
	newStatus := "1"
	if user.IsActive == "1" {
		newStatus = "0"
	}

	// อัปเดตสถานะในฐานข้อมูลด้วยคำสั่ง SQL เดียว
	if err := db.Model(&user).Update("is_active", newStatus).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user status"})
		return
	}

	// อัปเดตเฉพาะฟิลด์ active ใน Firestore
	_, err := firestoreClient.Collection("usersLogin").Doc(user.Email).Update(c, []firestore.Update{
		{
			Path:  "active",
			Value: newStatus,
		},
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update Firestore document"})
		return
	}

	// ส่งข้อความตอบกลับที่เหมาะสม
	message := "User enabled successfully"
	if newStatus == "0" {
		message = "User disabled successfully"
	}

	c.JSON(http.StatusOK, gin.H{
		"message": message,
		"status":  newStatus,
	})
}

func CreateAdmin(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req dto.AdminRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request or email is required"})
		return
	}

	// ตรวจสอบว่ามีผู้ใช้นี้ในระบบหรือไม่
	var existingUser model.User
	result := db.Where("email = ?", req.Email).First(&existingUser)
	if result.Error == nil {
		// พบผู้ใช้ในระบบแล้ว
		c.JSON(http.StatusConflict, gin.H{"error": "User already exists"})
		return
	} else if result.Error != gorm.ErrRecordNotFound {
		// เกิดข้อผิดพลาดในการค้นหา
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	// แฮชรหัสผ่านโดยใช้ bcrypt
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.HashedPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// สร้างผู้ใช้ใหม่
	defaultProfile := "none-url"
	newUser := model.User{
		Email:          req.Email,
		HashedPassword: string(hashedPassword),
		Name:           "admin",
		Profile:        defaultProfile,
		Role:           "admin",
		IsVerify:       "0",
		IsActive:       "1",
		CreatedAt:      time.Now(),
	}

	// บันทึกข้อมูลผู้ใช้
	if err := db.Create(&newUser).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Admin user created successfully",
		"email":   req.Email,
		"role":    "admin",
	})
}
