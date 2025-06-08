package dto

type GetBoardsRequest struct {
	Email string `json:"UserId" validate:"required"`
	Group string `json:"is_group"`
}

type CreateBoardRequest struct {
	BoardName string `json:"board_name" validate:"required"`
	Is_group  string `json:"is_group"`
}

type DeleteBoardRequest struct {
	BoardID []string `json:"board_id" validate:"required"`
}

type AdjustBoardRequest struct {
	BoardID   string `json:"board_id" validate:"required"`
	BoardName string `json:"board_name" validate:"required"`
}

type InviteBoardRequest struct {
	BoardID string `json:"board_id" validate:"required"`
	UserID  string `json:"user_id" validate:"required"`
}

type AcceptBoardRequest struct {
	InviteID string `json:"inviteid" validate:"required"`
	Accept   bool   `json:"accept" validate:"required"`
}
