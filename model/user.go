package model

import (
	"time"
)

type User struct {
	UserID         int       `gorm:"column:user_id;primaryKey;autoIncrement"`
	Name           string    `gorm:"column:name;type:varchar(255);not null"`
	Email          string    `gorm:"column:email;type:varchar(255);not null;unique"`
	HashedPassword string    `gorm:"column:hashed_password;type:varchar(255);not null"`
	Profile        string    `gorm:"column:profile;type:varchar(255)"`
	Role           string    `gorm:"column:role;type:enum('user','admin');default:'user'"`
	IsVerify       string    `gorm:"column:is_verify;type:enum('0','1');default:0"`
	IsActive       string    `gorm:"column:is_active;type:enum('0','1','2');default:'1'"`
	CreatedAt      time.Time `gorm:"column:create_at;autoCreateTime"`
}

func (User) TableName() string {
	return "user"
}
