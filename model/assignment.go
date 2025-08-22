package model

type Assignment struct {
	AssID  string `gorm:"primaryKey" json:"ass_id"`
	TaskID int    `json:"task_id"`
	UserID int    `json:"user_id"`
}
