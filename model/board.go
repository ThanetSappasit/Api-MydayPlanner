package model

import (
	"time"
)

type Board struct {
	BoardID   int       `gorm:"column:board_id;primaryKey;autoIncrement"`
	BoardName string    `gorm:"column:board_name;type:varchar(255);not null"`
	CreatedAt time.Time `gorm:"column:create_at;autoCreateTime"`
	CreatedBy int       `gorm:"column:create_by;not null"`

	// Relations
	Creator User `gorm:"foreignKey:CreatedBy;references:UserID;constraint:OnUpdate:CASCADE"`
}

func (Board) TableName() string {
	return "board"
}
