// model/checklist.go
package model

import (
	"time"
)

type Checklist struct {
	ChecklistID   int       `gorm:"column:checklist_id;primaryKey;autoIncrement"`
	TaskID        int       `gorm:"column:task_id;not null"`
	ChecklistName string    `gorm:"column:checklist_name;type:varchar(255);not null"`
	AssignedTo    int       `gorm:"column:assigned_to;not null"`
	IsArchive     bool      `gorm:"column:is_archive;default:0"`
	CreateAt      time.Time `gorm:"column:create_at;autoCreateTime"`

	// Relations
	Task         Tasks `gorm:"foreignKey:TaskID;references:TaskID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	AssignedUser User  `gorm:"foreignKey:AssignedTo;references:UserID;constraint:OnUpdate:CASCADE"`
}

func (Checklist) TableName() string {
	return "checklists"
}
