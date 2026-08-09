package auth

import (
	"errors"
	"time"
)

var (
	ErrInvalidToken      = errors.New("invalid token")
	ErrExpiredToken      = errors.New("token has expired")
	ErrWrongTokenPurpose = errors.New("token was issued for a different purpose")
)

// TokenRequest describes a token to mint.
type TokenRequest struct {
	UserID   string
	TenantID string
	Role     string
	Purpose  TokenPurpose
	Duration time.Duration
}

// TokenMaker issues and verifies bearer tokens. Implementations must be safe
// for concurrent use.
type TokenMaker interface {
	CreateToken(req TokenRequest) (string, error)
	VerifyToken(token string, purpose TokenPurpose) (*TokenPayload, error)
}
