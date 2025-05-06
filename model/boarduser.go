package model

import (
	"time"
)

type BoardUser struct {
	BoardUserID int       `gorm:"column:board_user_id;primaryKey;autoIncrement"`
	BoardID     int       `gorm:"column:board_id;not null"`
	UserID      int       `gorm:"column:user_id;not null"`
	AddedAt     time.Time `gorm:"column:added_at;autoCreateTime"`

	// Relations
	Board Board `gorm:"foreignKey:BoardID;references:BoardID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	User  User  `gorm:"foreignKey:UserID;references:UserID;constraint:OnUpdate:CASCADE"`
}

func (BoardUser) TableName() string {
	return "board_user"
}
