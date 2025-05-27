package dto

type CreateChecklistTaskRequest struct {
	TaskID        string `json:"task_id" binding:"required"`
	Checklistname string `json:"checklist_name" binding:"required"`
}
