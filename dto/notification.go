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
	IsSend           *string `json:"is_send"`
}

type InviteNotify struct {
	RecieveEmail string `json:"recieveemail" binding:"required"`
	SendingEmail string `json:"sendingemail" binding:"required"`
	BoardID      string `json:"board_id" binding:"required"`
}

type AssignedNotify struct {
	RecieveID string `json:"recieveID" binding:"required"`
	TaskID    string `json:"task_id" binding:"required"`
}

type UnAssignedNotify struct {
	RecieveID string `json:"recieveID" binding:"required"`
	TaskName  string `json:"task_name" binding:"required"`
}
