package dto

type CreateTaskRequest struct {
	BoardID    int    `json:"board_id" binding:"required"`
	TaskName   string `json:"task_name" binding:"required"`
	Desciption string `json:"description"`
	Status     string `json:"status" binding:"required"`
	Priority   string `json:"priority"`
	CreatedBy  int    `json:"created_by" binding:"required"`
}

type CreateTodayTaskRequest struct {
	TaskName    string  `json:"task_name" binding:"required"`
	Description *string `json:"description,omitempty"`
	Priority    *string `json:"priority,omitempty"`
	Status      string  `json:"status" binding:"required"`
}

type DataTodayTaskByNameRequest struct {
	Email    string `json:"email" binding:"required"`
	TaskName string `json:"task_name" binding:"required"`
}

type FinishTodayTaskRequest struct {
	Email    string `json:"email" binding:"required"`
	TaskName string `json:"task_name" binding:"required"`
}

type AdjustTodayTaskRequest struct {
	FirebaseTaskName *string `json:"fbtask_name"`
	TaskName         *string `json:"task_name"`
	Description      *string `json:"description"`
	Status           *string `json:"status" `
	Priority         *string `json:"priority"`
}
