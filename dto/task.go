package dto

type CreateTaskRequest struct {
	BoardID    int    `json:"board_id" binding:"required"`
	TaskName   string `json:"task_name" binding:"required"`
	Desciption string `json:"description"`
	Status     string `json:"status" binding:"required"`
	Priority   string `json:"priority"`
	CreatedBy  int    `json:"created_by" binding:"required"`
}
