package dto

type CreateAttachmentsTaskRequest struct {
	TaskID   string `json:"task_id" binding:"required"`
	Filename string `json:"filename" binding:"required"`
	Filepath string `json:"filepath" binding:"required"`
	Filetype string `json:"filetype" binding:"required"`
}
