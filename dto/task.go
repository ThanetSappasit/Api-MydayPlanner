package dto

type CreateTaskRequest struct {
	BoardID     int    `json:"board_id" binding:"required"`
	TaskName    string `json:"task_name" binding:"required"`
	Description string `json:"description"`
	Status      string `json:"status" binding:"required"`
	Priority    string `json:"priority"`
}

type CreateTodayTaskRequest struct {
	TaskName    string  `json:"task_name" binding:"required"`
	Description *string `json:"description,omitempty"`
	Priority    *string `json:"priority,omitempty"`
	Status      string  `json:"status" binding:"required"`
}

type DataTodayTaskByNameRequest struct {
	TaskID string `json:"task_id" binding:"required"`
}

type FinishTodayTaskRequest struct {
	TaskID string `json:"task_id" binding:"required"`
}

type AdjustTodayTaskRequest struct {
	TaskName    string `json:"task_name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	DocumentID  string `json:"document_id" binding:"required"`
}

type DeletetaskRequest struct {
	BoardID int      `json:"board_id" binding:"required"`
	TaskID  []string `json:"task_id" validate:"required"`
}

type DeleteTodayTaskRequest struct {
	TaskID []string `json:"task_id" validate:"required"`
}

type DeleteIDTodayTaskRequest struct {
	TaskID string `json:"task_id" validate:"required"`
}

type AdjustTaskRequest struct {
	BoardID     int    `json:"board_id" binding:"required"`
	TaskID      int    `json:"task_id" binding:"required"`
	TaskName    string `json:"task_name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
}
