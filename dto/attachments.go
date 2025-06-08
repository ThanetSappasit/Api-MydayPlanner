package dto

type CreateAttachmentsTodayTaskRequest struct {
	Filename string `json:"filename" binding:"required"`
	Filepath string `json:"filepath" binding:"required"`
	Filetype string `json:"filetype" binding:"required"`
}
type CreateAttachmentsTaskRequest struct {
	Filename string `json:"filename" binding:"required"`
	Filepath string `json:"filepath" binding:"required"`
	Filetype string `json:"filetype" binding:"required"`
}

type DeleteAttachmentRequest struct {
	TaskID       string `json:"task_id" binding:"required"`       // Task ID ที่ attachment อยู่ภายใต้
	AttachmentID string `json:"attachment_id" binding:"required"` // Attachment ID ที่จะลบ
}
