package model

import (
	"time"
)

type Notification struct {
	NotificationID   int        `gorm:"column:notification_id;primaryKey;autoIncrement"`
	TaskID           int        `gorm:"column:task_id;not null"`
	DueDate          *time.Time `gorm:"column:due_date"`
	BeforeDueDate    *time.Time `gorm:"column:beforedue_date"`
	Snooze           *time.Time `gorm:"column:snooze"`
	RecurringPattern string     `gorm:"column:recurring_pattern;type:varchar(255);default:'onetime'"`
	IsSend           string     `gorm:"column:is_send;type:enum('0','1','2','3');default:'0'"` // enum string
	CreatedAt        time.Time  `gorm:"column:created_at;autoCreateTime"`

	// Relations
	Task Tasks `gorm:"foreignKey:TaskID;references:TaskID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

func (Notification) TableName() string {
	return "notification"
}
