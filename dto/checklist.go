package dto

type CreateChecklistTodayTaskRequest struct {
	Checklistname string `json:"checklist_name" binding:"required"`
}

type CreateChecklistTaskRequest struct {
	ChecklistName string `json:"checklist_name" binding:"required"`
}

type UpdateChecklistRequest struct {
	ChecklistName string `json:"checklist_name" binding:"required,min=1,max=255"`
}

type FinishTodayChecklistRequest struct {
	TaskID      string `json:"task_id" binding:"required"`      // Task ID ที่ checklist อยู่ภายใต้
	ChecklistID string `json:"checklist_id" binding:"required"` // Checklist ID ที่จะอัปเดต
}

type AdjustChecklistRequest struct {
	ChecklistName string `json:"checklist_name" binding:"required"`
}

type DeleteChecklistRequest struct {
	ChecklistIDs []string `json:"checklist_id" binding:"required"` // ใช้ []string เพื่อรองรับการลบหลาย task
}

type DeleteChecklistTodayRequest struct {
	TaskID      string `json:"task_id" binding:"required"`
	ChecklistID string `json:"checklist_id" binding:"required"` // ใช้ []string เพื่อรองรับการลบหลาย task
}
