// model/attachment.go
package model

import (
	"time"
)

type Attachment struct {
	AttachmentID int       `gorm:"column:attachment_id;primaryKey;autoIncrement"`
	TasksID      int       `gorm:"column:tasks_id;not null"`
	FileName     string    `gorm:"column:file_name;type:varchar(255);not null"`
	FilePath     string    `gorm:"column:file_path;type:varchar(255);not null"`
	FileType     string    `gorm:"column:file_type;type:enum('picture','pdf','link','');not null"`
	UploadAt     time.Time `gorm:"column:upload_at;autoCreateTime"`

	// Relations
	Task Tasks `gorm:"foreignKey:TasksID;references:TaskID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

func (Attachment) TableName() string {
	return "attachments"
}
