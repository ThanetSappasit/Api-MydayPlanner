package middleware

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func AccessTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.Request.Header.Get("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "Authorization header is missing"})
			return
		}

		tokenString := strings.Replace(header, "Bearer ", "", 1)
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			// ตรวจสอบว่า token ใช้ algorithm HMAC
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			hmacSampleSecret := []byte(os.Getenv("JWT_SECRET_KEY"))
			return hmacSampleSecret, nil
		})

		if err != nil {
			c.AbortWithStatusJSON(403, gin.H{"error": "Token is expired or invalid: " + err.Error()})
			return
		}

		if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
			// เก็บ claims ทั้งหมด
			c.Set("claims", claims)

			// แปลงค่า userID จาก float64 เป็น uint
			if userIDFloat, ok := claims["userId"].(float64); ok {
				userID := uint(userIDFloat)
				c.Set("userId", userID)
			} else {
				c.AbortWithStatusJSON(401, gin.H{"error": "Invalid userId in token claims"})
				return
			}

			c.Next()
		} else {
			c.AbortWithStatusJSON(401, gin.H{"error": "Invalid token claims"})
			return
		}
	}
}

func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		claimsValue, exists := c.Get("claims")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Claims not found"})
			return
		}

		claims, ok := claimsValue.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid claims format"})
			return
		}

		role, ok := claims["role"].(string)
		if !ok || role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
			return
		}

		c.Next()
	}
}

func RefreshTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// รับ refresh token จาก Header
		authHeader := c.Request.Header.Get("Authorization")
		if authHeader == "" {
			c.JSON(401, gin.H{"error": "Refresh token is missing"})
			c.Abort()
			return
		}

		// ตรวจสอบรูปแบบของ token
		bearerToken := strings.Split(authHeader, " ")
		if len(bearerToken) != 2 || bearerToken[0] != "Bearer" {
			c.JSON(401, gin.H{"error": "Invalid token format"})
			c.Abort()
			return
		}

		refreshToken := bearerToken[1]

		// Decode และตรวจสอบ token โดยใช้ JWT key
		hmacSampleSecret := []byte(os.Getenv("JWT_REFRESH_SECRET_KEY"))
		token, err := jwt.Parse(refreshToken, func(token *jwt.Token) (interface{}, error) {
			// ตรวจสอบว่า token ใช้ algorithm HMAC
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return hmacSampleSecret, nil
		})

		if err != nil {
			c.JSON(403, gin.H{"error": "Invalid refresh token: " + err.Error()})
			c.Abort()
			return
		}

		if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
			// ตรวจสอบว่า token หมดอายุหรือไม่ (ถ้ามีการกำหนด expiration ใน claims)
			if exp, ok := claims["expiresAt"].(float64); ok {
				if int64(exp) < time.Now().Unix() {
					c.JSON(401, gin.H{"error": "Refresh token has expired"})
					c.Abort()
					return
				}
			}

			// ดึง userID จาก claims
			var userID uint

			// ตรวจสอบว่า claims["userId"] หรือ claims["UserID"] มีค่าหรือไม่
			if userIDFloat, ok := claims["userId"].(float64); ok {
				userID = uint(userIDFloat)
			} else if userIDFloat, ok := claims["UserID"].(float64); ok {
				userID = uint(userIDFloat)
			} else {
				c.JSON(401, gin.H{"error": "Invalid token claims: UserID not found"})
				c.Abort()
				return
			}

			// เก็บข้อมูลที่จำเป็นไว้ใน context เพื่อให้ handler สามารถเข้าถึงได้
			c.Set("userID", userID)
			c.Set("refreshToken", refreshToken)

			// ดำเนินการต่อไปยัง handler
			c.Next()
		} else {
			c.JSON(401, gin.H{"error": "Invalid refresh token claims"})
			c.Abort()
			return
		}
	}
}
