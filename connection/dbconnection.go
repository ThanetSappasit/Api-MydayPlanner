package connection

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func DBConnection() (*gorm.DB, error) {
	// โหลด .env
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Warning: No .env file found or failed to load") // ใช้เฉพาะตอน dev
	}

	// ดึง DSN จาก ENV
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		return nil, fmt.Errorf("environment variable DB_DSN is not set")
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("error connecting to database: %w", err)
	}

	fmt.Println("Database connection successful")
	return db, nil
}
