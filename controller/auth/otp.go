package auth

import (
	"context"
	"fmt"
	"math/rand"
	"mydayplanner/dto"
	"mydayplanner/model"
	"net/http"
	"net/smtp"
	"os"
	"strconv"
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
		routes.POST("/IdentityOTP", func(c *gin.Context) {
			IdentityOTP(c, db, firestoreClient)
		})
		routes.POST("/resetpasswordOTP", func(c *gin.Context) {
			ResetpasswordOTP(c, db, firestoreClient)
		})
		routes.POST("/sendemail", func(c *gin.Context) {
			Sendemail(c, db, firestoreClient)
		})
		routes.POST("/resendotp", func(c *gin.Context) {
			ResendOTP(c, db, firestoreClient)
		})
		routes.PUT("/verifyOTP", func(c *gin.Context) {
			VerifyOTP(c, db, firestoreClient)
		})
	}
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
func isEmailBlocked(c context.Context, firestoreClient *firestore.Client, email string, recordfirebase string) (bool, error) {
	// เข้าถึง document ของ email ใน collection หลัก
	mainDoc := firestoreClient.Collection("EmailBlocked").Doc(email)
	subCollection := mainDoc.Collection(fmt.Sprintf("EmailBlocked_%s", recordfirebase))
	blockedRef := subCollection.Doc(email)

	// ดึงข้อมูลจาก Firestore
	blockedDoc, err := blockedRef.Get(c)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}
		return false, err
	}

	// ตรวจสอบข้อมูล
	if blockedDoc.Exists() {
		blockData := blockedDoc.Data()
		expiresAt, ok := blockData["expiresAt"].(time.Time)
		if ok {
			if time.Now().Before(expiresAt) {
				return true, nil
			}

			// ลบบันทึกถ้าหมดเวลาแล้ว
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
	// เข้าถึง subcollection ใน document ของ email
	mainDoc := firestoreClient.Collection("OTPRecords").Doc(email)
	subCollection := mainDoc.Collection(fmt.Sprintf("OTPRecords_%s", record))

	// อ่านทุก document ใน subcollection
	iter := subCollection.Documents(c)
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

		data := doc.Data()
		expiresAt, ok := data["expiresAt"].(time.Time)
		if !ok {
			otpCount++
			continue
		}

		if currentTime.Before(expiresAt) {
			otpCount++
		}
	}

	if otpCount >= 3 {
		err := blockEmail(c, firestoreClient, email, record)
		if err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

// ฟังก์ชันบล็อกอีเมล
func blockEmail(c context.Context, firestoreClient *firestore.Client, email string, record string) error {
	blockTime := time.Now()
	expireTime := blockTime.Add(10 * time.Minute)

	blockData := map[string]interface{}{
		"email":     email,
		"createdAt": blockTime,
		"expiresAt": expireTime,
	}

	mainDoc := firestoreClient.Collection("EmailBlocked").Doc(email)
	subCollection := mainDoc.Collection(fmt.Sprintf("EmailBlocked_%s", record))
	_, err := subCollection.Doc(email).Set(c, blockData)

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

	mainDoc := firestoreClient.Collection("OTPRecords").Doc(email)
	subCollection := mainDoc.Collection(fmt.Sprintf("OTPRecords_%s", record))
	_, err := subCollection.Doc(ref).Set(c, otpData)
	return err
}

func IdentityOTP(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req dto.IdentityOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request format"})
		return
	}

	// ตรวจสอบว่าอีเมลนี้มีอยู่ในระบบหรือไม่
	var user model.User
	result := db.Where("email = ?", req.Email).First(&user)
	if result.Error != nil {
		c.JSON(404, gin.H{"error": "Email not found"})
		return
	}

	// ตรวจสอบว่าอีเมลถูกบล็อกหรือไม่
	blocked, err := isEmailBlocked(c, firestoreClient, req.Email, "verify")
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to check email status"})
		return
	}
	if blocked {
		c.JSON(403, gin.H{"error": "Too many OTP requests. Please try again later."})
		return
	}

	// ตรวจสอบจำนวนครั้งที่ขอ OTP และบล็อกถ้าเกินกำหนด
	shouldBlock, err := checkAndBlockIfNeeded(c, firestoreClient, req.Email, "verify")
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to check OTP request count"})
		return
	}
	if shouldBlock {
		c.JSON(403, gin.H{"error": "Too many OTP requests. Your email has been blocked temporarily."})
		return
	}

	ref := generateREF(10)

	c.JSON(200, gin.H{
		"message": "OTP has been sent to your email",
		"ref":     ref,
	})
}

func ResetpasswordOTP(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req dto.ResetpasswordOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request format"})
		return
	}

	// ตรวจสอบว่าอีเมลนี้มีอยู่ในระบบหรือไม่
	var user model.User
	result := db.Where("email = ?", req.Email).First(&user)
	if result.Error != nil {
		c.JSON(404, gin.H{"error": "Email not found"})
		return
	}

	// ตรวจสอบว่าอีเมลถูกบล็อกหรือไม่
	blocked, err := isEmailBlocked(c, firestoreClient, req.Email, "resetpassword")
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to check email status"})
		return
	}
	if blocked {
		c.JSON(403, gin.H{"error": "Too many OTP requests. Please try again later."})
		return
	}

	// ตรวจสอบจำนวนครั้งที่ขอ OTP และบล็อกถ้าเกินกำหนด
	shouldBlock, err := checkAndBlockIfNeeded(c, firestoreClient, req.Email, "resetpassword")
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to check OTP request count"})
		return
	}
	if shouldBlock {
		c.JSON(403, gin.H{"error": "Too many OTP requests. Your email has been blocked temporarily."})
		return
	}

	ref := generateREF(10)

	c.JSON(200, gin.H{
		"message": "OTP has been sent to your email",
		"ref":     ref,
	})
}

func Sendemail(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req dto.SendemailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request format"})
		return
	}

	// ตรวจสอบว่าอีเมลนี้มีอยู่ในระบบหรือไม่
	var user model.User
	result := db.Where("email = ?", req.Email).First(&user)
	if result.Error != nil {
		c.JSON(404, gin.H{"error": "Email not found"})
		return
	}

	// สร้าง OTP และ REF
	otp, err := generateOTP(6)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to generate OTP"})
		return
	}

	// สร้างเนื้อหาอีเมล
	emailContent := generateEmailContent(otp, req.Reference)

	var recordemail string
	var recordfirebase string
	// ส่งอีเมล

	switch req.Record {
	case "1":
		recordemail = "รหัส OTP สำหรับยืนยันตัวตนบัญชีอีเมล"
		recordfirebase = "verify"
	case "2":
		recordemail = "รหัส OTP สำหรับรีเซ็ตรหัสผ่าน"
		recordfirebase = "resetpassword"
	}
	err = sendEmail(req.Email, recordemail, emailContent)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to send email: " + err.Error()})
		return
	}

	// บันทึกข้อมูล OTP ลงใน Firebase
	err = saveOTPRecord(c, firestoreClient, req.Email, otp, req.Reference, recordfirebase)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to save OTP record: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"message": fmt.Sprintf("OTP %s has been sent to your email", recordfirebase),
	})
}

func VerifyOTP(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	// รับข้อมูลจาก request
	var verifyRequest dto.VerifyRequest

	if err := c.ShouldBindJSON(&verifyRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	var user model.User
	result := db.Where("email = ?", verifyRequest.Email).First(&user)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	// ตรวจสอบว่า input ไม่เป็นค่าว่าง
	if verifyRequest.Record == "" || verifyRequest.Reference == "" || verifyRequest.OTP == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Record, Reference and OTP are required"})
		return
	}

	var recordfirebase string
	// ส่งอีเมล

	switch verifyRequest.Record {
	case "1":
		recordfirebase = "verify"
	case "2":
		recordfirebase = "resetpassword"
	}

	// ดึงข้อมูล OTP จาก Firebase โดยใช้ reference
	ctx := c.Request.Context() // ใช้ context จาก request แทนการส่ง c ไปโดยตรง
	mainDoc := firestoreClient.Collection("OTPRecords").Doc(verifyRequest.Email)
	subCollection := mainDoc.Collection(fmt.Sprintf("OTPRecords_%s", recordfirebase))
	docRef := subCollection.Doc(verifyRequest.Reference)

	docSnap, err := docRef.Get(ctx)

	if err != nil {
		if status.Code(err) == codes.NotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Invalid reference code"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve OTP record"})
			fmt.Printf("Firestore error: %v", err) // บันทึก error ที่เกิดขึ้นโดยไม่แสดงให้ user เห็น
		}
		return
	}

	// แปลงข้อมูลจาก Firestore เป็นโครงสร้างข้อมูลที่ใช้งานได้
	var otpRecord model.OTPRecord

	if err := docSnap.DataTo(&otpRecord); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse OTP record"})
		fmt.Printf("Data parsing error: %v", err)
		return
	}

	// ตรวจสอบว่า OTP ถูกใช้ไปแล้วหรือไม่
	if otpRecord.Is_used == "1" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "OTP has already been used"})
		return
	}

	// ตรวจสอบว่า OTP หมดอายุหรือยัง
	currentTime := time.Now()
	if currentTime.After(otpRecord.ExpiresAt) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "OTP has expired"})
		return
	}

	// ตรวจสอบว่า OTP ตรงกันหรือไม่
	if otpRecord.OTP != verifyRequest.OTP {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid OTP"})
		return
	}

	// เริ่ม transaction สำหรับการอัปเดต SQL database (ถ้าจำเป็น)
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		fmt.Printf("Transaction error: %v", err)
		return
	}

	// อัปเดตสถานะ OTP ว่าถูกใช้แล้ว
	_, err = docRef.Update(ctx, []firestore.Update{
		{Path: "is_used", Value: "1"},
	})

	if err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update OTP status"})
		fmt.Printf("Firestore update error: %v", err)
		return
	}

	// ตัวแปรสำหรับเก็บข้อมูลที่จะส่งกลับ
	responseData := gin.H{
		"message": "OTP verified successfully",
	}

	// เงื่อนไขพิเศษสำหรับ OTPRecords_verify
	if recordfirebase == "verify" {
		// อัปเดตคอลัมน์ is_verify เป็น 1 ในตาราง user ของ SQL database
		result := tx.Model(&model.User{}).Where("email = ?", otpRecord.Email).Update("is_verify", 1)

		if result.Error != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user verification status"})
			fmt.Printf("DB update error: %v", result.Error)
			return
		}

		if result.RowsAffected == 0 {
			tx.Rollback()
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}

		// สร้าง tokens
		accessToken, err := CreateAccessToken(uint(user.UserID), user.Role)
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create access token"})
			return
		}

		refreshToken, err := CreateRefreshToken(uint(user.UserID))
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create refresh token"})
			return
		}

		// แฮช refresh token
		hashedRefreshToken, err := HashRefreshToken(refreshToken)
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash refresh token"})
			return
		}

		// กำหนดค่าเวลาสำหรับ token
		now := time.Now()
		expiresAt := now.Add(7 * 24 * time.Hour).Unix() // เปลี่ยนเป็น 7 วันแทน 2 นาที
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
		if _, err := firestoreClient.Collection("refreshTokens").Doc(userIDStr).Set(ctx, refreshTokenData); err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store refresh token"})
			return
		}

		// เฉพาะกรณี verify เท่านั้นที่จะบันทึกข้อมูลลงใน usersLogin
		// เตรียมข้อมูลสำหรับบันทึกหรืออัปเดตใน Firebase
		role := "user"
		isActive := "1"
		isVerify := "1"
		// บันทึกหรืออัปเดตข้อมูลใน Firebase collection "usersLogin"
		_, err = firestoreClient.Collection("usersLogin").Doc(otpRecord.Email).Set(ctx, map[string]interface{}{
			"email":      otpRecord.Email,
			"active":     isActive,
			"verify":     isVerify,
			"login":      0,
			"role":       role,
			"updated_at": time.Now(),
		}, firestore.MergeAll)

		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update Firebase user data"})
			fmt.Printf("Firestore set error: %v", err)
			return
		}

		// เพิ่ม token ในข้อมูลที่จะส่งกลับ
		responseData["accessToken"] = accessToken
		responseData["refreshToken"] = refreshToken
	}

	// commit transaction หากทุกอย่างเรียบร้อย
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		fmt.Printf("Transaction commit error: %v", err)
		return
	}

	// ส่งข้อมูลตอบกลับ (ทำเพียงครั้งเดียว)
	c.JSON(http.StatusOK, responseData)
}

func ResendOTP(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	var req dto.ResendOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request format"})
		return
	}

	// ตรวจสอบว่าอีเมลนี้มีอยู่ในระบบหรือไม่
	var user model.User
	result := db.Where("email = ?", req.Email).First(&user)
	if result.Error != nil {
		c.JSON(404, gin.H{"error": "Email not found"})
		return
	}

	var recordemail string
	var recordfirebase string
	// ส่งอีเมล

	switch req.Record {
	case "1":
		recordemail = "รหัส OTP สำหรับยืนยันตัวตนบัญชีอีเมล"
		recordfirebase = "verify"
	case "2":
		recordemail = "รหัส OTP สำหรับรีเซ็ตรหัสผ่าน"
		recordfirebase = "resetpassword"
	}

	// ตรวจสอบว่าอีเมลถูกบล็อกหรือไม่
	blocked, err := isEmailBlocked(c, firestoreClient, req.Email, recordfirebase)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to check email status"})
		return
	}
	if blocked {
		c.JSON(403, gin.H{"error": "Too many OTP requests. Please try again later."})
		return
	}

	// ตรวจสอบจำนวนครั้งที่ขอ OTP และบล็อกถ้าเกินกำหนด
	shouldBlock, err := checkAndBlockIfNeeded(c, firestoreClient, req.Email, recordfirebase)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to check OTP request count"})
		return
	}
	if shouldBlock {
		c.JSON(403, gin.H{"error": "Too many OTP requests. Your email has been blocked temporarily."})
		return
	}

	// สร้าง OTP และ REF ใหม่
	otp, err := generateOTP(6)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to generate OTP"})
		return
	}

	ref := generateREF(10)

	// สร้างเนื้อหาอีเมล
	emailContent := generateEmailContent(otp, ref)

	err = sendEmail(req.Email, recordemail, emailContent)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to send email: " + err.Error()})
		return
	}

	err = saveOTPRecord(c, firestoreClient, req.Email, otp, ref, recordfirebase)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to save OTP record: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"message": "OTP has been sent to your email",
		"ref":     ref,
	})
}
