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
	firebase "firebase.google.com/go"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/api/option"
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
		routes.POST("/googlelogin", middleware.RefreshTokenMiddleware(), func(c *gin.Context) {
			GoogleSignIn(c, db, firestoreClient)
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
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(4 * time.Minute)),
			// ExpiresAt: jwt.NewNumericDate(time.Now().Add(30 * time.Minute)),
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
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Minute)), // Longer-lived token (7 days)
			// ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)), // Longer-lived token (7 days)
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
	expiresAt := now.Add(2 * time.Minute).Unix()
	// expiresAt := now.Add(7 * 24 * time.Hour).Unix()
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
	docRef := firestoreClient.Collection("refreshTokens").Doc(strconv.Itoa(int(userId)))
	_, err := docRef.Delete(c)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to delete refresh token"})
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
		c.JSON(401, gin.H{"error": "Token has been revoked"})
		return
	}

	// ตรวจสอบว่า token หมดอายุหรือไม่ (เช็คอีกครั้งจากฐานข้อมูล)
	if tokenData.CreatedAt+tokenData.ExpiresIn < time.Now().Unix() {
		c.JSON(401, gin.H{"error": "Token has expired"})
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
	var req dto.GoogleSignInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request format"})
		return
	}

	// ตรวจสอบว่า Token ID ไม่ว่างเปล่า
	if req.TokenID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Token ID is required"})
		return
	}

	ctx := context.Background()

	// ใช้ค่า Firebase App จาก global variable หรือ dependency injection ถ้าเป็นไปได้
	// แทนที่จะสร้างใหม่ทุกครั้งที่มีการเรียกใช้งาน
	app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")))
	if err != nil {
		log.Printf("Error initializing Firebase app: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Authentication service unavailable",
		})
		return
	}

	authClient, err := app.Auth(ctx)
	if err != nil {
		log.Printf("Error getting Auth client: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Authentication service error",
		})
		return
	}

	// ตรวจสอบ ID token
	token, err := authClient.VerifyIDToken(ctx, req.TokenID)
	if err != nil {
		log.Printf("Error verifying ID token: %v\n", err)
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid authentication token",
		})
		return
	}

	// ดึงข้อมูลผู้ใช้จาก Firebase
	userRecord, err := authClient.GetUser(ctx, token.UID)
	if err != nil {
		log.Printf("Error fetching user data: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve user information",
		})
		return
	}

	// ตรวจสอบว่ามีอีเมลหรือไม่
	if userRecord.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Email not found in authentication data",
		})
		return
	}

	// เริ่มทำ database transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to begin transaction"})
		return
	}

	// ค้นหาผู้ใช้จากฐานข้อมูล
	var user model.User
	result := tx.Where("email = ?", userRecord.Email).First(&user)

	// กำหนดค่าเริ่มต้น
	role := "user"
	var userID uint

	// กรณีที่ผู้ใช้ไม่มีในระบบ ให้ลงทะเบียนใหม่
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		newUser := model.User{
			Name:           userRecord.DisplayName,
			Email:          userRecord.Email,
			Profile:        userRecord.PhotoURL,
			HashedPassword: "-", // Google Sign-In ไม่ต้องใช้รหัสผ่าน
			Role:           role,
			IsActive:       "1",
			IsVerify:       "1",
			CreatedAt:      time.Now().UTC(),
		}

		if err := tx.Create(&newUser).Error; err != nil {
			tx.Rollback()
			log.Printf("Error creating new user: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to register user"})
			return
		}
		userID = uint(newUser.UserID)
	} else if result.Error != nil {
		// กรณีที่เกิดข้อผิดพลาดอื่นๆ นอกเหนือจาก record not found
		tx.Rollback()
		log.Printf("Database error: %v\n", result.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Database error"})
		return
	} else {
		// กรณีที่พบผู้ใช้ในระบบ
		userID = uint(user.UserID)

		// ตรวจสอบสถานะบัญชีผู้ใช้
		switch user.IsActive {
		case "0":
			tx.Rollback()
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "User account is inactive", "status": "0"})
			return
		case "2":
			tx.Rollback()
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "User account has been deleted", "status": "2"})
			return
		}

		// ถ้าผู้ใช้เป็น admin ให้คงสถานะนั้นไว้
		if user.Role == "admin" {
			role = "admin"
		}
	}

	// สร้าง access token และ refresh token
	accessToken, err := CreateAccessToken(userID, role)
	if err != nil {
		tx.Rollback()
		log.Printf("Error creating access token: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create authentication tokens"})
		return
	}

	refreshToken, err := CreateRefreshToken(userID)
	if err != nil {
		tx.Rollback()
		log.Printf("Error creating refresh token: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create authentication tokens"})
		return
	}

	// แฮช refresh token
	hashedRefreshToken, err := HashRefreshToken(refreshToken)
	if err != nil {
		tx.Rollback()
		log.Printf("Error hashing refresh token: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Token processing error"})
		return
	}

	// กำหนดเวลาสำหรับ token
	now := time.Now()
	// เพิ่มเวลาหมดอายุให้มากขึ้น (15 นาที จะเหมาะสมกว่า 2 นาที)
	expiresAt := now.Add(15 * time.Minute).Unix()
	issuedAt := now.Unix()

	// สร้างข้อมูล refresh token
	refreshTokenData := model.TokenResponse{
		UserID:       int(userID),
		RefreshToken: hashedRefreshToken,
		CreatedAt:    issuedAt,
		Revoked:      false,
		ExpiresIn:    expiresAt - issuedAt,
	}

	// เตรียมข้อมูลสำหรับ Firestore
	userLoginData := map[string]interface{}{
		"email":      userRecord.Email,
		"active":     "1",
		"verify":     "1",
		"login":      1,
		"role":       role,
		"updated_at": now,
	}

	// Commit transaction ก่อนที่จะดำเนินการกับ Firestore
	if err := tx.Commit().Error; err != nil {
		log.Printf("Error committing transaction: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Database error"})
		return
	}

	// บันทึก refresh token ใน Firestore
	userIDStr := strconv.Itoa(int(userID))
	if _, err := firestoreClient.Collection("refreshTokens").Doc(userIDStr).Set(c, refreshTokenData); err != nil {
		log.Printf("Error storing refresh token in Firestore: %v\n", err)
		// ไม่ rollback SQL transaction เพราะได้ commit ไปแล้ว แต่แจ้งเตือนผู้ใช้
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to complete authentication"})
		return
	}

	// บันทึกข้อมูลการเข้าสู่ระบบใน Firestore
	if _, err := firestoreClient.Collection("usersLogin").Doc(userRecord.Email).Set(c, userLoginData, firestore.MergeAll); err != nil {
		log.Printf("Error updating login status: %v\n", err)
		// ไม่ rollback SQL transaction เพราะได้ commit ไปแล้ว แต่เราสามารถส่ง response สำเร็จได้
		// เพราะการอัปเดตสถานะการเข้าสู่ระบบไม่ใช่ operation ที่สำคัญ
	}

	// ส่ง response กลับไปยังผู้ใช้
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Login successful",
		"token": gin.H{
			"accessToken":  accessToken,
			"refreshToken": refreshToken,
			"expiresIn":    expiresAt - issuedAt,
		},
		"user": gin.H{
			"id":    userID,
			"name":  userRecord.DisplayName,
			"email": userRecord.Email,
			"role":  role,
		},
	})
}
