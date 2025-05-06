package connection

import (
	"context"
	"fmt"
	"log"
	"os"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
	"github.com/joho/godotenv"
	"google.golang.org/api/option"
)

var FirestoreClient *firestore.Client

func FBConnection() (*firestore.Client, error) {
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Warning: No .env file found or failed to load") // Use only in dev
	}

	// Get the path to the service account key from the environment variable
	serviceAccountKeyPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_1")
	if serviceAccountKeyPath == "" {
		return nil, fmt.Errorf("environment variable GOOGLE_APPLICATION_CREDENTIALS is not set")
	}

	ctx := context.Background()

	// Initialize Firebase app with Firestore
	app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(serviceAccountKeyPath))
	if err != nil {
		log.Fatalf("error initializing app: %v\n", err)
		return nil, err
	}

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("error getting Firestore client: %v\n", err)
		return nil, err
	}

	fmt.Println("Firestore connection successful")
	return client, nil
}
