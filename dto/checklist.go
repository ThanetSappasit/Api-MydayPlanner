package dto

type CreateChecklistTodayTaskRequest struct {
	TaskID        string `json:"task_id" binding:"required"`
	Checklistname string `json:"checklist_name" binding:"required"`
}

type CreateChecklistTaskRequest struct {
	BoardID       string `json:"board_id" binding:"required"`
	TaskID        string `json:"task_id" binding:"required"`
	ChecklistName string `json:"checklist_name" binding:"required"`
	Isgroup       string `json:"is_group" binding:"required"`
}
