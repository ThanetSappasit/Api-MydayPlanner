package dto

type CaptchaRequest struct {
	Token  string `json:"token" validate:"required"`
	Action string `json:"action" validate:"required"`
}

type AssessmentResult struct {
	Score   float32
	Action  string
	Reasons []string
}
