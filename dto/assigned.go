package dto

type AssignedTaskRequest struct {
	TaskID string `json:"task_id" binding:"required"`
	UserID string `json:"user_id" binding:"required"`
}
