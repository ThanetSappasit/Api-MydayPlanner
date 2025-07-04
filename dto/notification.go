package dto

type NotificationRequest struct {
	TaskID           int    `json:"task_id" binding:"required"`
	DueDate          string `json:"due_date" binding:"required"`
	RecurringPattern string `json:"recurring_pattern" binding:"required"`
}
