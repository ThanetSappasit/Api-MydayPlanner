package dto

type GetBoardsRequest struct {
	Email string `json:"UserId" validate:"required"`
	Group string `json:"is_group"`
}
