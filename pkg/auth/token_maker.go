package auth

import (
	"errors"
	"time"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token has expired")
)

type TokenMaker interface {
	CreateToken(userID string, tenantID string, role string, duration time.Duration) (string, error)
	VerifyToken(token string) (*TokenPayload, error)
}
