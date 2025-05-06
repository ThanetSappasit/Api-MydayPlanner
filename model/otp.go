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
	Email     string    `gorm:"not null;index"`  // Email associated with OTP
	OTP       string    `gorm:"not null"`        // OTP code
	Reference string    `gorm:"not null;unique"` // Unique reference code
	Is_used   string    `gorm:"not null"`        // Indicates if OTP is used
	CreatedAt time.Time `gorm:"autoCreateTime"`
	ExpiresAt time.Time `gorm:"not null"` // OTP expiration time
}

func (OTPRecord) TableName() string {
	return "OTPRecord"
}
