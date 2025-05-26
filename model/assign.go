package model

import (
	"time"
)

type Assigned struct {
	AssID    int       `gorm:"column:ass_id;primaryKey;autoIncrement"`
	TaskID   int       `gorm:"column:task_id;not null"`
	UserID   int       `gorm:"column:user_id;not null"`
	AssignAt time.Time `gorm:"column:assign_at;not null"`

	// Relations
	Task Tasks `gorm:"foreignKey:TaskID;references:TaskID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	User User  `gorm:"foreignKey:UserID;references:UserID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

func (Assigned) TableName() string {
	return "assigned"
}
