package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"log"
	"mydayplanner/dto"
	"mydayplanner/middleware"
	"mydayplanner/model"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func AuthController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/auth")
	{
		routes.POST("/signin", func(c *gin.Context) {
			Signin(c, db, firestoreClient)
		})
		routes.POST("/signup", func(c *gin.Context) {
			Signup(c, db, firestoreClient)
		})
		routes.POST("/signout", middleware.AccessTokenMiddleware(), func(c *gin.Context) {
			Signout(c, db, firestoreClient)
		})
		routes.POST("/newaccesstoken", middleware.RefreshTokenMiddleware(), func(c *gin.Context) {
			NewAccessToken(c, db, firestoreClient)
		})
		routes.POST("/googlelogin", func(c *gin.Context) {
			GoogleSignIn(c, db, firestoreClient)
		})
		routes.PUT("/resetpassword", func(c *gin.Context) {
			ResetPassword(c, db, firestoreClient)
		})
	}
}

func CreateAccessToken(userID uint, role string) (string, error) {
	hmacSampleSecret := []byte(os.Getenv("JWT_SECRET_KEY"))
	claims := &model.AccessClaims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mydayplanner",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(60 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(hmacSampleSecret)
}

func CreateRefreshToken(userID uint) (string, error) {
	refreshTokenSecret := []byte(os.Getenv("JWT_REFRESH_SECRET_KEY"))
	claims := &model.AccessRefresh{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mydayplanner",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)), // Longer-lived token (7 days)
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(refreshTokenSecret)
}

func HashRefreshToken(token string) (string, error) {
	// ใช้ SHA-256 เพื่อลดความยาวของ token ก่อนส่งเข้า bcrypt
	// SHA-256 จะผลิต hash ที่มีความยาวแน่นอนเป็น 32 bytes (256 bits)
	hash := sha256.Sum256([]byte(token))

	// เอา hash ที่ได้จาก SHA-256 ที่มีความยาวแน่นอนแล้วไปเข้า bcrypt
	hashedToken, err := bcrypt.GenerateFromPassword(hash[:], bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedToken), nil
}

func isValidEmail(email string) error {
	// Check email format with regex
	const emailRegex = `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	re := regexp.MustCompile(emailRegex)
	if !re.MatchString(email) {
		return errors.New("invalid email format")
	}

	// Extract domain from email
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return errors.New("invalid email structure")
	}
	domain := parts[1]

	// Check for MX records
	mxRecords, err := net.LookupMX(domain)
	if err != nil || len(mxRecords) == 0 {
		return errors.New("email domain does not have valid MX records")
	}

	return nil
}

func Signin(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var request dto.SigninRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// ตรวจสอบข้อมูลที่จำเป็น
	if request.Email == "" || request.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Email and password are required"})
		return
	}

	// ค้นหาผู้ใช้จากฐานข้อมูล
	var user model.User
	if err := db.Where("email = ?", request.Email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		}
		return
	}

	// ตรวจสอบรหัสผ่าน
	if err := bcrypt.CompareHashAndPassword([]byte(user.HashedPassword), []byte(request.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid password"})
		return
	}

	// ตรวจสอบสถานะบัญชีผู้ใช้
	switch user.IsActive {
	case "0":
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User account is not active", "status": "0"})
		return
	case "2":
		c.JSON(http.StatusBadRequest, gin.H{"error": "User account is deleted", "status": "2"})
		return
	}

	// ตรวจสอบการยืนยันบัญชี
	if user.IsVerify != "1" {
		c.JSON(http.StatusForbidden, gin.H{"error": "User account is not verified"})
		return
	}

	// สร้าง tokens
	accessToken, err := CreateAccessToken(uint(user.UserID), user.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create access token"})
		return
	}

	refreshToken, err := CreateRefreshToken(uint(user.UserID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create refresh token"})
		return
	}

	// แฮช refresh token
	hashedRefreshToken, err := HashRefreshToken(refreshToken)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash refresh token"})
		return
	}

	// กำหนดค่าเวลาสำหรับ token
	now := time.Now()
	expiresAt := now.Add(7 * 24 * time.Hour).Unix()
	issuedAt := now.Unix()

	// สร้างข้อมูล refresh token
	refreshTokenData := model.TokenResponse{
		UserID:       user.UserID,
		RefreshToken: hashedRefreshToken,
		CreatedAt:    issuedAt,
		Revoked:      false,
		ExpiresIn:    expiresAt - issuedAt,
	}

	// บันทึก refresh token ใน Firestore
	userIDStr := strconv.Itoa(user.UserID)
	if _, err := firestoreClient.Collection("refreshTokens").Doc(userIDStr).Set(c, refreshTokenData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store refresh token"})
		return
	}

	// กำหนดบทบาทผู้ใช้
	role := "user"
	if user.Role == "admin" {
		role = user.Role
	}

	// อัปเดตข้อมูลการเข้าสู่ระบบใน Firestore
	loginData := map[string]interface{}{
		"email":      request.Email,
		"active":     user.IsActive,
		"verify":     user.IsVerify,
		"login":      1,
		"role":       role,
		"updated_at": now,
	}

	// บันทึกข้อมูลการเข้าสู่ระบบใน Firestore
	if _, err := firestoreClient.Collection("usersLogin").Doc(request.Email).Set(c, loginData, firestore.MergeAll); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update login status"})
		return
	}

	// ส่งผลลัพธ์กลับ
	c.JSON(http.StatusOK, gin.H{
		"message": "Login Successfully",
		"token": gin.H{
			"accessToken":  accessToken,
			"refreshToken": refreshToken,
		},
	})
}

func Signup(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var request dto.SignupRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := isValidEmail(request.Email); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	var user model.User
	result := db.Where("email = ?", request.Email).First(&user)
	if result.Error == nil {
		c.JSON(400, gin.H{"error": "Email already exists"})
		return
	}
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to hash password"})
		return
	}

	userData := model.User{
		Name:           request.Name,
		Email:          request.Email,
		HashedPassword: string(hashedPassword),
		Profile:        "none-url",
		Role:           "user",
		IsVerify:       "0",
		IsActive:       "1",
	}

	result = db.Create(&userData)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "Failed to create user"})
		return
	}
	c.JSON(200, gin.H{
		"message": "User created successfully",
		"user": gin.H{
			"userId": userData.UserID,
			"name":   userData.Name,
			"email":  userData.Email,
		},
	})
}

func Signout(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userId").(uint)
	var user model.User
	result := db.First(&user, userId)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			c.JSON(404, gin.H{"error": "User not found"})
			return
		}
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	docRef := firestoreClient.Collection("refreshTokens").Doc(strconv.Itoa(int(userId)))
	_, err := docRef.Delete(c)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to delete refresh token"})
		return
	}
	// อัปเดตข้อมูลการเข้าสู่ระบบใน Firestore ให้ login = 0
	loginData := map[string]interface{}{
		"login": 0,
	}

	if _, err := firestoreClient.Collection("usersLogin").Doc(user.Email).Set(c, loginData, firestore.MergeAll); err != nil {
		c.JSON(500, gin.H{"error": "Failed to update login status"})
		return
	}
	c.JSON(200, gin.H{"message": "Signout successfully"})
}

func NewAccessToken(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	userId := c.MustGet("userID").(uint)
	refreshToken := c.MustGet("refreshToken").(string)
	docRef := firestoreClient.Collection("refreshTokens").Doc(strconv.Itoa(int(userId)))
	docSnap, err := docRef.Get(c)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to get refresh token from database"})
		return
	}
	var tokenData model.TokenResponse
	if err := docSnap.DataTo(&tokenData); err != nil {
		c.JSON(500, gin.H{"error": "Failed to parse token data"})
		return
	}
	// ตรวจสอบว่า token ถูก revoke หรือไม่
	if tokenData.Revoked {
		c.JSON(403, gin.H{"error": "Token has been revoked"})
		return
	}

	// ตรวจสอบว่า token หมดอายุหรือไม่ (เช็คอีกครั้งจากฐานข้อมูล)
	if tokenData.CreatedAt+tokenData.ExpiresIn < time.Now().Unix() {
		c.JSON(440, gin.H{"error": "Token has expired"})
		return
	}
	// ตรวจสอบ token ที่ส่งมากับ hash ที่เก็บไว้
	hash := sha256.Sum256([]byte(refreshToken))
	if err := bcrypt.CompareHashAndPassword([]byte(tokenData.RefreshToken), hash[:]); err != nil {
		c.JSON(401, gin.H{"error": "Invalid refresh token"})
		return
	}

	// ถ้าผ่านการตรวจสอบทั้งหมด ดึงข้อมูล user
	var user model.User
	result := db.First(&user, userId)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			c.JSON(404, gin.H{"error": "User not found"})
			return
		}
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}

	// สร้าง access token ใหม่
	newAccessToken, err := CreateAccessToken(uint(user.UserID), user.Role)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to create new access token"})
		return
	}
	c.JSON(200, gin.H{"accessToken": newAccessToken})
}

func GoogleSignIn(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// รับและตรวจสอบข้อมูลจาก Request
	var req dto.GoogleSignInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "กรุณาระบุข้อมูลให้ครบถ้วนและถูกต้อง",
		})
		return
	}

	// ตรวจสอบข้อมูลที่จำเป็น
	if req.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "กรุณาระบุอีเมล",
		})
		return
	}

	// เริ่ม transaction
	tx := db.Begin()
	if tx.Error != nil {
		log.Printf("Error starting transaction: %v", tx.Error)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "เกิดข้อผิดพลาดในระบบ โปรดลองใหม่อีกครั้ง",
		})
		return
	}

	// ค้นหาผู้ใช้ในฐานข้อมูล
	var user model.User
	result := tx.Where("email = ?", req.Email).First(&user)

	// กำหนดค่าเริ่มต้น
	role := "user"
	var userID uint
	isActive := "1"
	isVerify := "1"
	now := time.Now()

	// กรณีไม่พบผู้ใช้ในระบบ
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// สร้างผู้ใช้ใหม่
		newUser := model.User{
			Name:           req.Name,
			Email:          req.Email,
			Profile:        req.Profile,
			HashedPassword: "-", // Google Sign-In ไม่ต้องใช้รหัสผ่าน
			Role:           role,
			IsActive:       isActive,
			IsVerify:       isVerify,
			CreatedAt:      now.UTC(),
		}

		if err := tx.Create(&newUser).Error; err != nil {
			tx.Rollback()
			log.Printf("Failed to create user: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "ไม่สามารถลงทะเบียนผู้ใช้ได้",
			})
			return
		}

		// ดึง ID ที่เพิ่งสร้าง
		userID = uint(newUser.UserID)
	} else if result.Error != nil {
		// กรณีเกิดข้อผิดพลาดอื่นๆ
		tx.Rollback()
		log.Printf("Database error: %v", result.Error)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "เกิดข้อผิดพลาดในการตรวจสอบข้อมูล",
		})
		return
	} else {
		// กรณีพบผู้ใช้ในระบบ
		userID = uint(user.UserID)

		// ตรวจสอบสถานะบัญชี
		switch user.IsActive {
		case "0":
			tx.Rollback()
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "user account is not active",
				"status":  "0",
			})
			return
		case "2":
			tx.Rollback()
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "user account is deleted",
				"status":  "2",
			})
			return
		}

		// ถ้าผู้ใช้เป็น admin ให้คงสถานะไว้
		if user.Role == "admin" {
			role = "admin"
		}
	}

	// สร้าง tokens
	accessToken, err := CreateAccessToken(userID, role)
	if err != nil {
		tx.Rollback()
		log.Printf("Failed to create access token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "ไม่สามารถสร้างโทเค็นได้",
		})
		return
	}

	refreshToken, err := CreateRefreshToken(userID)
	if err != nil {
		tx.Rollback()
		log.Printf("Failed to create refresh token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "ไม่สามารถสร้างโทเค็นได้",
		})
		return
	}

	// แฮช refresh token
	hashedRefreshToken, err := HashRefreshToken(refreshToken)
	if err != nil {
		tx.Rollback()
		log.Printf("Failed to hash refresh token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "เกิดข้อผิดพลาดในการประมวลผลโทเค็น",
		})
		return
	}

	// กำหนดค่าเวลาสำหรับ token (7 วัน)
	expiresAt := now.Add(7 * 24 * time.Hour).Unix()
	issuedAt := now.Unix()

	// สร้างข้อมูล refresh token
	refreshTokenData := model.TokenResponse{
		UserID:       int(userID),
		RefreshToken: hashedRefreshToken,
		CreatedAt:    issuedAt,
		Revoked:      false,
		ExpiresIn:    expiresAt - issuedAt,
	}

	// Commit transaction database
	if err := tx.Commit().Error; err != nil {
		log.Printf("Failed to commit transaction: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "เกิดข้อผิดพลาดในการบันทึกข้อมูล",
		})
		return
	}
	// บันทึกข้อมูลการเข้าสู่ระบบใน Firestore
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// บันทึก refresh token ใน Firestore
	userIDStr := strconv.Itoa(int(userID))
	if _, err := firestoreClient.Collection("refreshTokens").Doc(userIDStr).Set(ctx, refreshTokenData); err != nil {
		log.Printf("Failed to store refresh token in Firestore: %v", err)
		// ไม่ต้อง rollback เพราะ transaction ได้ commit ไปแล้ว
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "เกิดข้อผิดพลาดในการบันทึกข้อมูลการเข้าสู่ระบบ",
		})
		return
	}

	// เตรียมข้อมูลสำหรับบันทึกใน Firebase
	firebaseData := map[string]interface{}{
		"email":      req.Email,
		"active":     isActive,
		"verify":     isVerify,
		"login":      1,
		"role":       role,
		"updated_at": now,
	}

	// บันทึกข้อมูลการเข้าสู่ระบบใน Firestore
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := firestoreClient.Collection("usersLogin").Doc(req.Email).Set(ctx, firebaseData, firestore.MergeAll); err != nil {
		log.Printf("Failed to update Firebase user data: %v", err)
		// ไม่ต้องส่งข้อผิดพลาดกลับไปยังผู้ใช้ เนื่องจากสามารถเข้าสู่ระบบได้แล้ว
		// การอัปเดตข้อมูลนี้เป็นเพียงข้อมูลเสริม
	}

	// เตรียมข้อมูลผู้ใช้สำหรับส่งกลับ
	var userName string
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		userName = req.Name
	} else {
		userName = user.Name
	}

	// ส่งผลลัพธ์กลับ
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "เข้าสู่ระบบสำเร็จ",
		"status": func() string {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return "registered"
			}
			return "success"
		}(),
		"user": gin.H{
			"id":    userID,
			"email": req.Email,
			"name":  userName,
			"role":  role,
		},
		"token": gin.H{
			"accessToken":  accessToken,
			"refreshToken": refreshToken,
			"expiresIn":    expiresAt - issuedAt,
		},
	})
}

func ResetPassword(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var resetPassword dto.ResetPasswordRequest
	if err := c.ShouldBindJSON(&resetPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	var user model.User
	result := db.Where("email = ?", resetPassword.Email).First(&user)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		}
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(resetPassword.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	if err := db.Model(&user).Update("hashed_password", hashedPassword).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Password reset successfully"})
}
