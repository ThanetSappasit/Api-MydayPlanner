package dto

type NotificationRequest struct {
	TaskID           int    `json:"task_id" binding:"required"`
	DueDate          string `json:"due_date" binding:"required"`
	RecurringPattern string `json:"recurring_pattern" binding:"required"`
}

type UpdateNotificationRequest struct {
	DueDate          *string `json:"due_date"`
	BeforeDueDate    *string `json:"before_due_date"`
	RecurringPattern *string `json:"recurring_pattern"`
	IsSend           *bool   `json:"is_send"`
}
