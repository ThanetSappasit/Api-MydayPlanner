// model/report.go
package model

import (
	"time"
)

type Report struct {
	ReportID    int       `gorm:"column:report_id;primaryKey;autoIncrement"`
	UserID      int       `gorm:"column:user_id;not null"`
	Description string    `gorm:"column:description;type:text;not null"`
	CreateAt    time.Time `gorm:"column:create_at;autoCreateTime"`
	Category    string    `gorm:"column:category;type:enum('Suggestions','Incorrect Information','Problems or Issues','Accessibility Issues','Notification Issues','Security Issues');not null"`

	// Relations
	User User `gorm:"foreignKey:UserID;references:UserID;constraint:OnUpdate:CASCADE"`
}

func (Report) TableName() string {
	return "reports"
}

type ReportResponse struct {
	ReportID    int       `gorm:"column:report_id;primaryKey"`
	Category    string    `gorm:"column:category;type:enum('Suggestions','Incorrect Information','Problems or Issues','Accessibility Issues','Notification Issues','Security Issues');not null"`
	Color       string    `gorm:"column:color"`
	CreateAt    time.Time `gorm:"column:create_at;autoCreateTime"`
	Name        string    `gorm:"column:name"`
	Email       string    `gorm:"column:email"`
	Description string    `gorm:"column:description;type:text;not null"`

	// Relations
	User User `gorm:"foreignKey:UserID;references:UserID;constraint:OnUpdate:CASCADE"`
}

func (ReportResponse) TableName() string {
	return "reportsresponse"
}
