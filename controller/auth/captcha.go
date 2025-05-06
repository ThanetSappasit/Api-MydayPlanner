package auth

import (
	"context"
	"fmt"
	"mydayplanner/dto"
	"os"
	"strings"

	"cloud.google.com/go/firestore"
	recaptcha "cloud.google.com/go/recaptchaenterprise/v2/apiv1"
	"cloud.google.com/go/recaptchaenterprise/v2/apiv1/recaptchaenterprisepb"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/option"
	"gorm.io/gorm"
)

// ResponseData โครงสร้างข้อมูลสำหรับส่งกลับ
type ResponseData struct {
	Success bool     `json:"success"`
	Score   float32  `json:"score,omitempty"`
	Action  string   `json:"action,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
	Message string   `json:"message,omitempty"`
}

func CaptchaController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/auth")
	{
		routes.POST("/captcha", func(c *gin.Context) {
			VerifyCaptcha(c, db, firestoreClient)
		})
	}
}

func VerifyCaptcha(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req dto.CaptchaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{
			"success": false,
			"message": "Invalid request format",
		})
		return
	}

	// ตรวจสอบข้อมูลที่จำเป็น
	if req.Token == "" {
		c.JSON(400, gin.H{
			"success": false,
			"message": "Token is required",
		})
		return
	}

	// ดึง IP address ของผู้ใช้แบบมีประสิทธิภาพ
	userIPAddress := getClientIP(c)
	userAgent := c.Request.UserAgent()

	// ดึงค่า env แบบรวมครั้งเดียว
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT_ID")
	recaptchaKey := os.Getenv("RECAPTCHA_SITE_KEY")
	credentialsPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_2")

	// เรียกใช้ createAssessment เพื่อตรวจสอบ reCAPTCHA
	result, err := createAssessment(c.Request.Context(), projectID, recaptchaKey, credentialsPath, req.Token, req.Action, userIPAddress, userAgent)

	if err != nil {
		fmt.Printf("❌ Error verifying reCAPTCHA: %v\n", err)
		c.JSON(500, gin.H{
			"success": false,
			"message": "Internal server error",
		})
		return
	}

	if result == nil {
		c.JSON(400, gin.H{
			"success": false,
			"message": "reCAPTCHA verification failed",
		})
		return
	}

	// ส่งผลลัพธ์กลับ
	c.JSON(200, gin.H{
		"success": true,
		"score":   result.Score,
		"action":  result.Action,
		"reasons": result.Reasons,
		"message": "Captcha verified successfully",
	})
}

// getClientIP ฟังก์ชั่นแยกออกมาเพื่อดึง IP แบบมีประสิทธิภาพ
func getClientIP(c *gin.Context) string {
	userIPAddress := c.ClientIP()
	if userIPAddress == "" {
		userIPAddress = c.Request.RemoteAddr
	}
	// ถ้ามีหลาย IP ให้ใช้ตัวแรก
	if idx := strings.Index(userIPAddress, ","); idx != -1 {
		userIPAddress = strings.TrimSpace(userIPAddress[:idx])
	}
	return userIPAddress
}

func createAssessment(ctx context.Context, projectID, recaptchaKey, credentialsPath, token, action, userIPAddress, userAgent string) (*dto.AssessmentResult, error) {
	// สร้าง reCAPTCHA client โดยระบุไฟล์ credentials
	client, err := recaptcha.NewClient(ctx, option.WithCredentialsFile(credentialsPath))
	if err != nil {
		fmt.Printf("❌ Error creating reCAPTCHA client: %v\n", err)
		return nil, err
	}
	defer client.Close()

	// สร้าง request
	projectPath := fmt.Sprintf("projects/%s", projectID)
	req := &recaptchaenterprisepb.CreateAssessmentRequest{
		Parent: projectPath,
		Assessment: &recaptchaenterprisepb.Assessment{
			Event: &recaptchaenterprisepb.Event{
				Token:         token,
				SiteKey:       recaptchaKey,
				UserIpAddress: userIPAddress,
				UserAgent:     userAgent,
			},
		},
	}

	// เรียก API
	response, err := client.CreateAssessment(ctx, req)
	if err != nil {
		fmt.Printf("❌ Error in createAssessment: %v\n", err)
		return nil, err
	}

	// ตรวจสอบความถูกต้องของ token
	if response.TokenProperties == nil || !response.TokenProperties.Valid {
		if response.TokenProperties != nil {
			fmt.Printf("❌ Token invalid: %s\n", response.TokenProperties.InvalidReason)
		} else {
			fmt.Println("❌ Token properties are null or undefined.")
		}
		return nil, nil
	}

	// ตรวจสอบ action ถ้ามีการระบุ
	if action != "" && response.TokenProperties.Action != action {
		fmt.Printf("⚠️ Action mismatch: expected %s, but got %s\n",
			action, response.TokenProperties.Action)
		return nil, nil
	}

	// สร้างผลลัพธ์
	result := &dto.AssessmentResult{
		Action: response.TokenProperties.Action,
	}
	// ตรวจสอบ risk analysis
	if response.RiskAnalysis != nil {
		result.Score = response.RiskAnalysis.Score

		// แปลง reasons
		if len(response.RiskAnalysis.Reasons) > 0 {
			reasons := make([]string, len(response.RiskAnalysis.Reasons))
			for i, reason := range response.RiskAnalysis.Reasons {
				reasons[i] = reason.String()
			}
			result.Reasons = reasons
		}
	}

	return result, nil
}
