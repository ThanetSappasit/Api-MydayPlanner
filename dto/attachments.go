package dto

type CreateAttachmentsTodayTaskRequest struct {
	TaskID   string `json:"task_id" binding:"required"`
	Filename string `json:"filename" binding:"required"`
	Filepath string `json:"filepath" binding:"required"`
	Filetype string `json:"filetype" binding:"required"`
}
type CreateAttachmentsTaskRequest struct {
	BoardID  string `json:"board_id" binding:"required"`
	TaskID   string `json:"task_id" binding:"required"`
	Filename string `json:"filename" binding:"required"`
	Filepath string `json:"filepath" binding:"required"`
	Filetype string `json:"filetype" binding:"required"`
	Isgroup  string `json:"is_group" binding:"required"`
}
