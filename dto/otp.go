package dto

type IdentityOTPRequest struct {
	Email     string `json:"email"`
	Reference string `json:"reference"`
	Record    string `json:"record"`
}
type ResetpasswordOTPRequest struct {
	Email     string `json:"email"`
	Reference string `json:"reference"`
	Record    string `json:"record"`
}
type SendemailRequest struct {
	Email     string `json:"email"`
	Reference string `json:"reference"`
	Record    string `json:"record"`
}

type ResendOTPRequest struct {
	Email  string `json:"email"`
	Record string `json:"record"`
}

type VerifyRequest struct {
	Email     string `json:"email" binding:"required"`
	Reference string `json:"ref" binding:"required"`
	OTP       string `json:"otp" binding:"required"`
	Record    string `json:"record"`
}
