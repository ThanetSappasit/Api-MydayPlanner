package dto

type AdminRequest struct {
	Email          string `json:"email"`
	HashedPassword string `json:"password"`
}
