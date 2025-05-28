package dto

type AssignedTaskRequest struct {
	BoardID string `json:"board_id" binding:"required"`
	TaskID  string `json:"task_id" binding:"required"`
	UserID  string `json:"user_id" binding:"required"`
}
