package services

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
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
	return InitializeFirebaseApp(serviceAccountKeyPath)
}

func InitializeFirebaseApp(serviceAccountKeyPath string) (*firebase.App, error) {
	ctx := context.Background()
	opt := option.WithCredentialsFile(serviceAccountKeyPath)
	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		return nil, fmt.Errorf("error initializing app: %v", err)
	}
	return app, nil
}

func SendMulticastNotification(app *firebase.App, tokens []string, title, body string, data map[string]string) error {
	ctx := context.Background()

	client, err := app.Messaging(ctx)
	if err != nil {
		return fmt.Errorf("error getting Messaging client: %v", err)
	}

	// แบ่ง tokens เป็น batches (FCM จำกัดที่ 500 tokens ต่อ request)
	const batchSize = 500
	for i := 0; i < len(tokens); i += batchSize {
		end := i + batchSize
		if end > len(tokens) {
			end = len(tokens)
		}

		batch := tokens[i:end]
		message := &messaging.MulticastMessage{
			Data: data,
			Notification: &messaging.Notification{
				Title: title,
				Body:  body,
			},
			Tokens: batch,
		}

		response, err := client.SendEachForMulticast(ctx, message)
		if err != nil {
			fmt.Printf("Error sending batch %d-%d: %v", i, end-1, err)
			continue
		}

		fmt.Printf("Batch %d-%d: Success: %d, Failure: %d",
			i, end-1, response.SuccessCount, response.FailureCount)

		if response.FailureCount > 0 {
			for idx, resp := range response.Responses {
				if !resp.Success {
					fmt.Printf("Failed to send to token %s: %v", batch[idx], resp.Error)
				}
			}
		}
	}

	return nil
}
