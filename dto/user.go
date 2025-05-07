package dto

type UpdateProfileRequest struct {
	Name           string `json:"name"`
	HashedPassword string `json:"password"`
	Profile        string `json:"profile"`
}
