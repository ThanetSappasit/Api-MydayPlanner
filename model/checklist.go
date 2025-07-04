// model/checklist.go
package model

import (
	"time"
)

type Checklist struct {
	ChecklistID   int       `gorm:"column:checklist_id;primaryKey;autoIncrement"`
	TaskID        int       `gorm:"column:task_id;not null"`
	ChecklistName string    `gorm:"column:checklist_name;type:varchar(255);not null"`
	Status        string    `gorm:"column:status;type:enum('0','1');default:'0';not null"`
	CreateAt      time.Time `gorm:"column:create_at;autoCreateTime"`

	// Relations
	Task Tasks `gorm:"foreignKey:TaskID;references:TaskID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

func (Checklist) TableName() string {
	return "checklists"
}
