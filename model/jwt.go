package model

import "github.com/golang-jwt/jwt/v5"

type TokenResponse struct {
	UserID       int    `json:"userId"`
	RefreshToken string `json:"refreshToken"`
	CreatedAt    int64  `json:"createdAt"` // creation time in seconds
	Revoked      bool   `json:"revoked"`   // whether the token is revoked
	ExpiresIn    int64  `json:"expiresIn"` // expiration in seconds
}

func (TokenResponse) TableName() string {
	return "token"
}

type AccessClaims struct {
	UserID  uint   `json:"userId"`
	Email   string `json:"email"`
	Role    string `json:"role,omitempty"`
	TokenID string `json:"tokenId,omitempty"` // For refresh token tracking
	jwt.RegisteredClaims
}

func (AccessClaims) TableName() string {
	return "claims"
}

type AccessRefresh struct {
	UserID  uint   `json:"userId"`
	Email   string `json:"email"`
	TokenID string `json:"tokenId,omitempty"` // For refresh token tracking
	jwt.RegisteredClaims
}

func (AccessRefresh) TableName() string {
	return "token"
}
