package dto

type UpdateProfileRequest struct {
	Name           string `json:"name"`
	HashedPassword string `json:"password"`
	Profile        string `json:"profile"`
}
type EmailRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type PasswordRequest struct {
	OldPassword string `json:"oldpassword" binding:"required"`
	NewPassword string `json:"newpassword" binding:"required"`
}

type EmailText struct {
	Email string
}
