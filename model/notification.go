package model

import (
	"time"
)

type Notification struct {
	NotificationID   int       `gorm:"column:notification_id;primaryKey;autoIncrement"`
	TaskID           int       `gorm:"column:task_id;not null"`
	DueDate          time.Time `gorm:"column:due_date;not null"`
	RecurringPattern string    `gorm:"column:recurring_pattern;type:varchar(255);default:'onetime'"`
	IsSend           bool      `gorm:"column:is_send;default:0"`
	CreatedAt        time.Time `gorm:"column:created_at;autoCreateTime"`

	// Relations
	Task Tasks `gorm:"foreignKey:TaskID;references:TaskID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

func (Notification) TableName() string {
	return "notification"
}
