package dto

type CreateChecklistTodayTaskRequest struct {
	Checklistname string `json:"checklist_name" binding:"required"`
}

type CreateChecklistTaskRequest struct {
	BoardID       string `json:"board_id" binding:"required"`
	TaskID        string `json:"task_id" binding:"required"`
	ChecklistName string `json:"checklist_name" binding:"required"`
}

type AdjustTodayChecklistRequest struct {
	TaskID        string `json:"task_id" binding:"required"`      // Task ID ที่ checklist อยู่ภายใต้
	ChecklistID   string `json:"checklist_id" binding:"required"` // Checklist ID ที่จะอัปเดต
	ChecklistName string `json:"checklist_name"`                  // ชื่อ checklist ใหม่
}

type FinishTodayChecklistRequest struct {
	TaskID      string `json:"task_id" binding:"required"`      // Task ID ที่ checklist อยู่ภายใต้
	ChecklistID string `json:"checklist_id" binding:"required"` // Checklist ID ที่จะอัปเดต
}

type AdjustChecklistRequest struct {
	TaskID        string `json:"task_id" binding:"required"`
	ChecklistID   string `json:"checklist_id" binding:"required"`
	ChecklistName string `json:"checklist_name" binding:"required"`
}

type DeleteChecklistRequest struct {
	TaskID      string   `json:"task_id" binding:"required"`
	ChecklistID []string `json:"checklist_id" binding:"required"` // ใช้ []string เพื่อรองรับการลบหลาย task
}

type DeleteChecklistTodayRequest struct {
	TaskID      string `json:"task_id" binding:"required"`
	ChecklistID string `json:"checklist_id" binding:"required"` // ใช้ []string เพื่อรองรับการลบหลาย task
}
