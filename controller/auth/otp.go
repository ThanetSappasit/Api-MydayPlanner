package auth

import (
	"context"
	"fmt"
	"math/rand"
	"mydayplanner/dto"
	"mydayplanner/model"
	"net/smtp"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"google.golang.org/api/iterator"
	"gorm.io/gorm"
)

func OTPController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	routes := router.Group("/auth")
	{
		routes.POST("/requestresetpassOTP", func(c *gin.Context) {
			RequestResetpasswordOTP(c, db, firestoreClient)
		})
		routes.POST("/requestverifyOTP", func(c *gin.Context) {
			RequestVerifyOTP(c, db, firestoreClient)
		})
	}
}

func RequestResetpasswordOTP(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var emailRequest dto.EmailOTPRequest
	if err := c.ShouldBindJSON(&emailRequest); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request format"})
		return
	}

	// ตรวจสอบว่าอีเมลนี้มีอยู่ในระบบหรือไม่
	var user model.User
	result := db.Where("email = ?", emailRequest.Email).First(&user)
	if result.Error != nil {
		c.JSON(404, gin.H{"error": "Email not found"})
		return
	}

	// ตรวจสอบว่าอีเมลถูกบล็อกหรือไม่
	blocked, err := isEmailBlocked(c, firestoreClient, emailRequest.Email)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to check email status"})
		return
	}
	if blocked {
		c.JSON(403, gin.H{"error": "Too many OTP requests. Please try again later."})
		return
	}

	// ตรวจสอบจำนวนครั้งที่ขอ OTP และบล็อกถ้าเกินกำหนด
	shouldBlock, err := checkAndBlockIfNeeded(c, firestoreClient, emailRequest.Email, "resetpassword")
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to check OTP request count"})
		return
	}
	if shouldBlock {
		c.JSON(403, gin.H{"error": "Too many OTP requests. Your email has been blocked temporarily."})
		return
	}

	// สร้าง OTP และ REF
	otp, err := generateOTP(6)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to generate OTP"})
		return
	}
	ref := generateREF(10)

	// สร้างเนื้อหาอีเมล
	emailContent := generateEmailContent(otp, ref)

	// ส่งอีเมล
	err = sendEmail(emailRequest.Email, "รหัส OTP สำหรับรีเซ็ตรหัสผ่าน", emailContent)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to send email: " + err.Error()})
		return
	}

	// บันทึกข้อมูล OTP ลงใน Firebase
	err = saveOTPRecord(c, firestoreClient, emailRequest.Email, otp, ref, "resetpassword")
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to save OTP record: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"message": "OTP has been sent to your email",
		"ref":     ref,
	})
}

func RequestVerifyOTP(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var emailRequest dto.EmailOTPRequest
	if err := c.ShouldBindJSON(&emailRequest); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request format"})
		return
	}

	// ตรวจสอบว่าอีเมลนี้มีอยู่ในระบบหรือไม่
	var user model.User
	result := db.Where("email = ?", emailRequest.Email).First(&user)
	if result.Error != nil {
		c.JSON(404, gin.H{"error": "Email not found"})
		return
	}

	// ตรวจสอบว่าอีเมลถูกบล็อกหรือไม่
	blocked, err := isEmailBlocked(c, firestoreClient, emailRequest.Email)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to check email status"})
		return
	}
	if blocked {
		c.JSON(403, gin.H{"error": "Too many OTP requests. Please try again later."})
		return
	}

	// ตรวจสอบจำนวนครั้งที่ขอ OTP และบล็อกถ้าเกินกำหนด
	shouldBlock, err := checkAndBlockIfNeeded(c, firestoreClient, emailRequest.Email, "verify")
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to check OTP request count"})
		return
	}
	if shouldBlock {
		c.JSON(403, gin.H{"error": "Too many OTP requests. Your email has been blocked temporarily."})
		return
	}

	// สร้าง OTP และ REF
	otp, err := generateOTP(6)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to generate OTP"})
		return
	}
	ref := generateREF(10)

	// สร้างเนื้อหาอีเมล
	emailContent := generateEmailContent(otp, ref)

	// ส่งอีเมล
	err = sendEmail(emailRequest.Email, "รหัส OTP สำหรับยืนยันตัวตนบัญชีอีเมล", emailContent)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to send email: " + err.Error()})
		return
	}

	// บันทึกข้อมูล OTP ลงใน Firebase
	err = saveOTPRecord(c, firestoreClient, emailRequest.Email, otp, ref, "verify")
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to save OTP record: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"message": "OTP has been sent to your email",
		"ref":     ref,
	})
}

func LoadEmailConfig() (*model.EmailConfig, error) {
	// โหลด .env เฉพาะตอนรัน local (เมื่อ ENV "RENDER" ว่าง)
	if os.Getenv("RENDER") == "" {
		if err := godotenv.Load(); err != nil {
			fmt.Println("Warning: .env file not loaded, fallback to OS env vars")
		}
	}

	config := &model.EmailConfig{
		Host:     os.Getenv("SMTP_HOST"),
		Port:     os.Getenv("SMTP_PORT"),
		Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"),
	}

	if config.Host == "" || config.Port == "" || config.Username == "" || config.Password == "" {
		return nil, fmt.Errorf("missing required SMTP environment variables")
	}

	fmt.Printf("SMTP Config: Host=%s, Port=%s, Username=%s\n", config.Host, config.Port, config.Username)
	return config, nil
}

func generateOTP(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("length must be greater than 0")
	}

	// In Go 1.20+, you don't need to call rand.Seed anymore
	var otp strings.Builder
	for i := 0; i < length; i++ {
		otp.WriteString(string(rune('0' + rand.Intn(10)))) // Random digit 0-9
	}

	return otp.String(), nil
}

func generateREF(length int) string {
	// Define the character set for REF
	const characters = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	// In Go 1.20+, you don't need to call rand.Seed anymore
	var ref strings.Builder
	for i := 0; i < length; i++ {
		randomIndex := rand.Intn(len(characters))
		ref.WriteByte(characters[randomIndex])
	}

	return ref.String()
}

func generateEmailContent(OTP string, REF string) string {
	// สร้าง HTML template สำหรับอีเมล
	emailTemplate := `
        <table width="680px" cellpadding="0" cellspacing="0" border="0">
                            <tbody>
                              <tr>
                                <td width="5%" height="20" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="20" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="80%" height="20" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="20" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="20" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                              </tr>
                              <tr>
                                <td width="5%" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="80%" bgcolor="#eeeeee" align="center"><h1>Myday-Planner</h1></td>
                                <td width="5%" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                              </tr>
                              <tr>
                                <td width="5%" height="20" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="20" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="80%" height="20" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="20" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="20" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                              </tr>
                              <tr>
                                <td width="5%" height="72" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="72" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="80%" height="72" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="72" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="72" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                              </tr>
                              <tr>
                                <td width="5%" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="80%" bgcolor="#ffffff" align="center" valign="top" style="line-height:24px"><font color="#333333" face="Arial"><span style="font-size:20px">สวัสดี!</span></font><br><font color="#333333" face="Arial"><span style="font-size:16px">กรุณานำรหัส <span class="il">OTP</span> ด้านล่าง ไปกรอกในหน้ายืนยัน.</span></font><br></td>
                                <td width="5%" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="5%" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                              </tr>
                              <tr>
                                <td width="5%" height="42" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="42" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="80%" height="42" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="42" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="42" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                              </tr>
                              <tr>
                                <td width="5%" height="72" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="72" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="80%" height="72" bgcolor="#ffffff" align="center" valign="top">
                                  <table width="100%" height="72" cellpadding="0" cellspacing="0" border="0">
                                    <tbody><tr>
                                      <td width="10%" height="1" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="1" height="1" bgcolor="#cc0000" style="font-size:0"></td>
                                      <td width="5%" height="1" bgcolor="#cc0000" style="font-size:0"></td>
                                      <td width="*" height="1" bgcolor="#cc0000" style="font-size:0"></td>
                                      <td width="5%" height="1" bgcolor="#cc0000" style="font-size:0"></td>
                                      <td width="1" height="1" bgcolor="#cc0000" style="font-size:0"></td>
                                      <td width="10%" height="1" bgcolor="#ffffff" style="font-size:0"></td>
                                    </tr>
                                    <tr>
                                      <td width="10%" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="1" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="5%" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="*" height="20" bgcolor="#ffffff" align="center" valign="middle" style="font-size:18px;color:#c00;font-family:Arial">
                                        <span class="il">OTP</span> : <strong style="color:#000">` + OTP + `</strong></td>
                                      <td width="5%" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="1" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="10%" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                    </tr>
                                    <tr>
                                      <td width="10%" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="1" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="5%" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="*" height="20" bgcolor="#ffffff" align="center" valign="middle" style="font-size:18px;color:#c00;font-family:Arial">
                                        <span class="il">Ref</span> : <strong style="color:#000">` + REF + `</strong></td>
                                      <td width="5%" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="1" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="10%" height="20" bgcolor="#ffffff" style="font-size:0"></td>
                                    </tr>
                                    <tr>
                                      <td width="10%" height="1" bgcolor="#ffffff" style="font-size:0"></td>
                                      <td width="1" height="1" bgcolor="#cc0000" style="font-size:0"></td>
                                      <td width="5%" height="1" bgcolor="#cc0000" style="font-size:0"></td>
                                      <td width="*" height="1" bgcolor="#cc0000" style="font-size:0"></td>
                                      <td width="5%" height="1" bgcolor="#cc0000" style="font-size:0"></td>
                                      <td width="1" height="1" bgcolor="#cc0000" style="font-size:0"></td>
                                      <td width="10%" height="1" bgcolor="#ffffff" style="font-size:0"></td>
                                    </tr>
                                  </tbody></table>
                                </td>
                                <td width="5%" height="72" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="72" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                              </tr>	
                              <tr>
                                <td width="5%" height="78" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="78" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="80%" height="78" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="78" bgcolor="#ffffff" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="78" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                              </tr>
                              <tr>
                                <td width="5%" height="54" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="54" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="80%" height="54" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="54" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="54" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                              </tr>
                              <tr>
                                <td width="5%" height="24" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="24" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="80%" height="24" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="24" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                                <td width="5%" height="24" bgcolor="#eeeeee" style="font-size:0">&nbsp;</td>
                              </tr>
                            </tbody>
                          </table>
                          `
	// ไม่ต้องใช้ fmt.Sprintf อีกต่อไปเพราะเราแทรกค่าโดยตรงในแบบ string concatenation
	return emailTemplate
}

func sendEmail(to, subject, body string) error {
	// Load SMTP configuration
	config, err := LoadEmailConfig()
	if err != nil {
		return fmt.Errorf("config loading error: %w", err)
	}

	// Validate SMTP configuration
	if config.Host == "" || config.Port == "" || config.Username == "" || config.Password == "" {
		return fmt.Errorf("incomplete SMTP configuration: host=%q, port=%q, username=%q",
			config.Host, config.Port, config.Username)
	}

	// Set up authentication and server address
	addr := config.Host + ":" + config.Port
	auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)

	// Create email message
	from := config.Username
	mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
	message := "From: " + from + "\n" +
		"To: " + to + "\n" +
		"Subject: " + subject + "\n" +
		mime + "\n" +
		body

	// Send email with better error handling
	fmt.Printf("Sending email to %s via %s...\n", to, addr)
	err = smtp.SendMail(addr, auth, from, []string{to}, []byte(message))
	if err != nil {
		return fmt.Errorf("SMTP send error: %w", err)
	}

	fmt.Println("Email sent successfully")
	return nil
}

// ฟังก์ชันตรวจสอบว่าอีเมลถูกบล็อกหรือไม่
func isEmailBlocked(c context.Context, firestoreClient *firestore.Client, email string) (bool, error) {
	blockedRef := firestoreClient.Collection("EmailBlocked").Doc(email)
	blockedDoc, err := blockedRef.Get(c)

	// ถ้าไม่พบข้อมูล แสดงว่าไม่ถูกบล็อก
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}
		return false, err
	}

	// ถ้าพบข้อมูล ตรวจสอบว่าหมดเวลาบล็อกหรือยัง
	if blockedDoc.Exists() {
		blockData := blockedDoc.Data()
		expiresAt, ok := blockData["expiresAt"].(time.Time)
		if ok {
			// ถ้ายังไม่หมดเวลา ถือว่ายังถูกบล็อกอยู่
			if time.Now().Before(expiresAt) {
				return true, nil
			}

			// ถ้าหมดเวลาแล้ว ลบออกจาก EmailBlocked
			_, err = blockedRef.Delete(c)
			if err != nil {
				return false, err
			}
		}
	}

	return false, nil
}

// ฟังก์ชันตรวจสอบจำนวนครั้งที่ขอ OTP และบล็อกถ้าเกินกำหนด
func checkAndBlockIfNeeded(c context.Context, firestoreClient *firestore.Client, email string, record string) (bool, error) {
	// ค้นหารายการ OTP ที่มีอีเมลตรงกัน
	collectionName := fmt.Sprintf("OTPRecords_%s", record)
	query := firestoreClient.Collection(collectionName).Where("email", "==", email)
	iter := query.Documents(c)
	defer iter.Stop()

	var otpCount int
	currentTime := time.Now()

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return false, err
		}

		// ดึงข้อมูลจากเอกสาร
		data := doc.Data()

		// ตรวจสอบว่ามีฟิลด์ expiresAt หรือไม่
		expiresAt, ok := data["expiresAt"].(time.Time)
		if !ok {
			// ถ้าไม่มีวันหมดอายุหรือรูปแบบไม่ถูกต้อง ให้นับว่าใช้ได้อยู่
			otpCount++
			continue
		}

		// ตรวจสอบว่ายังไม่หมดอายุหรือไม่
		if currentTime.Before(expiresAt) {
			otpCount++
		}
	}

	// ถ้าจำนวน OTP ที่ยังไม่หมดอายุมากกว่า 3 รายการ ให้บล็อกอีเมล
	if otpCount >= 3 {
		err := blockEmail(c, firestoreClient, email)
		if err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

// ฟังก์ชันบล็อกอีเมล
func blockEmail(c context.Context, firestoreClient *firestore.Client, email string) error {
	blockTime := time.Now()
	expireTime := blockTime.Add(10 * time.Minute)

	blockData := map[string]interface{}{
		"email":     email,
		"createdAt": blockTime,
		"expiresAt": expireTime,
	}

	_, err := firestoreClient.Collection("EmailBlocked").Doc(email).Set(c, blockData)

	return err

}

// ฟังก์ชันบันทึกข้อมูล OTP ลงใน Firebase
func saveOTPRecord(c context.Context, firestoreClient *firestore.Client, email, otp, ref string, record string) error {
	expirationTime := time.Now().Add(15 * time.Minute)
	otpData := map[string]interface{}{
		"email":     email,
		"otp":       otp,
		"reference": ref,
		"is_used":   "0",
		"createdAt": time.Now(),
		"expiresAt": expirationTime,
	}

	collectionName := fmt.Sprintf("OTPRecords_%s", record)
	_, err := firestoreClient.Collection(collectionName).Doc(ref).Set(c, otpData)
	return err
}
