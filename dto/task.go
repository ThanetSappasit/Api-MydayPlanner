package dto

type CreateTaskRequest struct {
	BoardID     int       `json:"board_id" binding:"required"`
	TaskName    string    `json:"task_name" binding:"required"`
	Description string    `json:"description"`
	Status      string    `json:"status" binding:"required"`
	Reminder    *Reminder `json:"reminder"`
	Priority    string    `json:"priority"`
}

type CreateTodayTaskRequest struct {
	TaskName    string    `json:"task_name" binding:"required"`
	Description string    `json:"description"`
	Status      string    `json:"status" binding:"required"`
	Reminder    *Reminder `json:"reminder"`
	Priority    string    `json:"priority"`
}

type Reminder struct {
	DueDate          string  `json:"due_date"`
	BeforeDueDate    *string `json:"before_due_date"`
	RecurringPattern string  `json:"recurring_pattern,omitempty"`
}

type DeletetaskRequest struct {
	TaskID []string `json:"task_id" validate:"required"`
}

type AdjustTaskRequest struct {
	TaskName    string `json:"task_name"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

type StatusRequest struct {
	Status string `json:"status" binding:"required"`
}
