package model

import (
	"time"
)

type Tasks struct {
	TaskID      int       `gorm:"column:task_id;primaryKey;autoIncrement"`
	BoardID     int       `gorm:"column:board_id;not null"`
	TaskName    string    `gorm:"column:task_name;type:varchar(255);not null"`
	Description string    `gorm:"column:description;type:text"`
	Status      string    `gorm:"column:status;type:enum('0','1','2');default:'0';not null"`
	Priority    string    `gorm:"column:priority;type:enum('1','2','3')"`
	CreateBy    int       `gorm:"column:create_by"`
	CreateAt    time.Time `gorm:"column:create_at;autoCreateTime"`

	// Relations
	Board   Board `gorm:"foreignKey:BoardID;references:BoardID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	Creator User  `gorm:"foreignKey:CreateBy;references:UserID;constraint:OnUpdate:CASCADE"`
}

func (Tasks) TableName() string {
	return "tasks"
}
