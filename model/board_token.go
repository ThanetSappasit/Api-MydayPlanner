// model/board_token.go
package model

import (
	"time"
)

type BoardToken struct {
	TokenID   int       `gorm:"column:token_id;primaryKey;autoIncrement"`
	BoardID   int       `gorm:"column:board_id;not null"`
	Token     string    `gorm:"column:token;type:varchar(255);not null"`
	ExpiresAt time.Time `gorm:"column:expires_at;autoCreateTime"`
	CreateAt  time.Time `gorm:"column:create_at;autoCreateTime"`

	// Relations
	Board Board `gorm:"foreignKey:BoardID;references:BoardID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

func (BoardToken) TableName() string {
	return "board_token"
}
