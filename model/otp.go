package model

import "time"

type EmailConfig struct {
	Host     string `yaml:"host" gorm:"column:host"`
	Port     string `yaml:"port" gorm:"column:port"`
	Username string `yaml:"username" gorm:"column:username"`
	Password string `yaml:"password" gorm:"column:password"`
}

// TableName specifies the database table name for GORM
func (EmailConfig) TableName() string {
	return "OTPconfig"
}

type OTPRecord struct {
	Email     string    `firestore:"email"`
	Reference string    `firestore:"reference"`
	Is_used   string    `firestore:"is_used"`
	CreatedAt time.Time `firestore:"createdAt"`
	ExpiresAt time.Time `firestore:"expiresAt"`
	Type      string    `firestore:"type"`
}

func (OTPRecord) TableName() string {
	return "OTPRecord"
}
