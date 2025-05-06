package model

type ResponseData struct {
	Success bool     `json:"success"`
	Score   float32  `json:"score,omitempty"`
	Action  string   `json:"action,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
	Message string   `json:"message,omitempty"`
}

func (ResponseData) TableName() string {
	return "response"
}
