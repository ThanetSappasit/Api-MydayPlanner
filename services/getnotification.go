package services

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"github.com/joho/godotenv"
	"google.golang.org/api/option"
)

func GetFMCTokenData(firestoreClient *firestore.Client, email string) (string, error) {
	ctx := context.Background()
	doc, err := firestoreClient.Collection("usersLogin").Doc(email).Get(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get document: %v", err)
	}

	if !doc.Exists() {
		return "", fmt.Errorf("user login data not found")
	}

	data := doc.Data()
	fcmTokenInterface, exists := data["FMCToken"]
	if !exists {
		return "", fmt.Errorf("FCM token not found for user")
	}

	fcmToken, ok := fcmTokenInterface.(string)
	if !ok || fcmToken == "" {
		return "", fmt.Errorf("invalid or empty FCM token")
	}

	return fcmToken, nil
}

func GetFirebaseApp() (*firebase.App, error) {
	// โหลด .env (เฉพาะกรณียังไม่โหลดที่อื่น)
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Warning: No .env file found or failed to load")
	}

	// อ่าน path ไปยัง service account
	serviceAccountKeyPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_1")
	if serviceAccountKeyPath == "" {
		return nil, fmt.Errorf("firebase credentials not configured")
	}

	// เรียกใช้ initializeFirebaseApp
	return initializeFirebaseApp(serviceAccountKeyPath)
}

func initializeFirebaseApp(serviceAccountKeyPath string) (*firebase.App, error) {
	ctx := context.Background()
	opt := option.WithCredentialsFile(serviceAccountKeyPath)
	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		return nil, fmt.Errorf("error initializing app: %v", err)
	}
	return app, nil
}
